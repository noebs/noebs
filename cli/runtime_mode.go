package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/adonese/noebs/store"
)

type serviceRole string

var (
	errMissingServiceRole  = errors.New("missing noebs.service_role")
	errInvalidServiceRole  = errors.New("invalid noebs.service_role")
	errMissingDatabaseURL  = errors.New("missing noebs.db_url")
	errMissingDatabasePath = errors.New("missing noebs.db_path")
	errDatabaseNotAllowed  = errors.New("database config not allowed for service role")
	errDatabaseOwnerKey    = errors.New("service database entry must use owner role")
)

const (
	serviceRoleAPIGateway     serviceRole = "api-gateway"
	serviceRoleIdentityAuth   serviceRole = "identity-auth"
	serviceRoleCardVault      serviceRole = "card-vault"
	serviceRoleEBSAdapter     serviceRole = "ebs-adapter"
	serviceRolePSPWebhook     serviceRole = "psp-webhook"
	serviceRoleAdminReporting serviceRole = "admin-reporting"
	serviceRoleNotification   serviceRole = "notification-chat"
	serviceRoleBeneficiary    serviceRole = "consumer-beneficiary"
	serviceRoleWalletAPI      serviceRole = "wallet-api"
	serviceRoleWalletLedger   serviceRole = "wallet-ledger"
	serviceRoleWalletWorker   serviceRole = "wallet-worker"

	serviceRoleIdentityAuthMigrate   serviceRole = "identity-auth-migrate"
	serviceRoleCardVaultMigrate      serviceRole = "card-vault-migrate"
	serviceRoleEBSAdapterMigrate     serviceRole = "ebs-adapter-migrate"
	serviceRolePSPWebhookMigrate     serviceRole = "psp-webhook-migrate"
	serviceRoleAdminReportingMigrate serviceRole = "admin-reporting-migrate"
	serviceRoleNotificationMigrate   serviceRole = "notification-chat-migrate"
	serviceRoleBeneficiaryMigrate    serviceRole = "consumer-beneficiary-migrate"
	serviceRoleWalletLedgerMigrate   serviceRole = "wallet-ledger-migrate"
)

func currentServiceRole() (serviceRole, error) {
	return parseServiceRole(noebsConfig.ServiceRole)
}

func parseServiceRole(value string) (serviceRole, error) {
	role := serviceRole(strings.TrimSpace(value))
	switch role {
	case "":
		return "", errMissingServiceRole
	case serviceRoleAPIGateway,
		serviceRoleIdentityAuth,
		serviceRoleCardVault,
		serviceRoleEBSAdapter,
		serviceRolePSPWebhook,
		serviceRoleAdminReporting,
		serviceRoleNotification,
		serviceRoleBeneficiary,
		serviceRoleWalletAPI,
		serviceRoleWalletLedger,
		serviceRoleWalletWorker,
		serviceRoleIdentityAuthMigrate,
		serviceRoleCardVaultMigrate,
		serviceRoleEBSAdapterMigrate,
		serviceRolePSPWebhookMigrate,
		serviceRoleAdminReportingMigrate,
		serviceRoleNotificationMigrate,
		serviceRoleBeneficiaryMigrate,
		serviceRoleWalletLedgerMigrate:
		return role, nil
	default:
		return "", fmt.Errorf("%w: %q", errInvalidServiceRole, value)
	}
}

func (r serviceRole) startsHTTP() bool {
	return r == serviceRoleAPIGateway || r == serviceRoleIdentityAuth || r == serviceRoleCardVault || r == serviceRoleEBSAdapter || r == serviceRolePSPWebhook || r == serviceRoleAdminReporting || r == serviceRoleNotification || r == serviceRoleBeneficiary || r == serviceRoleWalletAPI
}

func (r serviceRole) startsGRPC() bool {
	return r == serviceRoleWalletLedger
}

func (r serviceRole) startsWalletWorker() bool {
	return r == serviceRoleWalletWorker
}

func (r serviceRole) startsChat() bool {
	return r == serviceRoleNotification
}

func (r serviceRole) opensDatabase() bool {
	_, ok := r.databaseOwnerRole()
	return ok
}

func (r serviceRole) requiresTemporal() bool {
	return r == serviceRolePSPWebhook || r == serviceRoleWalletLedger || r == serviceRoleWalletWorker
}

func validateRoleDatabaseConfig(role serviceRole, dbURL, dbPath, driver string) error {
	if !role.opensDatabase() {
		if strings.TrimSpace(dbURL) != "" {
			return fmt.Errorf("%w: %s must not set noebs.db_url", errDatabaseNotAllowed, role)
		}
		if strings.TrimSpace(dbPath) != "" {
			return fmt.Errorf("%w: %s must not set noebs.db_path", errDatabaseNotAllowed, role)
		}
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "":
		return fmt.Errorf("%w: %s requires noebs.db_driver", store.ErrMissingDatabaseDriver, role)
	case "sqlite", store.DriverSQLite:
		if strings.TrimSpace(dbPath) == "" {
			return fmt.Errorf("%w: %s requires noebs.db_path for sqlite", errMissingDatabasePath, role)
		}
		return nil
	case "postgres", store.DriverPostgres:
		if strings.TrimSpace(dbURL) == "" {
			return fmt.Errorf("%w: %s requires noebs.db_url", errMissingDatabaseURL, role)
		}
		return nil
	default:
		return fmt.Errorf("%w: %s noebs.db_driver %q", store.ErrUnsupportedDatabaseDriver, role, driver)
	}
}

func (r serviceRole) runsMigrations() bool {
	_, ok := r.migrationScope()
	return ok
}

func (r serviceRole) databaseOwnerRole() (serviceRole, bool) {
	switch r {
	case serviceRoleIdentityAuth, serviceRoleIdentityAuthMigrate:
		return serviceRoleIdentityAuth, true
	case serviceRoleCardVault, serviceRoleCardVaultMigrate:
		return serviceRoleCardVault, true
	case serviceRoleEBSAdapter, serviceRoleEBSAdapterMigrate:
		return serviceRoleEBSAdapter, true
	case serviceRolePSPWebhook, serviceRolePSPWebhookMigrate:
		return serviceRolePSPWebhook, true
	case serviceRoleAdminReporting, serviceRoleAdminReportingMigrate:
		return serviceRoleAdminReporting, true
	case serviceRoleNotification, serviceRoleNotificationMigrate:
		return serviceRoleNotification, true
	case serviceRoleBeneficiary, serviceRoleBeneficiaryMigrate:
		return serviceRoleBeneficiary, true
	case serviceRoleWalletLedger, serviceRoleWalletLedgerMigrate, serviceRoleWalletWorker:
		return serviceRoleWalletLedger, true
	default:
		return "", false
	}
}

func (r serviceRole) migrationScope() (string, bool) {
	switch r {
	case serviceRoleIdentityAuthMigrate:
		return store.MigrationScopeIdentityAuth, true
	case serviceRoleCardVaultMigrate:
		return store.MigrationScopeCardVault, true
	case serviceRoleEBSAdapterMigrate:
		return store.MigrationScopeEBSAdapter, true
	case serviceRolePSPWebhookMigrate:
		return store.MigrationScopePSPWebhook, true
	case serviceRoleAdminReportingMigrate:
		return store.MigrationScopeAdminReporting, true
	case serviceRoleNotificationMigrate:
		return store.MigrationScopeNotificationChat, true
	case serviceRoleBeneficiaryMigrate:
		return store.MigrationScopeConsumerBeneficiary, true
	case serviceRoleWalletLedgerMigrate:
		return store.MigrationScopeWalletLedger, true
	default:
		return "", false
	}
}
