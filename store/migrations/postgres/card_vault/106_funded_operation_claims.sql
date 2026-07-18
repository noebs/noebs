-- +goose Up
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
  FOREIGN KEY (tenant_id, card_id)
    REFERENCES cards(tenant_id, card_id)
);

CREATE INDEX idx_card_vault_funded_operation_owner
  ON card_funded_operation_claims(tenant_id, user_id, claimed_at DESC);

-- +goose Down
DROP TABLE card_funded_operation_claims;
