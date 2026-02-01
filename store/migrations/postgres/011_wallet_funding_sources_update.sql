-- +goose Up
ALTER TABLE funding_sources
  ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

ALTER TABLE ownership_verifications
  ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- +goose Down
ALTER TABLE ownership_verifications
  DROP COLUMN IF EXISTS updated_at;

ALTER TABLE funding_sources
  DROP COLUMN IF EXISTS updated_at;
