-- +goose Up
ALTER TABLE cards
  ADD COLUMN IF NOT EXISTS mobile TEXT;

CREATE INDEX IF NOT EXISTS idx_card_vault_cards_mobile ON cards(tenant_id, mobile)
  WHERE deleted_at IS NULL;

-- +goose Down
