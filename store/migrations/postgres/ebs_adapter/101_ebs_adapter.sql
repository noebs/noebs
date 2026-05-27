-- +goose Up
CREATE TABLE IF NOT EXISTS tenants (
  id TEXT PRIMARY KEY CHECK (lower(btrim(id)) <> 'default'),
  name TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS cache_billers (
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  mobile TEXT NOT NULL,
  biller_id TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, mobile)
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
CREATE INDEX IF NOT EXISTS idx_ebs_transactions_pan ON transactions(tenant_id, pan);
CREATE INDEX IF NOT EXISTS idx_ebs_transactions_sender_pan ON transactions(tenant_id, sender_pan);
CREATE INDEX IF NOT EXISTS idx_ebs_transactions_receiver_pan ON transactions(tenant_id, receiver_pan);
CREATE INDEX IF NOT EXISTS idx_ebs_transactions_uuid ON transactions(tenant_id, uuid);
CREATE INDEX IF NOT EXISTS idx_ebs_transactions_terminal_id ON transactions(tenant_id, terminal_id);
CREATE INDEX IF NOT EXISTS idx_ebs_transactions_system_trace ON transactions(tenant_id, system_trace_audit_number);

CREATE TABLE IF NOT EXISTS meter_names (
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  nec TEXT NOT NULL,
  name TEXT NOT NULL,
  PRIMARY KEY (tenant_id, nec)
);

-- +goose Down
