package store

import (
	"context"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestPSPWebhookAuthModeConstraintsAreExact(t *testing.T) {
	ctx := context.Background()
	db := newValidationDB(t)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set migration dialect: %v", err)
	}
	goose.SetTableName(migrationScopeTableNames[MigrationScopePSPWebhook])
	goose.SetBaseFS(postgresMigrations)
	if err := goose.UpToContext(ctx, db.DB.DB, migrationScopePaths[MigrationScopePSPWebhook], 21); err != nil {
		t.Fatalf("migrate psp webhook to version 21: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		ALTER TABLE psp_configs
			DROP CONSTRAINT psp_configs_webhook_auth_mode_check,
			ADD CONSTRAINT psp_configs_webhook_auth_mode_check
				CHECK (webhook_auth_mode IN ('signature', 'ip_allowlist', 'signature_or_ip_allowlist'));
		ALTER TABLE psp_config_overrides
			DROP CONSTRAINT psp_config_overrides_webhook_auth_mode_check,
			ADD CONSTRAINT psp_config_overrides_webhook_auth_mode_check
				CHECK (webhook_auth_mode IS NULL OR webhook_auth_mode IN ('signature', 'ip_allowlist', 'signature_or_ip_allowlist'));
	`); err != nil {
		t.Fatalf("install pre-22 webhook auth constraints: %v", err)
	}
	if err := MigrateScope(ctx, db, MigrationScopePSPWebhook); err != nil {
		t.Fatalf("migrate psp webhook: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO tenants(id, name) VALUES ('tenant-webhook-auth', 'Webhook Auth')`); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO psp_configs(
			tenant_id, provider_code, provider_name, api_base_url, enabled_currencies
		) VALUES
			('tenant-webhook-auth', 'signature-provider', 'Signature Provider', 'https://signature.example', ARRAY['SDG']),
			('tenant-webhook-auth', 'ip-provider', 'IP Provider', 'https://ip.example', ARRAY['SDG'])
	`); err != nil {
		t.Fatalf("insert psp configs: %v", err)
	}

	assertWebhookAuthModeAccepted(t, ctx, db, "psp_configs", "signature-provider", "signature")
	assertWebhookAuthModeAccepted(t, ctx, db, "psp_configs", "ip-provider", "ip_allowlist")
	assertWebhookAuthModeRejected(t, ctx, db, "psp_configs", "signature-provider", "signature_or_ip_allowlist")
	assertWebhookAuthModeRejected(t, ctx, db, "psp_configs", "signature-provider", "unknown")

	if _, err := db.ExecContext(ctx, `
		INSERT INTO psp_config_overrides(tenant_id, provider_code, region)
		VALUES ('tenant-webhook-auth', 'signature-provider', 'SD')
	`); err != nil {
		t.Fatalf("insert psp config override: %v", err)
	}
	assertWebhookAuthModeAccepted(t, ctx, db, "psp_config_overrides", "signature-provider", "signature")
	assertWebhookAuthModeAccepted(t, ctx, db, "psp_config_overrides", "signature-provider", "ip_allowlist")
	assertWebhookAuthModeRejected(t, ctx, db, "psp_config_overrides", "signature-provider", "signature_or_ip_allowlist")
	assertWebhookAuthModeRejected(t, ctx, db, "psp_config_overrides", "signature-provider", "unknown")
}

func assertWebhookAuthModeAccepted(t *testing.T, ctx context.Context, db *DB, table, providerCode, mode string) {
	t.Helper()
	query := webhookAuthModeUpdateQuery(t, db, table)
	if _, err := db.ExecContext(ctx, query, mode, "tenant-webhook-auth", providerCode); err != nil {
		t.Fatalf("%s rejected webhook auth mode %q: %v", table, mode, err)
	}
}

func assertWebhookAuthModeRejected(t *testing.T, ctx context.Context, db *DB, table, providerCode, mode string) {
	t.Helper()
	query := webhookAuthModeUpdateQuery(t, db, table)
	if _, err := db.ExecContext(ctx, query, mode, "tenant-webhook-auth", providerCode); err == nil {
		t.Fatalf("%s accepted removed webhook auth mode %q", table, mode)
	}
}

func webhookAuthModeUpdateQuery(t *testing.T, db *DB, table string) string {
	t.Helper()
	switch table {
	case "psp_configs":
		return db.Rebind("UPDATE psp_configs SET webhook_auth_mode = ? WHERE tenant_id = ? AND provider_code = ?")
	case "psp_config_overrides":
		return db.Rebind("UPDATE psp_config_overrides SET webhook_auth_mode = ? WHERE tenant_id = ? AND provider_code = ?")
	default:
		t.Fatalf("unsupported PSP config table %q", table)
		return ""
	}
}
