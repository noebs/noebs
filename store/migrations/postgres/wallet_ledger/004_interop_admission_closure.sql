-- +goose Up
-- A durable admission fence permits recovery of an app POST whose response was
-- lost. It never cancels a command already admitted to the ledger worker.
CREATE TABLE interop_quote_closures (
 tenant_id TEXT NOT NULL,
 quote_id UUID NOT NULL,
 owner_id TEXT NOT NULL CHECK(owner_id <> ''),
 closed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,quote_id),
 FOREIGN KEY(tenant_id,quote_id) REFERENCES interop_quotes(tenant_id,id)
);
-- +goose Down
DROP TABLE interop_quote_closures;
