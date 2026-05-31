-- +goose Up
CREATE UNIQUE INDEX IF NOT EXISTS idx_funding_sources_null_external_reference_unique
  ON funding_sources(tenant_id, wallet_id, source_type)
  WHERE external_reference IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_funding_sources_null_external_reference_unique;
