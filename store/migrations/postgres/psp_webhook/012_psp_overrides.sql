-- +goose Up
CREATE TABLE IF NOT EXISTS psp_config_overrides (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  provider_code TEXT NOT NULL,
  region TEXT,
  currency TEXT,
  direction TEXT,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  supports_deposit BOOLEAN NOT NULL DEFAULT TRUE,
  supports_withdrawal BOOLEAN NOT NULL DEFAULT TRUE,
  enabled_currencies TEXT[],
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_psp_overrides_scope ON psp_config_overrides(tenant_id, provider_code, region, currency, direction);
CREATE INDEX IF NOT EXISTS idx_psp_overrides_active ON psp_config_overrides(tenant_id, provider_code, is_active);

-- +goose Down
DROP INDEX IF EXISTS idx_psp_overrides_active;
DROP INDEX IF EXISTS idx_psp_overrides_scope;
DROP TABLE IF EXISTS psp_config_overrides;
