-- +goose Up
ALTER TABLE psp_configs
  ADD COLUMN IF NOT EXISTS response_default_currency TEXT,
  ADD COLUMN IF NOT EXISTS deposit_response_mapping JSONB,
  ADD COLUMN IF NOT EXISTS payout_response_mapping JSONB,
  ADD COLUMN IF NOT EXISTS status_response_mapping JSONB,
  ADD COLUMN IF NOT EXISTS webhook_response_mapping JSONB;

ALTER TABLE psp_config_overrides
  ADD COLUMN IF NOT EXISTS response_default_currency TEXT,
  ADD COLUMN IF NOT EXISTS deposit_response_mapping JSONB,
  ADD COLUMN IF NOT EXISTS payout_response_mapping JSONB,
  ADD COLUMN IF NOT EXISTS status_response_mapping JSONB,
  ADD COLUMN IF NOT EXISTS webhook_response_mapping JSONB;

-- +goose Down
ALTER TABLE psp_config_overrides
  DROP COLUMN IF EXISTS webhook_response_mapping,
  DROP COLUMN IF EXISTS status_response_mapping,
  DROP COLUMN IF EXISTS payout_response_mapping,
  DROP COLUMN IF EXISTS deposit_response_mapping,
  DROP COLUMN IF EXISTS response_default_currency;

ALTER TABLE psp_configs
  DROP COLUMN IF EXISTS webhook_response_mapping,
  DROP COLUMN IF EXISTS status_response_mapping,
  DROP COLUMN IF EXISTS payout_response_mapping,
  DROP COLUMN IF EXISTS deposit_response_mapping,
  DROP COLUMN IF EXISTS response_default_currency;
