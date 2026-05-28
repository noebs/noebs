-- +goose Up
CREATE TABLE IF NOT EXISTS transaction_events (
  id BIGSERIAL PRIMARY KEY,
  transaction_id BIGINT NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  topic TEXT NOT NULL,
  event_key TEXT NOT NULL,
  event_type TEXT NOT NULL,
  payload JSONB NOT NULL,
  publish_attempts INTEGER NOT NULL DEFAULT 0,
  last_error TEXT,
  published_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ebs_transaction_events_pending
  ON transaction_events(id)
  WHERE published_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_ebs_transaction_events_topic_key
  ON transaction_events(topic, event_key);

-- +goose Down
DROP INDEX IF EXISTS idx_ebs_transaction_events_topic_key;
DROP INDEX IF EXISTS idx_ebs_transaction_events_pending;
DROP TABLE IF EXISTS transaction_events;
