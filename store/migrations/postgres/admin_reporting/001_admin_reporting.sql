-- +goose Up
CREATE TABLE tenants (
  id TEXT PRIMARY KEY CONSTRAINT tenant_id_canonical CHECK (
    id <> 'default' AND length(id) <= 63 AND id ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$'
  ),
  name TEXT NOT NULL CHECK (name <> '' AND name = btrim(name)),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE transactions (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
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
  updated_at TIMESTAMPTZ NOT NULL,
  FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

CREATE INDEX idx_admin_transactions_terminal_id ON transactions(tenant_id, terminal_id);
CREATE INDEX idx_admin_transactions_uuid ON transactions(tenant_id, uuid);

CREATE UNIQUE INDEX idx_admin_transactions_tenant_uuid_unique
  ON transactions(tenant_id, uuid)
  WHERE uuid IS NOT NULL AND btrim(uuid) <> '';

CREATE TABLE merchant_issues (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  terminal_id TEXT NOT NULL,
  latitude NUMERIC,
  longitude NUMERIC,
  reported_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

CREATE INDEX idx_admin_merchant_issues_terminal ON merchant_issues(tenant_id, terminal_id);

-- +goose Down
DROP TABLE merchant_issues;
DROP TABLE transactions;
DROP TABLE tenants;
