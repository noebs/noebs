-- +goose Up
DROP TABLE IF EXISTS beneficiaries;

-- +goose Down
CREATE TABLE beneficiaries (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  user_id BIGINT NOT NULL,
  data TEXT NOT NULL,
  bill_type TEXT NOT NULL,
  name TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_beneficiary_user ON beneficiaries(tenant_id, user_id);
CREATE UNIQUE INDEX idx_beneficiaries_identity_unique ON beneficiaries(tenant_id, user_id, data);
