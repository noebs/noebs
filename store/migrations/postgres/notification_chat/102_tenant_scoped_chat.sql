-- +goose Up
CREATE TABLE chats_v2 (
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  id TEXT NOT NULL,
  from_user_id BIGINT NOT NULL CHECK (from_user_id > 0),
  to_user_id BIGINT NOT NULL CHECK (to_user_id > 0),
  text TEXT NOT NULL,
  is_delivered BOOLEAN NOT NULL DEFAULT FALSE,
  date BIGINT NOT NULL,
  PRIMARY KEY (tenant_id, id)
);
CREATE INDEX idx_chats_v2_unread ON chats_v2(tenant_id, to_user_id, is_delivered, date, id);

CREATE TABLE contacts_v2 (
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  owner_user_id BIGINT NOT NULL CHECK (owner_user_id > 0),
  contact_user_id BIGINT NOT NULL CHECK (contact_user_id > 0),
  PRIMARY KEY (tenant_id, owner_user_id, contact_user_id)
);
CREATE INDEX idx_contacts_v2_reverse ON contacts_v2(tenant_id, contact_user_id, owner_user_id);

-- Legacy mobile-scoped chats and contacts are deliberately not backfilled.
DROP TABLE IF EXISTS chats;
DROP TABLE IF EXISTS contacts;

-- +goose Down
DROP TABLE IF EXISTS contacts_v2;
DROP TABLE IF EXISTS chats_v2;

CREATE TABLE chats (
  id TEXT PRIMARY KEY,
  "from" TEXT NOT NULL,
  "to" TEXT NOT NULL,
  text TEXT NOT NULL,
  is_delivered INTEGER NOT NULL DEFAULT 0,
  date BIGINT
);
CREATE INDEX idx_notification_chats_to_delivered_date ON chats("to", is_delivered, date);

CREATE TABLE contacts (
  first TEXT NOT NULL,
  second TEXT NOT NULL,
  "both" TEXT NOT NULL,
  PRIMARY KEY ("both")
);
CREATE INDEX idx_notification_contacts_second ON contacts(second);
