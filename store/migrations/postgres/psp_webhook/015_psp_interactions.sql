-- +goose Up
CREATE TABLE IF NOT EXISTS psp_interactions (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  psp_provider TEXT NOT NULL,
  psp_transaction_id TEXT,
  client_reference TEXT,
  direction TEXT CHECK (direction IS NULL OR direction IN ('inbound', 'outbound')),
  interaction_type TEXT NOT NULL,
  method TEXT,
  url TEXT,
  request_headers JSONB,
  request_body JSONB,
  response_headers JSONB,
  response_body JSONB,
  status_code INT,
  error_message TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_psp_interactions_tx ON psp_interactions(tenant_id, psp_provider, psp_transaction_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_psp_interactions_client_ref ON psp_interactions(tenant_id, client_reference, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_psp_interactions_type ON psp_interactions(tenant_id, interaction_type, created_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_psp_interactions_type;
DROP INDEX IF EXISTS idx_psp_interactions_client_ref;
DROP INDEX IF EXISTS idx_psp_interactions_tx;
DROP TABLE IF EXISTS psp_interactions;
