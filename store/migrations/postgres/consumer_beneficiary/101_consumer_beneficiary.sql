-- +goose Up
CREATE TABLE IF NOT EXISTS tenants (
  id TEXT PRIMARY KEY CHECK (lower(btrim(id)) <> 'default'),
  name TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS beneficiaries (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  user_id BIGINT NOT NULL,
  data TEXT NOT NULL,
  bill_type TEXT NOT NULL,
  name TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_beneficiary_user ON beneficiaries(tenant_id, user_id);

-- +goose Down
