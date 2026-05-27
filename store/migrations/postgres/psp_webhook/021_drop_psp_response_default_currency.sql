-- +goose Up
ALTER TABLE psp_config_overrides
  DROP COLUMN IF EXISTS response_default_currency;

ALTER TABLE psp_configs
  DROP COLUMN IF EXISTS response_default_currency;

-- +goose Down
ALTER TABLE psp_configs
  ADD COLUMN IF NOT EXISTS response_default_currency TEXT;

ALTER TABLE psp_config_overrides
  ADD COLUMN IF NOT EXISTS response_default_currency TEXT;
