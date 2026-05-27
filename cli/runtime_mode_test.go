package main

import (
	"errors"
	"testing"
)

func TestParseServiceRoleRequiresExplicitRole(t *testing.T) {
	_, err := parseServiceRole("")
	if !errors.Is(err, errMissingServiceRole) {
		t.Fatalf("error = %v, want %v", err, errMissingServiceRole)
	}
}

func TestParseServiceRoleRejectsUnknownRole(t *testing.T) {
	_, err := parseServiceRole("api")
	if !errors.Is(err, errInvalidServiceRole) {
		t.Fatalf("error = %v, want %v", err, errInvalidServiceRole)
	}
}

func TestCurrentServiceRoleReadsConfig(t *testing.T) {
	setServiceRoleForTest(t, serviceRolePSPWebhook)
	got, err := currentServiceRole()
	if err != nil {
		t.Fatalf("currentServiceRole() error = %v", err)
	}
	if got != serviceRolePSPWebhook {
		t.Fatalf("role = %q, want %q", got, serviceRolePSPWebhook)
	}
}

func TestParseServiceRoleAcceptsKnownRoles(t *testing.T) {
	roles := []serviceRole{
		serviceRoleAPIGateway,
		serviceRoleIdentityAuth,
		serviceRoleCardVault,
		serviceRolePSPWebhook,
		serviceRoleAdminReporting,
		serviceRoleNotification,
		serviceRoleWalletLedger,
		serviceRoleWalletWorker,
		serviceRoleMigrate,
	}
	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			got, err := parseServiceRole(string(role))
			if err != nil {
				t.Fatalf("parseServiceRole() error = %v", err)
			}
			if got != role {
				t.Fatalf("role = %q, want %q", got, role)
			}
		})
	}
}

func TestServiceRoleProcessOwnership(t *testing.T) {
	if !serviceRoleAPIGateway.startsHTTP() || serviceRoleAPIGateway.startsGRPC() || serviceRoleAPIGateway.startsWalletWorker() || serviceRoleAPIGateway.runsMigrations() {
		t.Fatalf("api-gateway role should own only the HTTP process")
	}
	if !serviceRoleIdentityAuth.startsHTTP() || serviceRoleIdentityAuth.startsGRPC() || serviceRoleIdentityAuth.startsWalletWorker() || serviceRoleIdentityAuth.runsMigrations() {
		t.Fatalf("identity-auth role should own only the HTTP process")
	}
	if !serviceRoleCardVault.startsHTTP() || serviceRoleCardVault.startsGRPC() || serviceRoleCardVault.startsWalletWorker() || serviceRoleCardVault.runsMigrations() {
		t.Fatalf("card-vault role should own only the HTTP process")
	}
	if !serviceRolePSPWebhook.startsHTTP() || serviceRolePSPWebhook.startsGRPC() || serviceRolePSPWebhook.startsWalletWorker() || serviceRolePSPWebhook.runsMigrations() {
		t.Fatalf("psp-webhook role should own only the HTTP process")
	}
	if !serviceRoleAdminReporting.startsHTTP() || serviceRoleAdminReporting.startsGRPC() || serviceRoleAdminReporting.startsWalletWorker() || serviceRoleAdminReporting.runsMigrations() {
		t.Fatalf("admin-reporting role should own only the HTTP process")
	}
	if !serviceRoleNotification.startsHTTP() || !serviceRoleNotification.startsChat() || serviceRoleNotification.startsGRPC() || serviceRoleNotification.startsWalletWorker() || serviceRoleNotification.runsMigrations() {
		t.Fatalf("notification-chat role should own only the HTTP chat process")
	}
	if serviceRoleWalletLedger.startsHTTP() || !serviceRoleWalletLedger.startsGRPC() || serviceRoleWalletLedger.startsWalletWorker() || serviceRoleWalletLedger.runsMigrations() {
		t.Fatalf("wallet-ledger role should own only the gRPC process")
	}
	if serviceRoleWalletWorker.startsHTTP() || serviceRoleWalletWorker.startsGRPC() || !serviceRoleWalletWorker.startsWalletWorker() || serviceRoleWalletWorker.runsMigrations() {
		t.Fatalf("wallet-worker role should own only the Temporal worker process")
	}
	if serviceRoleMigrate.startsHTTP() || serviceRoleMigrate.startsGRPC() || serviceRoleMigrate.startsWalletWorker() || !serviceRoleMigrate.runsMigrations() {
		t.Fatalf("migrate role should only run migrations")
	}
}
