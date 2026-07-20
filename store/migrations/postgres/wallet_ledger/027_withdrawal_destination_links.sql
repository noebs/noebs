-- +goose Up
CREATE TABLE IF NOT EXISTS ledger_withdrawal_destination_links (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  ledger_entry_id BIGINT NOT NULL REFERENCES ledger_entries(id),
  destination_id BIGINT NOT NULL REFERENCES withdrawal_destinations(id),
  amount BIGINT NOT NULL,
  currency TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(tenant_id, ledger_entry_id, destination_id)
);

CREATE INDEX IF NOT EXISTS idx_withdrawal_destination_links_entry
  ON ledger_withdrawal_destination_links(tenant_id, ledger_entry_id);

CREATE INDEX IF NOT EXISTS idx_withdrawal_destination_links_destination
  ON ledger_withdrawal_destination_links(tenant_id, destination_id);

-- +goose Down
DROP INDEX IF EXISTS idx_withdrawal_destination_links_destination;
DROP INDEX IF EXISTS idx_withdrawal_destination_links_entry;
DROP TABLE IF EXISTS ledger_withdrawal_destination_links;
