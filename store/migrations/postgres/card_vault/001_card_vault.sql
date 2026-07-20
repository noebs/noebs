-- +goose Up
CREATE TABLE tenants (
  id TEXT PRIMARY KEY CONSTRAINT tenant_id_canonical CHECK (
    id <> 'default' AND length(id) <= 63 AND id ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$'
  ),
  name TEXT NOT NULL CHECK (name <> '' AND name = btrim(name)),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE cards (
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
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  UNIQUE (tenant_id, card_id),
  CHECK ((status = 'retired') = (retired_at IS NOT NULL))
);

CREATE UNIQUE INDEX idx_card_vault_cards_active_fingerprint
  ON cards(tenant_id, pan_fingerprint)
  WHERE status = 'active';

CREATE UNIQUE INDEX idx_card_vault_cards_active_main
  ON cards(tenant_id, user_id)
  WHERE status = 'active' AND is_main = TRUE;

CREATE INDEX idx_card_vault_cards_active_user
  ON cards(tenant_id, user_id, is_main DESC, created_at, card_id)
  WHERE status = 'active';

CREATE TABLE card_enrollment_intents (
  enrollment_id UUID NOT NULL,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  user_id BIGINT NOT NULL CHECK (user_id > 0),
  rail_uuid UUID NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'completed', 'failed', 'expired')),
  operation_kind TEXT,
  request_claim TEXT,
  request_fingerprint TEXT,
  request_expiry TEXT,
  request_name TEXT,
  rail_submitted_at TIMESTAMPTZ,
  completed_card_id UUID,
  failure_code TEXT,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, enrollment_id),
  UNIQUE (tenant_id, rail_uuid),
  CHECK (
    status NOT IN ('processing', 'completed')
    OR (
      operation_kind IS NOT NULL
      AND request_claim IS NOT NULL
      AND request_fingerprint IS NOT NULL
      AND request_expiry IS NOT NULL
      AND request_name IS NOT NULL
    )
  ),
  CHECK ((status = 'completed') = (completed_card_id IS NOT NULL)),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, completed_card_id)
    REFERENCES cards(tenant_id, card_id)
);

CREATE UNIQUE INDEX idx_card_vault_one_open_enrollment_intent
  ON card_enrollment_intents(tenant_id, user_id)
  WHERE status IN ('pending', 'processing');

CREATE INDEX idx_card_vault_enrollment_expiry
  ON card_enrollment_intents(tenant_id, expires_at)
  WHERE status IN ('pending', 'processing');

CREATE TABLE card_funded_operation_claims (
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  rail_uuid UUID NOT NULL,
  user_id BIGINT NOT NULL CHECK (user_id > 0),
  card_id UUID NOT NULL,
  purpose TEXT NOT NULL CHECK (purpose ~ '^[a-z][a-z0-9_]{0,63}$'),
  body_claim TEXT NOT NULL CHECK (body_claim ~ '^v1:[0-9a-f]{64}$'),
  rail_tran_date_time TEXT NOT NULL CHECK (rail_tran_date_time ~ '^[0-9]{12}$'),
  claimed_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, rail_uuid),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, card_id)
    REFERENCES cards(tenant_id, card_id)
);

CREATE INDEX idx_card_vault_funded_operation_owner
  ON card_funded_operation_claims(tenant_id, user_id, claimed_at DESC);

-- +goose Down
DROP TABLE card_funded_operation_claims;
DROP TABLE card_enrollment_intents;
DROP TABLE cards;
DROP TABLE tenants;
