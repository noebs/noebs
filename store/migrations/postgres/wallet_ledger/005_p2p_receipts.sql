-- +goose Up
-- A terminal no-debit fence; immutable command and ledger remain authoritative.
CREATE TABLE p2p_command_failures (
 tenant_id TEXT NOT NULL,
 idempotency_key TEXT NOT NULL,
 error_code TEXT NOT NULL CHECK(error_code IN ('payment_not_completed','p2p_fee_changed','currency_mismatch')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,idempotency_key),
 FOREIGN KEY(tenant_id,idempotency_key) REFERENCES p2p_commands(tenant_id,idempotency_key)
);
-- +goose Down
DROP TABLE p2p_command_failures;
