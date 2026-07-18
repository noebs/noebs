package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/workloadauth"
)

func TestWorkloadAuthRuntimeConfigAcceptsExactRoleMatrix(t *testing.T) {
	roles := []serviceRole{
		serviceRoleAPIGateway,
		serviceRoleIdentityAuth,
		serviceRoleCardVault,
		serviceRoleEBSAdapter,
		serviceRoleEBSAdapterEvents,
		serviceRolePSPWebhook,
		serviceRoleAdminReporting,
		serviceRoleAdminReportingProjector,
		serviceRoleNotification,
		serviceRoleBeneficiary,
		serviceRoleWalletAPI,
		serviceRoleWalletLedger,
		serviceRoleWalletWorker,
		serviceRoleWorkloadAuthMigrate,
		serviceRoleIdentityAuthMigrate,
		serviceRoleCardVaultMigrate,
		serviceRoleEBSAdapterMigrate,
		serviceRolePSPWebhookMigrate,
		serviceRoleAdminReportingMigrate,
		serviceRoleNotificationMigrate,
		serviceRoleBeneficiaryMigrate,
		serviceRoleWalletLedgerMigrate,
	}
	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			cfg := validWorkloadRuntimeConfig(role)
			if err := validateWorkloadAuthRuntimeConfig(role, cfg); err != nil {
				t.Fatal(err)
			}

			audiences := workloadCallerAudiences(role)
			if len(audiences) == 0 {
				return
			}
			signers, err := workloadauth.NewSignerSet(cfg.WorkloadAuth, audiences)
			if err != nil {
				t.Fatal(err)
			}
			for _, audience := range audiences {
				if !signers.HasAudience(audience) {
					t.Fatalf("missing signer for %s", audience)
				}
			}
			if signers.HasAudience("undeclared-service") {
				t.Fatal("signer set accepted an undeclared audience")
			}
		})
	}
}

func TestWorkloadAuthRuntimeConfigRejectsMissingAndExcessAuthority(t *testing.T) {
	tests := []struct {
		name   string
		role   serviceRole
		mutate func(*ebs_fields.NoebsConfig)
		want   string
	}{
		{
			name: "missing caller key",
			role: serviceRoleAPIGateway,
			mutate: func(cfg *ebs_fields.NoebsConfig) {
				cfg.WorkloadAuth.SigningKeyID = ""
				cfg.WorkloadAuth.SigningPrivateKey = ""
			},
			want: "signing key is required",
		},
		{
			name: "partial caller key",
			role: serviceRoleAPIGateway,
			mutate: func(cfg *ebs_fields.NoebsConfig) {
				cfg.WorkloadAuth.SigningPrivateKey = ""
			},
			want: "invalid workload authentication configuration",
		},
		{
			name: "passive role gets private key",
			role: serviceRoleCardVault,
			mutate: func(cfg *ebs_fields.NoebsConfig) {
				privateKey := testWorkloadPrivateKey(string(serviceRoleCardVault))
				cfg.WorkloadAuth.SigningKeyID = testWorkloadKeyID(string(serviceRoleCardVault))
				cfg.WorkloadAuth.SigningPrivateKey = base64.StdEncoding.EncodeToString(privateKey)
			},
			want: "signing key is not allowed",
		},
		{
			name: "caller-only role gets receiver registry",
			role: serviceRoleAPIGateway,
			mutate: func(cfg *ebs_fields.NoebsConfig) {
				cfg.WorkloadAuth.NonceDatabaseURL = "postgres://nonce"
				cfg.WorkloadAuth.TrustedKeys = workloadTrustedKeys("api-gateway")
			},
			want: "receiver config is not allowed",
		},
		{
			name: "receiver missing nonce database",
			role: serviceRoleCardVault,
			mutate: func(cfg *ebs_fields.NoebsConfig) {
				cfg.WorkloadAuth.NonceDatabaseURL = " "
			},
			want: "nonce_db_url is required",
		},
		{
			name: "receiver missing registry",
			role: serviceRoleCardVault,
			mutate: func(cfg *ebs_fields.NoebsConfig) {
				cfg.WorkloadAuth.TrustedKeys = nil
			},
			want: "trusted_keys is required",
		},
		{
			name: "receiver missing expected caller",
			role: serviceRoleIdentityAuth,
			mutate: func(cfg *ebs_fields.NoebsConfig) {
				delete(cfg.WorkloadAuth.TrustedKeys, testWorkloadKeyID(string(serviceRoleNotification)))
			},
			want: "missing trusted key for notification-chat",
		},
		{
			name: "receiver trusts unexpected caller",
			role: serviceRoleIdentityAuth,
			mutate: func(cfg *ebs_fields.NoebsConfig) {
				for keyID, key := range workloadTrustedKeys("wallet-worker") {
					cfg.WorkloadAuth.TrustedKeys[keyID] = key
				}
			},
			want: `caller "wallet-worker" is not authorized`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validWorkloadRuntimeConfig(test.role)
			test.mutate(&cfg)
			err := validateWorkloadAuthRuntimeConfig(test.role, cfg)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestWorkloadAuthRuntimeConfigAllowsPublicKeyRotation(t *testing.T) {
	cfg := validWorkloadRuntimeConfig(serviceRoleIdentityAuth)
	oldPrivateKey := testWorkloadPrivateKey("api-gateway-old")
	cfg.WorkloadAuth.TrustedKeys["api-gateway-old-key"] = workloadauth.TrustedKeyConfig{
		Caller:    string(serviceRoleAPIGateway),
		PublicKey: base64.StdEncoding.EncodeToString(oldPrivateKey.Public().(ed25519.PublicKey)),
	}
	if err := validateWorkloadAuthRuntimeConfig(serviceRoleIdentityAuth, cfg); err != nil {
		t.Fatal(err)
	}
}

func validWorkloadRuntimeConfig(role serviceRole) ebs_fields.NoebsConfig {
	cfg := ebs_fields.NoebsConfig{}
	if len(workloadCallerAudiences(role)) > 0 {
		privateKey := testWorkloadPrivateKey(string(role))
		cfg.WorkloadAuth.SigningKeyID = testWorkloadKeyID(string(role))
		cfg.WorkloadAuth.SigningPrivateKey = base64.StdEncoding.EncodeToString(privateKey)
	}
	if roleReceivesSignedHTTP(role) {
		cfg.WorkloadAuth.NonceDatabaseURL = "postgres://workload-auth"
		cfg.WorkloadAuth.TrustedKeys = make(map[string]workloadauth.TrustedKeyConfig)
		for caller := range expectedWorkloadCallers(role) {
			for keyID, key := range workloadTrustedKeys(caller) {
				cfg.WorkloadAuth.TrustedKeys[keyID] = key
			}
		}
	}
	return cfg
}

func workloadTrustedKeys(caller string) map[string]workloadauth.TrustedKeyConfig {
	privateKey := testWorkloadPrivateKey(caller)
	return map[string]workloadauth.TrustedKeyConfig{
		testWorkloadKeyID(caller): {
			Caller:    caller,
			PublicKey: base64.StdEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
		},
	}
}
