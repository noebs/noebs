-- +goose Up
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
DROP TABLE card_enrollment_intents;
