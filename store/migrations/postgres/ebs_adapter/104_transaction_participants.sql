-- +goose Up
CREATE UNIQUE INDEX IF NOT EXISTS idx_ebs_transactions_id_tenant_unique
  ON transactions(id, tenant_id);

CREATE TABLE IF NOT EXISTS transaction_participants (
  transaction_id BIGINT NOT NULL,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  user_id BIGINT NOT NULL CHECK (user_id > 0),
  role TEXT NOT NULL CHECK (role IN ('actor', 'recipient')),
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (transaction_id, user_id, role),
  FOREIGN KEY (transaction_id, tenant_id)
    REFERENCES transactions(id, tenant_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_ebs_transaction_participants_user
  ON transaction_participants(tenant_id, user_id, transaction_id);

-- Existing transactions are intentionally not backfilled: PAN masks are display
-- values and cannot establish ownership.

-- +goose Down
DROP INDEX IF EXISTS idx_ebs_transaction_participants_user;
DROP TABLE IF EXISTS transaction_participants;
DROP INDEX IF EXISTS idx_ebs_transactions_id_tenant_unique;
