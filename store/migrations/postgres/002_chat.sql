-- +goose Up
CREATE TABLE IF NOT EXISTS chats (
  id TEXT PRIMARY KEY,
  "from" TEXT NOT NULL,
  "to" TEXT NOT NULL,
  text TEXT NOT NULL,
  is_delivered INTEGER NOT NULL DEFAULT 0,
  date BIGINT
);
CREATE INDEX IF NOT EXISTS idx_chats_to_delivered_date ON chats("to", is_delivered, date);

CREATE TABLE IF NOT EXISTS contacts (
  first TEXT NOT NULL,
  second TEXT NOT NULL,
  both TEXT NOT NULL,
  PRIMARY KEY (both)
);
CREATE INDEX IF NOT EXISTS idx_contacts_second ON contacts(second);

-- +goose Down
DROP INDEX IF EXISTS idx_contacts_second;
DROP TABLE IF EXISTS contacts;

DROP INDEX IF EXISTS idx_chats_to_delivered_date;
DROP TABLE IF EXISTS chats;
