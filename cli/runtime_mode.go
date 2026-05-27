package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/adonese/noebs/store"
)

type serviceRole string

var (
	errMissingServiceRole = errors.New("missing noebs.service_role")
	errInvalidServiceRole = errors.New("invalid noebs.service_role")
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

func (r serviceRole) startsBackgroundJobs() bool {
	return false
}

func (r serviceRole) runsMigrations() bool {
	_, ok := r.migrationScope()
	return ok
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
