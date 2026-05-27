-- +goose Up
CREATE TABLE IF NOT EXISTS tenants (
  id TEXT PRIMARY KEY CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(id)) <> 'default'),
  name TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS transactions (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  token_id BIGINT,
  uuid TEXT,
  response_code INTEGER,
  response_message TEXT,
  response_status TEXT,
  tran_date_time TEXT,
  tran_amount NUMERIC,
  tran_fee NUMERIC,
  pan TEXT,
  sender_pan TEXT,
  receiver_pan TEXT,
  terminal_id TEXT,
  system_trace_audit_number INTEGER,
  approval_code TEXT,
  service_id TEXT,
  merchant_id TEXT,
  bill_type TEXT,
  bill_to TEXT,
  bill_info2 TEXT,
  payload JSONB,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_admin_transactions_terminal_id ON transactions(tenant_id, terminal_id);
CREATE INDEX IF NOT EXISTS idx_admin_transactions_uuid ON transactions(tenant_id, uuid);

CREATE TABLE IF NOT EXISTS merchant_issues (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  terminal_id TEXT NOT NULL,
  latitude NUMERIC,
  longitude NUMERIC,
  reported_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_admin_merchant_issues_terminal ON merchant_issues(tenant_id, terminal_id);

-- +goose Down
