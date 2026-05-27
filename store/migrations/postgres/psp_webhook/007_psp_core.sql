-- +goose Up
CREATE TABLE IF NOT EXISTS tenants (
  id TEXT PRIMARY KEY CHECK (lower(btrim(id)) <> 'default'),
  name TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS psp_configs (
  id SERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  provider_code TEXT NOT NULL,
  provider_name TEXT NOT NULL,
  api_base_url TEXT NOT NULL,
  enabled_currencies TEXT[],
  idempotency_header_name TEXT,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  supports_deposit BOOLEAN NOT NULL DEFAULT TRUE,
  supports_withdrawal BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(tenant_id, provider_code)
);

CREATE TABLE IF NOT EXISTS psp_transactions (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  psp_provider TEXT NOT NULL,
  psp_transaction_id TEXT,
  idempotency_key TEXT NOT NULL,
  client_reference TEXT NOT NULL,
  direction TEXT NOT NULL CHECK (direction IN ('inbound', 'outbound')),
  amount BIGINT NOT NULL,
  fee_amount BIGINT,
  net_amount BIGINT,
  currency TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('initiated','processing','pending','held','failed','cancelled','success')),
  workflow_id TEXT,
  response_code TEXT,
  response_message TEXT,
  raw_request JSONB,
  raw_response JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  confirmed_at TIMESTAMPTZ,
  last_polled_at TIMESTAMPTZ,
  next_poll_at TIMESTAMPTZ,
  reconciled_at TIMESTAMPTZ,
  retry_count INT NOT NULL DEFAULT 0,
  lock_token TEXT,
  lock_expires_at TIMESTAMPTZ,
  last_error_type TEXT,
  last_error_at TIMESTAMPTZ,
  UNIQUE(tenant_id, psp_provider, psp_transaction_id),
  UNIQUE(tenant_id, psp_provider, idempotency_key),
  UNIQUE(tenant_id, client_reference)
);

CREATE INDEX IF NOT EXISTS idx_psp_tx_status ON psp_transactions(tenant_id, status, created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_psp_tx_status;

DROP TABLE IF EXISTS psp_transactions;
DROP TABLE IF EXISTS psp_configs;
