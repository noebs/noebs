-- +goose Up
CREATE TABLE IF NOT EXISTS tenants (
  id TEXT PRIMARY KEY CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(id)) <> 'default'),
  name TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS cards (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  card_id UUID NOT NULL,
  user_id BIGINT NOT NULL CHECK (user_id > 0),
  pan_fingerprint TEXT NOT NULL,
  pan_ciphertext TEXT NOT NULL,
  pan_key_version INTEGER NOT NULL CHECK (pan_key_version > 0),
  masked_pan TEXT NOT NULL CHECK (masked_pan ~ '^\*{4}[0-9]{4}$'),
  expiry TEXT NOT NULL CHECK (expiry ~ '^[0-9]{4}$'),
  name TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL CHECK (status IN ('active', 'retired', 'blocked')),
  is_main BOOLEAN NOT NULL DEFAULT FALSE,
  verification_method TEXT NOT NULL,
  verified_at TIMESTAMPTZ NOT NULL,
  retired_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  UNIQUE (tenant_id, card_id),
  CHECK ((status = 'retired') = (retired_at IS NOT NULL))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_card_vault_cards_active_fingerprint
  ON cards(tenant_id, pan_fingerprint)
  WHERE status = 'active';

CREATE UNIQUE INDEX IF NOT EXISTS idx_card_vault_cards_active_main
  ON cards(tenant_id, user_id)
  WHERE status = 'active' AND is_main = TRUE;

CREATE INDEX IF NOT EXISTS idx_card_vault_cards_active_user
  ON cards(tenant_id, user_id, is_main DESC, created_at, card_id)
  WHERE status = 'active';

-- +goose Down
