-- +goose Up
CREATE TABLE IF NOT EXISTS fee_configs (
  id SERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  transaction_type TEXT NOT NULL,
  currency TEXT NOT NULL,
  tier_min BIGINT NOT NULL DEFAULT 0,
  tier_max BIGINT,
  percentage_fee NUMERIC(8,4) NOT NULL,
  flat_fee BIGINT NOT NULL DEFAULT 0,
  min_fee BIGINT NOT NULL DEFAULT 0,
  max_fee BIGINT,
  fee_account_code TEXT,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_by_operator_id BIGINT NOT NULL REFERENCES operator_identities(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(tenant_id, transaction_type, currency, tier_min)
);

CREATE TABLE IF NOT EXISTS exchange_rates (
  id SERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  base_currency TEXT NOT NULL,
  quote_currency TEXT NOT NULL,
  buy_rate NUMERIC(18,8) NOT NULL,
  sell_rate NUMERIC(18,8) NOT NULL,
  spread NUMERIC(8,4),
  set_by_operator_id BIGINT NOT NULL REFERENCES operator_identities(id),
  effective_from TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  effective_to TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (buy_rate > 0 AND sell_rate > 0),
  UNIQUE(tenant_id, base_currency, quote_currency, effective_from)
);

CREATE TABLE IF NOT EXISTS transaction_limits (
  id SERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  kyc_tier TEXT NOT NULL,
  transaction_type TEXT NOT NULL,
  currency TEXT NOT NULL,
  daily_limit BIGINT NOT NULL,
  monthly_limit BIGINT NOT NULL,
  per_transaction_limit BIGINT NOT NULL,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  UNIQUE(tenant_id, kyc_tier, transaction_type, currency)
);

-- +goose Down
DROP TABLE IF EXISTS transaction_limits;
DROP TABLE IF EXISTS exchange_rates;
DROP TABLE IF EXISTS fee_configs;
