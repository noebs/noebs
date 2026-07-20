package main

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/transportauth"
)

type dockerDatabaseRoleCredential struct {
	roleName string
	database string
	password string
}

func validateDockerPostgresTLS(root string) (string, error) {
	read := func(label, fileName string) (string, error) {
		path := filepath.Join(root, "deploy", "docker", "postgres", fileName)
		if err := requireReadableFile(label, path); err != nil {
			return "", err
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", label, err)
		}
		return string(payload), nil
	}

	caCertificate, err := read("Noebs Postgres transport CA certificate", "ca.pem")
	if err != nil {
		return "", err
	}
	certificate, err := read("Noebs Postgres TLS certificate", "tls.crt")
	if err != nil {
		return "", err
	}
	privateKey, err := read("Noebs Postgres TLS private key", "tls.key")
	if err != nil {
		return "", err
	}
	identity := transportauth.Config{
		CACertificate: caCertificate,
		Certificate:   certificate,
		PrivateKey:    privateKey,
	}
	if err := identity.ValidateIdentity("db"); err != nil {
		return "", fmt.Errorf("validate Noebs Postgres TLS identity: %w", err)
	}
	if err := validateServerCertificateIdentity(certificate, "db"); err != nil {
		return "", fmt.Errorf("validate Noebs Postgres TLS identity: %w", err)
	}
	return strings.TrimSpace(caCertificate), nil
}

func validateServerCertificateIdentity(certificate string, dnsNames ...string) error {
	leaf, err := parseLeafCertificate(certificate)
	if err != nil {
		return err
	}
	if err := validateCertificateDNSIdentities(leaf, dnsNames); err != nil {
		return err
	}
	if leaf.IsCA {
		return errors.New("server certificate must not be a CA")
	}
	if leaf.KeyUsage != x509.KeyUsageDigitalSignature {
		return errors.New("server certificate must permit only digital signatures")
	}
	if !slices.Equal(leaf.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}) || len(leaf.UnknownExtKeyUsage) != 0 {
		return errors.New("server certificate must permit only server authentication")
	}
	return nil
}

func validateClientCertificateIdentity(certificate string, dnsName string) error {
	leaf, err := parseLeafCertificate(certificate)
	if err != nil {
		return err
	}
	if err := validateCertificateDNSIdentities(leaf, []string{dnsName}); err != nil {
		return err
	}
	if leaf.IsCA || leaf.KeyUsage != x509.KeyUsageDigitalSignature {
		return errors.New("client certificate must be a digital-signature leaf")
	}
	if !slices.Equal(leaf.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}) || len(leaf.UnknownExtKeyUsage) != 0 {
		return errors.New("client certificate must permit only client authentication")
	}
	return nil
}

func validateServiceCertificateIdentity(certificate, identity string) error {
	leaf, err := parseLeafCertificate(certificate)
	if err != nil {
		return err
	}
	dnsNames := []string{identity, identity + ".noebs.svc", identity + ".noebs.svc.cluster.local"}
	if err := validateCertificateDNSIdentities(leaf, dnsNames); err != nil {
		return err
	}
	if leaf.IsCA || leaf.KeyUsage != x509.KeyUsageDigitalSignature {
		return errors.New("service certificate must be a digital-signature leaf")
	}
	if !slices.Equal(leaf.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}) || len(leaf.UnknownExtKeyUsage) != 0 {
		return errors.New("service certificate must permit only client and server authentication")
	}
	return nil
}

func parseLeafCertificate(certificate string) (*x509.Certificate, error) {
	block, rest := pem.Decode([]byte(certificate))
	if block == nil || block.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("invalid leaf certificate")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, errors.New("invalid leaf certificate")
	}
	return leaf, nil
}

func validateCertificateDNSIdentities(leaf *x509.Certificate, dnsNames []string) error {
	if len(dnsNames) == 0 || !slices.Equal(leaf.DNSNames, dnsNames) || len(leaf.IPAddresses) != 0 || len(leaf.EmailAddresses) != 0 || len(leaf.URIs) != 0 {
		return fmt.Errorf("certificate DNS identities must be exactly %v", dnsNames)
	}
	for _, identity := range dnsNames {
		if leaf.VerifyHostname(identity) != nil {
			return fmt.Errorf("certificate must carry DNS identity %q", identity)
		}
	}
	return nil
}

func validateDockerDatabaseRoleCredentials(root string, configMap map[string]interface{}, ageKeyPath, databaseCA string, decrypt deploymentDecryptFunc) error {
	passwords, err := readDockerPostgresRolePasswords(root)
	if err != nil {
		return err
	}
	servicePasswords := make(map[string]string, len(servicePostgresRoleSpecs))
	for _, spec := range servicePostgresRoleSpecs {
		servicePasswords[spec.username] = passwords[spec.username]
	}
	workload := preparedWorkloadDatabase{
		migratePassword: passwords["workload_auth_migrate"],
		runtimePassword: passwords["workload_auth_runtime"],
		cleanupPassword: passwords["workload_auth_cleanup"],
	}
	gateway := preparedGatewayAuthRelease{
		migratePassword: passwords["gateway_auth_migrate"],
		runtimePassword: passwords["gateway_auth_runtime"],
		cleanupPassword: passwords["gateway_auth_cleanup"],
	}
	if err := validateAllPostgresRolePasswords(servicePasswords, workload, gateway); err != nil {
		return err
	}

	secrets := make(map[string]map[string]interface{}, len(kubernetesSecretReleaseServiceNames))
	services := make(map[serviceRole]kubernetesReleaseServiceConfig, len(kubernetesSecretReleaseServiceNames))
	serviceSecret := func(serviceName string) (map[string]interface{}, error) {
		if secret, ok := secrets[serviceName]; ok {
			return secret, nil
		}
		secretPath := filepath.Join(root, "deploy", "docker", "secrets", serviceName+".secrets.yaml")
		if err := requireReadableFile(serviceName+" secrets", secretPath); err != nil {
			return nil, err
		}
		payload, err := decrypt(secretPath, ageKeyPath)
		if err != nil {
			return nil, fmt.Errorf("decrypt %s secrets: %w", serviceName, err)
		}
		secret, err := parseYAMLMap(secretPath, payload)
		if err != nil {
			return nil, err
		}
		noebs := getMap(secret, "noebs")
		if noebs == nil {
			return nil, fmt.Errorf("%s secrets missing noebs", serviceName)
		}
		secrets[serviceName] = noebs
		return noebs, nil
	}

	for _, serviceName := range kubernetesSecretReleaseServiceNames {
		servicePath := filepath.Join(root, "deploy", "docker", "services", serviceName+".yaml")
		serviceMap, err := readYAMLMapFile(servicePath)
		if err != nil {
			return err
		}
		secret, err := serviceSecret(serviceName)
		if err != nil {
			return err
		}
		merged := mergeConfig(configMap, serviceMap).(map[string]interface{})
		merged = mergeConfig(merged, map[string]interface{}{"noebs": secret}).(map[string]interface{})
		noebs := getMap(merged, "noebs")
		if err := applyServiceDatabaseURL(noebs); err != nil {
			return fmt.Errorf("%s service database config: %w", serviceName, err)
		}
		role, err := parseServiceRole(firstString(noebs, "service_role"))
		if err != nil {
			return fmt.Errorf("%s service_role: %w", serviceName, err)
		}
		encoded, err := json.Marshal(noebs)
		if err != nil {
			return fmt.Errorf("encode %s merged config: %w", serviceName, err)
		}
		var value ebs_fields.NoebsConfig
		if err := json.Unmarshal(encoded, &value); err != nil {
			return fmt.Errorf("decode %s merged config: %w", serviceName, err)
		}
		services[role] = kubernetesReleaseServiceConfig{role: role, noebs: noebs, value: value}
		if roleUsesInternalTransportIdentity(role) {
			if strings.TrimSpace(value.InternalTransport.CACertificate) != databaseCA {
				return fmt.Errorf("%s internal transport CA does not match the Docker transport authority", serviceName)
			}
			if err := value.InternalTransport.ValidateIdentity(string(role)); err != nil {
				return fmt.Errorf("validate %s internal transport identity: %w", serviceName, err)
			}
			if err := validateServiceCertificateIdentity(value.InternalTransport.Certificate, string(role)); err != nil {
				return fmt.Errorf("validate %s internal transport identity: %w", serviceName, err)
			}
		} else if value.InternalTransport.Present() {
			return fmt.Errorf("%s must not carry an internal transport identity", serviceName)
		}
		if !role.opensDatabase() && !roleReceivesSignedHTTP(role) {
			continue
		}
		if strings.TrimSpace(firstString(secret, "database_ca_certificate")) != databaseCA {
			return fmt.Errorf("%s database CA does not match the Docker Postgres transport CA", serviceName)
		}
		if role.opensDatabase() {
			spec, present := postgresRoleSpecForService(role)
			if !present {
				return fmt.Errorf("%s database role is not cataloged", serviceName)
			}
			databaseURL := firstString(getMap(secret, "service_databases"), string(spec.owner))
			credential := dockerDatabaseRoleCredential{roleName: spec.username, database: spec.database, password: passwords[spec.username]}
			if err := requireExactDockerDatabaseURL(serviceName, databaseURL, credential); err != nil {
				return err
			}
		}
		if !roleReceivesSignedHTTP(role) {
			continue
		}
		nonceDatabaseURL := firstString(getMap(secret, "workload_auth"), "nonce_db_url")
		runtimeCredential := dockerDatabaseRoleCredential{roleName: "workload_auth_runtime", database: "workload_auth", password: passwords["workload_auth_runtime"]}
		if err := requireExactDockerDatabaseURL(serviceName+" workload nonce", nonceDatabaseURL, runtimeCredential); err != nil {
			return err
		}
	}
	return validateReleaseWorkloadKeyProjection(services)
}

func readDockerPostgresRolePasswords(root string) (map[string]string, error) {
	path := filepath.Join(root, "deploy", "docker", "postgres", "service-role-passwords.env")
	if err := requireReadableFile("Docker Postgres role password catalog", path); err != nil {
		return nil, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Docker Postgres role password catalog: %w", err)
	}
	text := string(payload)
	if text == "" || !strings.HasSuffix(text, "\n") || strings.Contains(text, "\r") || strings.HasSuffix(text, "\n\n") {
		return nil, errors.New("docker Postgres role password catalog must be canonical newline-delimited ROLE=PASSWORD records")
	}
	expected := make(map[string]postgresRoleSpec, len(allPostgresRoleSpecs()))
	for _, spec := range allPostgresRoleSpecs() {
		expected[spec.username] = spec
	}
	passwords := make(map[string]string, len(expected))
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		role, password, present := strings.Cut(line, "=")
		if !present || role == "" || password == "" || strings.Contains(password, "=") {
			return nil, errors.New("docker Postgres role password catalog contains an invalid record")
		}
		if _, present := expected[role]; !present {
			return nil, fmt.Errorf("docker Postgres role password catalog contains unsupported role %q", role)
		}
		if _, duplicate := passwords[role]; duplicate {
			return nil, fmt.Errorf("docker Postgres role password catalog repeats role %q", role)
		}
		password, err = requireCanonicalReleaseSecret("database role "+role+" password", password)
		if err != nil {
			return nil, err
		}
		passwords[role] = password
	}
	for role := range expected {
		if _, present := passwords[role]; !present {
			return nil, fmt.Errorf("docker Postgres role password catalog missing role %q", role)
		}
	}
	return passwords, nil
}

func requireExactDockerDatabaseURL(label, value string, credential dockerDatabaseRoleCredential) error {
	expected := dockerDatabaseURL(credential.roleName, credential.password, credential.database)
	if value != expected {
		return fmt.Errorf("%s database URL must exactly bind role %s to database %s on the Docker Postgres endpoint", label, credential.roleName, credential.database)
	}
	return nil
}

func dockerDatabaseURL(roleName, password, database string) string {
	return (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(roleName, password),
		Host:     "db:5432",
		Path:     "/" + database,
		RawQuery: "sslmode=verify-full",
	}).String()
}
