-- +goose Up
CREATE TABLE tenants (
  id TEXT PRIMARY KEY CONSTRAINT tenant_id_canonical CHECK (
    id <> 'default' AND length(id) <= 63 AND id ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$'
  ),
  name TEXT NOT NULL CHECK (name <> '' AND name = btrim(name)),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE push_data (
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
  deleted_at TIMESTAMPTZ,
  FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

CREATE INDEX idx_notification_push_user ON push_data(tenant_id, user_mobile);
CREATE INDEX idx_notification_push_phone ON push_data(tenant_id, phone);

CREATE TABLE chats (
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  id TEXT NOT NULL,
  from_user_id BIGINT NOT NULL CHECK (from_user_id > 0),
  to_user_id BIGINT NOT NULL CHECK (to_user_id > 0),
  text TEXT NOT NULL,
  is_delivered BOOLEAN NOT NULL DEFAULT FALSE,
  date BIGINT NOT NULL,
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);
CREATE INDEX idx_chats_unread ON chats(tenant_id, to_user_id, is_delivered, date, id);

CREATE TABLE contacts (
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  owner_user_id BIGINT NOT NULL CHECK (owner_user_id > 0),
  contact_user_id BIGINT NOT NULL CHECK (contact_user_id > 0),
  PRIMARY KEY (tenant_id, owner_user_id, contact_user_id),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);
CREATE INDEX idx_contacts_reverse ON contacts(tenant_id, contact_user_id, owner_user_id);

-- +goose Down
DROP TABLE contacts;
DROP TABLE chats;
DROP TABLE push_data;
DROP TABLE tenants;
