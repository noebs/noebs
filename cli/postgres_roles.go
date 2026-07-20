package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/adonese/noebs/internal/postgresauthority"
)

type postgresRoleSpec struct {
	service  serviceRole
	owner    serviceRole
	username string
	database string
}

var servicePostgresRoleSpecs = []postgresRoleSpec{
	{service: serviceRoleIdentityAuthMigrate, owner: serviceRoleIdentityAuth, username: "identity_auth_migrate", database: "identity_auth"},
	{service: serviceRoleIdentityAuth, owner: serviceRoleIdentityAuth, username: "identity_auth_runtime", database: "identity_auth"},
	{service: serviceRoleCardVaultMigrate, owner: serviceRoleCardVault, username: "card_vault_migrate", database: "card_vault"},
	{service: serviceRoleCardVault, owner: serviceRoleCardVault, username: "card_vault_runtime", database: "card_vault"},
	{service: serviceRoleEBSAdapterMigrate, owner: serviceRoleEBSAdapter, username: "ebs_adapter_migrate", database: "ebs_adapter"},
	{service: serviceRoleEBSAdapter, owner: serviceRoleEBSAdapter, username: "ebs_adapter_runtime", database: "ebs_adapter"},
	{service: serviceRoleEBSAdapterEvents, owner: serviceRoleEBSAdapter, username: "ebs_adapter_events", database: "ebs_adapter"},
	{service: serviceRolePSPWebhook, owner: serviceRoleWalletLedger, username: "wallet_ledger_webhook", database: "wallet_ledger"},
	{service: serviceRoleAdminReportingMigrate, owner: serviceRoleAdminReporting, username: "admin_reporting_migrate", database: "admin_reporting"},
	{service: serviceRoleAdminReporting, owner: serviceRoleAdminReporting, username: "admin_reporting_runtime", database: "admin_reporting"},
	{service: serviceRoleAdminReportingProjector, owner: serviceRoleAdminReporting, username: "admin_reporting_projector", database: "admin_reporting"},
	{service: serviceRoleNotificationMigrate, owner: serviceRoleNotification, username: "notification_chat_migrate", database: "notification_chat"},
	{service: serviceRoleNotification, owner: serviceRoleNotification, username: "notification_chat_runtime", database: "notification_chat"},
	{service: serviceRoleWalletLedgerMigrate, owner: serviceRoleWalletLedger, username: "wallet_ledger_migrate", database: "wallet_ledger"},
	{service: serviceRoleWalletLedger, owner: serviceRoleWalletLedger, username: "wallet_ledger_runtime", database: "wallet_ledger"},
	{service: serviceRoleWalletWorker, owner: serviceRoleWalletLedger, username: "wallet_ledger_worker", database: "wallet_ledger"},
}

var workloadAuthRuntimePostgresRoleSpec = postgresRoleSpec{username: "workload_auth_runtime", database: "workload_auth"}

var authPostgresRoleSpecs = []postgresRoleSpec{
	{service: serviceRoleWorkloadAuthMigrate, owner: serviceRoleWorkloadAuthMigrate, username: "workload_auth_migrate", database: "workload_auth"},
	workloadAuthRuntimePostgresRoleSpec,
	{service: serviceRoleWorkloadAuthCleanup, owner: serviceRoleWorkloadAuthMigrate, username: "workload_auth_cleanup", database: "workload_auth"},
	{service: serviceRoleGatewayAuthMigrate, owner: serviceRoleAPIGateway, username: "gateway_auth_migrate", database: "gateway_auth"},
	{service: serviceRoleAPIGateway, owner: serviceRoleAPIGateway, username: "gateway_auth_runtime", database: "gateway_auth"},
	{service: serviceRoleGatewayAuthCleanup, owner: serviceRoleAPIGateway, username: "gateway_auth_cleanup", database: "gateway_auth"},
}

func postgresRoleSpecForService(role serviceRole) (postgresRoleSpec, bool) {
	for _, spec := range servicePostgresRoleSpecs {
		if spec.service == role {
			return spec, true
		}
	}
	for _, spec := range authPostgresRoleSpecs {
		if spec.service == role {
			return spec, true
		}
	}
	return postgresRoleSpec{}, false
}

func servicePostgresRoleNames() []string {
	result := make([]string, 0, len(servicePostgresRoleSpecs))
	for _, spec := range servicePostgresRoleSpecs {
		result = append(result, spec.username)
	}
	sort.Strings(result)
	return result
}

func allPostgresRoleSpecs() []postgresRoleSpec {
	result := make([]postgresRoleSpec, 0, len(servicePostgresRoleSpecs)+len(authPostgresRoleSpecs))
	result = append(result, servicePostgresRoleSpecs...)
	result = append(result, authPostgresRoleSpecs...)
	return result
}

func prepareServicePostgresPasswords(values map[string]string) (map[string]string, error) {
	if err := validatePostgresRoleCatalog(); err != nil {
		return nil, err
	}
	expected := make(map[string]bool, len(servicePostgresRoleSpecs))
	for _, spec := range servicePostgresRoleSpecs {
		expected[spec.username] = true
	}
	for role := range values {
		if !expected[role] {
			return nil, fmt.Errorf("unsupported service database role %q", role)
		}
	}
	passwords := make(map[string]string, len(expected))
	seen := make(map[string]string, len(expected))
	for _, role := range servicePostgresRoleNames() {
		password, present := values[role]
		if !present {
			return nil, fmt.Errorf("service database role %s password is required", role)
		}
		password, err := requireCanonicalReleaseSecret("service database role "+role+" password", password)
		if err != nil {
			return nil, err
		}
		if prior, duplicate := seen[password]; duplicate {
			return nil, fmt.Errorf("service database roles %s and %s must use distinct passwords", prior, role)
		}
		seen[password] = role
		passwords[role] = password
	}
	return passwords, nil
}

func validateAllPostgresRolePasswords(service map[string]string, workload preparedWorkloadDatabase, gateway preparedGatewayAuthRelease) error {
	seen := make(map[string]string, len(service)+6)
	check := func(role, password string) error {
		if prior, duplicate := seen[password]; duplicate {
			return fmt.Errorf("database roles %s and %s must use globally distinct passwords", prior, role)
		}
		seen[password] = role
		return nil
	}
	for _, role := range servicePostgresRoleNames() {
		if err := check(role, service[role]); err != nil {
			return err
		}
	}
	authPasswords := []string{
		workload.migratePassword,
		workload.runtimePassword,
		workload.cleanupPassword,
		gateway.migratePassword,
		gateway.runtimePassword,
		gateway.cleanupPassword,
	}
	for index, spec := range authPostgresRoleSpecs {
		if err := check(spec.username, authPasswords[index]); err != nil {
			return err
		}
	}
	return nil
}

func exactPostgresURL(host string, spec postgresRoleSpec, password string) string {
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(spec.username, password),
		Host:     host,
		Path:     "/" + spec.database,
		RawQuery: "sslmode=verify-full",
	}).String()
}

func encodeServicePostgresPasswordFile(passwords map[string]string) (string, error) {
	if len(passwords) != len(servicePostgresRoleSpecs)+6 {
		return "", errors.New("complete service database role password set is required")
	}
	roles := make([]string, 0, len(passwords))
	for role, password := range passwords {
		decoded, err := base64.RawURLEncoding.DecodeString(password)
		if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != password {
			return "", fmt.Errorf("database role %s password is invalid", role)
		}
		roles = append(roles, role)
	}
	sort.Strings(roles)
	var result strings.Builder
	for _, role := range roles {
		result.WriteString(role)
		result.WriteByte('=')
		result.WriteString(passwords[role])
		result.WriteByte('\n')
	}
	return result.String(), nil
}

func validatePostgresProvisioningSQLFile(path string) error {
	if err := validatePostgresRoleCatalog(); err != nil {
		return err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Postgres provisioning SQL: %w", err)
	}
	sql := string(payload)
	for _, forbidden := range []string{
		"OWNER noebs",
		" TO noebs;",
		" TO noebs,",
		"psp_webhook_migrate",
		"psp_webhook_runtime",
		"CREATE DATABASE psp_webhook",
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES",
		"GRANT USAGE ON ALL SEQUENCES",
	} {
		if strings.Contains(sql, forbidden) {
			return errors.New("postgres provisioning SQL must not grant the retired shared database authority")
		}
	}
	for _, required := range []string{
		"SET password_encryption = 'scram-sha-256';",
		"FROM pg_auth_members edge",
		"REVOKE %I FROM %I CASCADE",
		"REVOKE ALL PRIVILEGES ON DATABASE %I FROM %I CASCADE",
		"ALTER ROLE %I IN DATABASE %I RESET ALL",
		"REVOKE ALL PRIVILEGES ON SCHEMA public FROM %I CASCADE",
	} {
		if !strings.Contains(sql, required) {
			return errors.New("postgres provisioning SQL does not converge exact role and database authority")
		}
	}
	for _, spec := range allPostgresRoleSpecs() {
		if !strings.Contains(sql, "('"+spec.username+"', :'") ||
			!strings.Contains(sql, "ALTER ROLE %I WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1 VALID UNTIL %L PASSWORD %L") ||
			!strings.Contains(sql, "('"+spec.database+"', '"+spec.username+"', ") {
			return fmt.Errorf("postgres provisioning SQL does not provision role %s", spec.username)
		}
	}
	for _, spec := range allPostgresRoleSpecs() {
		if strings.HasSuffix(spec.username, "_migrate") {
			create := "CREATE DATABASE " + spec.database + " OWNER " + spec.username
			if !strings.Contains(sql, create) ||
				!strings.Contains(sql, "ALTER SCHEMA public OWNER TO "+spec.username+";") {
				return fmt.Errorf("postgres provisioning SQL does not assign database and schema authority to %s", spec.username)
			}
		}
	}
	return nil
}

func validatePostgresRoleCatalog() error {
	roles := postgresauthority.Roles()
	specs := allPostgresRoleSpecs()
	if len(roles) != len(specs) {
		return fmt.Errorf("postgres role catalogs differ: authority has %d roles, release has %d", len(roles), len(specs))
	}
	expected := make(map[string]postgresauthority.Role, len(roles))
	for _, role := range roles {
		expected[role.Name] = role
	}
	seen := make(map[string]bool, len(roles))
	for _, spec := range specs {
		role, ok := expected[spec.username]
		if !ok || seen[spec.username] || role.Database != spec.database || role.Migration != strings.HasSuffix(spec.username, "_migrate") {
			return fmt.Errorf("postgres release role %s does not match the authority catalog", spec.username)
		}
		seen[spec.username] = true
	}
	return nil
}
