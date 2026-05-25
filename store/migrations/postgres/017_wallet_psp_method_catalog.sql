-- +goose Up
ALTER TABLE psp_configs
  ADD COLUMN IF NOT EXISTS method_type TEXT NOT NULL DEFAULT 'redirect'
    CHECK (method_type IN ('redirect', 'blocking', 'qr', 'offline', 'bank_transfer', 'card', 'crypto', 'mobile_money')),
  ADD COLUMN IF NOT EXISTS display_name TEXT,
  ADD COLUMN IF NOT EXISTS supported_regions TEXT[],
  ADD COLUMN IF NOT EXISTS min_amount BIGINT,
  ADD COLUMN IF NOT EXISTS max_amount BIGINT,
  ADD COLUMN IF NOT EXISTS deposit_input_schema JSONB,
  ADD COLUMN IF NOT EXISTS withdrawal_input_schema JSONB,
  ADD COLUMN IF NOT EXISTS presentation_schema JSONB;

ALTER TABLE psp_config_overrides
  ADD COLUMN IF NOT EXISTS method_type TEXT
    CHECK (method_type IS NULL OR method_type IN ('redirect', 'blocking', 'qr', 'offline', 'bank_transfer', 'card', 'crypto', 'mobile_money')),
  ADD COLUMN IF NOT EXISTS display_name TEXT,
  ADD COLUMN IF NOT EXISTS supported_regions TEXT[],
  ADD COLUMN IF NOT EXISTS min_amount BIGINT,
  ADD COLUMN IF NOT EXISTS max_amount BIGINT,
  ADD COLUMN IF NOT EXISTS deposit_input_schema JSONB,
  ADD COLUMN IF NOT EXISTS withdrawal_input_schema JSONB,
  ADD COLUMN IF NOT EXISTS presentation_schema JSONB;

-- +goose Down
ALTER TABLE psp_config_overrides
  DROP COLUMN IF EXISTS presentation_schema,
  DROP COLUMN IF EXISTS withdrawal_input_schema,
  DROP COLUMN IF EXISTS deposit_input_schema,
  DROP COLUMN IF EXISTS max_amount,
  DROP COLUMN IF EXISTS min_amount,
  DROP COLUMN IF EXISTS supported_regions,
  DROP COLUMN IF EXISTS display_name,
  DROP COLUMN IF EXISTS method_type;

ALTER TABLE psp_configs
  DROP COLUMN IF EXISTS presentation_schema,
  DROP COLUMN IF EXISTS withdrawal_input_schema,
  DROP COLUMN IF EXISTS deposit_input_schema,
  DROP COLUMN IF EXISTS max_amount,
  DROP COLUMN IF EXISTS min_amount,
  DROP COLUMN IF EXISTS supported_regions,
  DROP COLUMN IF EXISTS display_name,
  DROP COLUMN IF EXISTS method_type;
