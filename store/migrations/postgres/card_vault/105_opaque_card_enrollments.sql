-- +goose Up
-- The legacy cards table mixes public identity, routing secrets, mobile
-- selectors, and reversible IPIN storage. Quarantine its explicit user
-- relationships, scrub unsafe plaintext/secret columns, and create a clean
-- runtime table rather than dual-writing two representations.
ALTER TABLE cards RENAME TO legacy_card_quarantine;
ALTER TABLE legacy_card_quarantine RENAME CONSTRAINT cards_pkey TO legacy_card_quarantine_pkey;
ALTER TABLE legacy_card_quarantine
  RENAME CONSTRAINT cards_tenant_id_user_id_pan_key TO legacy_card_quarantine_tenant_user_pan_key;
ALTER SEQUENCE cards_id_seq RENAME TO legacy_card_quarantine_id_seq;

DROP INDEX IF EXISTS idx_card_vault_cards_user;
DROP INDEX IF EXISTS idx_card_vault_cards_pan;
DROP INDEX IF EXISTS idx_card_vault_cards_mobile;

ALTER TABLE legacy_card_quarantine
  ADD COLUMN card_id UUID NOT NULL DEFAULT gen_random_uuid(),
  ADD COLUMN status TEXT NOT NULL DEFAULT 'legacy_unverified',
  ADD COLUMN retired_at TIMESTAMPTZ;

UPDATE legacy_card_quarantine
SET status = CASE
      WHEN deleted_at IS NULL THEN 'legacy_unverified'
      ELSE 'retired'
    END,
    retired_at = CASE
      WHEN deleted_at IS NULL THEN NULL
      ELSE COALESCE(deleted_at, updated_at)
    END,
    is_main = FALSE;

ALTER TABLE legacy_card_quarantine
  DROP CONSTRAINT legacy_card_quarantine_tenant_user_pan_key,
  DROP COLUMN pan,
  DROP COLUMN pan_enc,
  DROP COLUMN ipin,
  DROP COLUMN ipin_enc,
  DROP COLUMN mobile,
  DROP COLUMN is_valid,
  DROP COLUMN expiry,
  DROP COLUMN is_main,
  ADD CONSTRAINT legacy_card_quarantine_status_valid
    CHECK (status IN ('legacy_unverified', 'retired', 'blocked')),
  ADD CONSTRAINT legacy_card_quarantine_retired_at_consistent
    CHECK ((status = 'retired') = (retired_at IS NOT NULL));

ALTER TABLE legacy_card_quarantine
  ALTER COLUMN card_id DROP DEFAULT;

CREATE UNIQUE INDEX idx_legacy_card_quarantine_public_id
  ON legacy_card_quarantine(tenant_id, card_id);
CREATE INDEX idx_legacy_card_quarantine_user
  ON legacy_card_quarantine(tenant_id, user_id, created_at);

DROP TABLE cache_cards;

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
  FOREIGN KEY (tenant_id, completed_card_id)
    REFERENCES cards(tenant_id, card_id)
);

CREATE UNIQUE INDEX idx_card_vault_one_open_enrollment_intent
  ON card_enrollment_intents(tenant_id, user_id)
  WHERE status IN ('pending', 'processing');

CREATE INDEX idx_card_vault_enrollment_expiry
  ON card_enrollment_intents(tenant_id, expires_at)
  WHERE status IN ('pending', 'processing');

-- +goose Down
-- Forward-only security cutover. Restoring scrubbed PAN/IPIN/cache data is
-- intentionally unsupported. Failing explicitly also prevents goose from
-- recording a lower schema version while the destructive schema remains.
-- +goose StatementBegin
DO $$
BEGIN
  RAISE EXCEPTION 'card vault migration 105 is irreversible';
END
$$;
-- +goose StatementEnd
