-- +goose Up
ALTER TABLE psp_configs
  ADD COLUMN IF NOT EXISTS deposit_request_method TEXT NOT NULL DEFAULT 'POST',
  ADD COLUMN IF NOT EXISTS deposit_request_path TEXT NOT NULL DEFAULT '/deposits/verify',
  ADD COLUMN IF NOT EXISTS deposit_request_mapping JSONB,
  ADD COLUMN IF NOT EXISTS payout_request_method TEXT NOT NULL DEFAULT 'POST',
  ADD COLUMN IF NOT EXISTS payout_request_path TEXT NOT NULL DEFAULT '/payouts',
  ADD COLUMN IF NOT EXISTS payout_request_mapping JSONB,
  ADD COLUMN IF NOT EXISTS status_request_method TEXT NOT NULL DEFAULT 'GET',
  ADD COLUMN IF NOT EXISTS status_request_path TEXT NOT NULL DEFAULT '/transactions/{transaction_id}',
  ADD COLUMN IF NOT EXISTS status_request_mapping JSONB;

ALTER TABLE psp_config_overrides
  ADD COLUMN IF NOT EXISTS deposit_request_method TEXT,
  ADD COLUMN IF NOT EXISTS deposit_request_path TEXT,
  ADD COLUMN IF NOT EXISTS deposit_request_mapping JSONB,
  ADD COLUMN IF NOT EXISTS payout_request_method TEXT,
  ADD COLUMN IF NOT EXISTS payout_request_path TEXT,
  ADD COLUMN IF NOT EXISTS payout_request_mapping JSONB,
  ADD COLUMN IF NOT EXISTS status_request_method TEXT,
  ADD COLUMN IF NOT EXISTS status_request_path TEXT,
  ADD COLUMN IF NOT EXISTS status_request_mapping JSONB;

-- +goose Down
ALTER TABLE psp_config_overrides
  DROP COLUMN IF EXISTS status_request_mapping,
  DROP COLUMN IF EXISTS status_request_path,
  DROP COLUMN IF EXISTS status_request_method,
  DROP COLUMN IF EXISTS payout_request_mapping,
  DROP COLUMN IF EXISTS payout_request_path,
  DROP COLUMN IF EXISTS payout_request_method,
  DROP COLUMN IF EXISTS deposit_request_mapping,
  DROP COLUMN IF EXISTS deposit_request_path,
  DROP COLUMN IF EXISTS deposit_request_method;

ALTER TABLE psp_configs
  DROP COLUMN IF EXISTS status_request_mapping,
  DROP COLUMN IF EXISTS status_request_path,
  DROP COLUMN IF EXISTS status_request_method,
  DROP COLUMN IF EXISTS payout_request_mapping,
  DROP COLUMN IF EXISTS payout_request_path,
  DROP COLUMN IF EXISTS payout_request_method,
  DROP COLUMN IF EXISTS deposit_request_mapping,
  DROP COLUMN IF EXISTS deposit_request_path,
  DROP COLUMN IF EXISTS deposit_request_method;
