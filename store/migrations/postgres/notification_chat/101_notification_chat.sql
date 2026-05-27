-- +goose Up
CREATE TABLE IF NOT EXISTS tenants (
  id TEXT PRIMARY KEY CHECK (lower(btrim(id)) <> 'default'),
  name TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS chats (
  id TEXT PRIMARY KEY,
  "from" TEXT NOT NULL,
  "to" TEXT NOT NULL,
  text TEXT NOT NULL,
  is_delivered INTEGER NOT NULL DEFAULT 0,
  date BIGINT
);
CREATE INDEX IF NOT EXISTS idx_notification_chats_to_delivered_date ON chats("to", is_delivered, date);

CREATE TABLE IF NOT EXISTS contacts (
  first TEXT NOT NULL,
  second TEXT NOT NULL,
  "both" TEXT NOT NULL,
  PRIMARY KEY ("both")
);
CREATE INDEX IF NOT EXISTS idx_notification_contacts_second ON contacts(second);

CREATE TABLE IF NOT EXISTS push_data (
  uuid TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  type TEXT,
  date BIGINT,
  to_device TEXT,
  title TEXT,
  body TEXT,
  call_to_action TEXT,
  phone TEXT,
  is_read BOOLEAN NOT NULL DEFAULT FALSE,
  device_id TEXT,
  user_mobile TEXT,
  ebs_uuid TEXT,
  payment_request JSONB,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_notification_push_user ON push_data(tenant_id, user_mobile);
CREATE INDEX IF NOT EXISTS idx_notification_push_phone ON push_data(tenant_id, phone);

-- +goose Down
