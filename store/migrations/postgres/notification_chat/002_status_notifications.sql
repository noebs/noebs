-- +goose Up
CREATE TABLE status_notifications (
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  user_id BIGINT NOT NULL CHECK (user_id > 0),
  event_id UUID NOT NULL,
  aggregate_type TEXT NOT NULL CHECK (aggregate_type IN ('transaction','verification')),
  aggregate_id TEXT NOT NULL CHECK (aggregate_id <> ''),
  version BIGINT NOT NULL CHECK (version > 0),
  payload JSONB NOT NULL,
  occurred_at TIMESTAMPTZ NOT NULL,
  received_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id,user_id,event_id),
  UNIQUE (tenant_id,user_id,aggregate_type,aggregate_id,version)
);
CREATE INDEX status_notifications_user ON status_notifications(tenant_id,user_id,occurred_at DESC,event_id);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'Status notifications contain durable Kafka receipts; use a forward migration';
END $$;
-- +goose StatementEnd
