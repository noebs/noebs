-- +goose Up
CREATE TABLE IF NOT EXISTS tenants (
  id TEXT PRIMARY KEY CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(id)) <> 'default'),
  name TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS cards (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  user_id BIGINT NOT NULL,
  pan TEXT NOT NULL,
  pan_enc TEXT,
  expiry TEXT,
  name TEXT,
  ipin TEXT,
  ipin_enc TEXT,
  is_main BOOLEAN NOT NULL DEFAULT FALSE,
  is_valid BOOLEAN,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  deleted_at TIMESTAMPTZ,
  UNIQUE(tenant_id, user_id, pan)
);
CREATE INDEX IF NOT EXISTS idx_card_vault_cards_user ON cards(tenant_id, user_id);
CREATE INDEX IF NOT EXISTS idx_card_vault_cards_pan ON cards(tenant_id, pan);

CREATE TABLE IF NOT EXISTS cache_cards (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  pan TEXT NOT NULL,
  pan_enc TEXT,
  expiry TEXT,
  name TEXT,
  is_valid BOOLEAN,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  UNIQUE(tenant_id, pan)
);

CREATE TABLE IF NOT EXISTS tokens (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  user_id BIGINT NOT NULL,
  amount INTEGER,
  cart_id TEXT,
  uuid TEXT NOT NULL,
  note TEXT,
  to_card TEXT,
  to_card_enc TEXT,
  is_paid BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  UNIQUE(tenant_id, uuid)
);

CREATE TABLE IF NOT EXISTS push_data (
  uuid TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  type TEXT,
  date BIGINT,
  to_device TEXT,
  title TEXT,
  body TEXT,
  call_to_action TEXT,
  phone TEXT,
  is_read BOOLEAN NOT NULL DEFAULT FALSE,
  device_id TEXT,
  user_mobile TEXT,
  ebs_uuid TEXT,
  payment_request JSONB,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  deleted_at TIMESTAMPTZ
);

-- +goose Down
