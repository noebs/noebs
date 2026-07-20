-- +goose Up
ALTER TABLE psp_configs
  DROP CONSTRAINT IF EXISTS psp_configs_webhook_auth_mode_check,
  ADD CONSTRAINT psp_configs_webhook_auth_mode_check
    CHECK (webhook_auth_mode IN ('signature', 'ip_allowlist'));

ALTER TABLE psp_config_overrides
  DROP CONSTRAINT IF EXISTS psp_config_overrides_webhook_auth_mode_check,
  ADD CONSTRAINT psp_config_overrides_webhook_auth_mode_check
    CHECK (webhook_auth_mode IS NULL OR webhook_auth_mode IN ('signature', 'ip_allowlist'));

-- +goose Down
ALTER TABLE psp_config_overrides
  DROP CONSTRAINT IF EXISTS psp_config_overrides_webhook_auth_mode_check,
  ADD CONSTRAINT psp_config_overrides_webhook_auth_mode_check
    CHECK (webhook_auth_mode IS NULL OR webhook_auth_mode IN ('signature', 'ip_allowlist'));

ALTER TABLE psp_configs
  DROP CONSTRAINT IF EXISTS psp_configs_webhook_auth_mode_check,
  ADD CONSTRAINT psp_configs_webhook_auth_mode_check
    CHECK (webhook_auth_mode IN ('signature', 'ip_allowlist'));
