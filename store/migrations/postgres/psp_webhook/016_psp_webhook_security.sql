-- +goose Up
ALTER TABLE psp_configs
  ADD COLUMN IF NOT EXISTS webhook_auth_mode TEXT NOT NULL DEFAULT 'signature'
    CHECK (webhook_auth_mode IN ('signature', 'ip_allowlist')),
  ADD COLUMN IF NOT EXISTS webhook_allowed_cidrs TEXT[],
  ADD COLUMN IF NOT EXISTS status_check_unauthenticated_webhook BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE psp_config_overrides
  ADD COLUMN IF NOT EXISTS webhook_auth_mode TEXT
    CHECK (webhook_auth_mode IS NULL OR webhook_auth_mode IN ('signature', 'ip_allowlist')),
  ADD COLUMN IF NOT EXISTS webhook_allowed_cidrs TEXT[],
  ADD COLUMN IF NOT EXISTS status_check_unauthenticated_webhook BOOLEAN;

-- +goose Down
ALTER TABLE psp_config_overrides
  DROP COLUMN IF EXISTS status_check_unauthenticated_webhook,
  DROP COLUMN IF EXISTS webhook_allowed_cidrs,
  DROP COLUMN IF EXISTS webhook_auth_mode;

ALTER TABLE psp_configs
  DROP COLUMN IF EXISTS status_check_unauthenticated_webhook,
  DROP COLUMN IF EXISTS webhook_allowed_cidrs,
  DROP COLUMN IF EXISTS webhook_auth_mode;
