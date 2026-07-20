-- +goose Up
CREATE TABLE tenants (
  id TEXT PRIMARY KEY CONSTRAINT tenant_id_canonical CHECK (
    id <> 'default' AND length(id) <= 63 AND id ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$'
  ),
  name TEXT NOT NULL CHECK (name <> '' AND name = btrim(name)),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE cache_billers (
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  mobile TEXT NOT NULL,
  biller_id TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, mobile),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id)
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
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  UNIQUE (tenant_id, id)
);

CREATE INDEX idx_ebs_transactions_pan ON transactions(tenant_id, pan);
CREATE INDEX idx_ebs_transactions_sender_pan ON transactions(tenant_id, sender_pan);
CREATE INDEX idx_ebs_transactions_receiver_pan ON transactions(tenant_id, receiver_pan);
CREATE INDEX idx_ebs_transactions_uuid ON transactions(tenant_id, uuid);
CREATE INDEX idx_ebs_transactions_terminal_id ON transactions(tenant_id, terminal_id);
CREATE INDEX idx_ebs_transactions_system_trace ON transactions(tenant_id, system_trace_audit_number);

CREATE UNIQUE INDEX idx_ebs_transactions_tenant_uuid_unique
  ON transactions(tenant_id, uuid)
  WHERE uuid IS NOT NULL AND btrim(uuid) <> '';

CREATE TABLE transaction_events (
  id BIGSERIAL PRIMARY KEY,
  transaction_id BIGINT NOT NULL,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  topic TEXT NOT NULL,
  event_key TEXT NOT NULL,
  event_type TEXT NOT NULL,
  payload JSONB NOT NULL,
  publish_attempts INTEGER NOT NULL DEFAULT 0,
  last_error TEXT,
  published_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, transaction_id)
    REFERENCES transactions(tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_ebs_transaction_events_pending
  ON transaction_events(id)
  WHERE published_at IS NULL;
CREATE INDEX idx_ebs_transaction_events_topic_key
  ON transaction_events(topic, event_key);

CREATE TABLE meter_names (
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  nec TEXT NOT NULL,
  name TEXT NOT NULL,
  PRIMARY KEY (tenant_id, nec),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

CREATE TABLE transaction_participants (
  transaction_id BIGINT NOT NULL,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  user_id BIGINT NOT NULL CHECK (user_id > 0),
  role TEXT NOT NULL CHECK (role IN ('actor', 'recipient')),
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (transaction_id, user_id, role),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, transaction_id)
    REFERENCES transactions(tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_ebs_transaction_participants_user
  ON transaction_participants(tenant_id, user_id, transaction_id);

-- +goose Down
DROP TABLE transaction_participants;
DROP TABLE meter_names;
DROP TABLE transaction_events;
DROP TABLE transactions;
DROP TABLE cache_billers;
DROP TABLE tenants;
