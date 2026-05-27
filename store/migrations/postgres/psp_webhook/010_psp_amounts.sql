-- +goose Up
CREATE TABLE IF NOT EXISTS psp_transaction_amounts (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  psp_transaction_id BIGINT NOT NULL REFERENCES psp_transactions(id),
  amount_kind TEXT NOT NULL,
  amount BIGINT NOT NULL,
  currency TEXT NOT NULL,
  fx_rate NUMERIC(18,8),
  fx_base_currency TEXT,
  fx_quote_currency TEXT,
  fx_source TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(tenant_id, psp_transaction_id, amount_kind, currency)
);

CREATE INDEX IF NOT EXISTS idx_psp_amounts_tx ON psp_transaction_amounts(tenant_id, psp_transaction_id, amount_kind);
CREATE INDEX IF NOT EXISTS idx_psp_amounts_currency ON psp_transaction_amounts(tenant_id, currency, amount_kind);

-- +goose Down
DROP INDEX IF EXISTS idx_psp_amounts_currency;
DROP INDEX IF EXISTS idx_psp_amounts_tx;
DROP TABLE IF EXISTS psp_transaction_amounts;
