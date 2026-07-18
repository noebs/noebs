-- +goose Up
ALTER TABLE tokens
  ADD COLUMN IF NOT EXISTS payment_status TEXT NOT NULL DEFAULT 'available'
  CHECK (payment_status IN ('available', 'processing', 'paid', 'failed'));

ALTER TABLE tokens
  ADD COLUMN IF NOT EXISTS rail_uuid TEXT,
  ADD COLUMN IF NOT EXISTS payer_user_id BIGINT CHECK (payer_user_id > 0),
  ADD COLUMN IF NOT EXISTS claimed_amount INTEGER CHECK (claimed_amount > 0),
  ADD COLUMN IF NOT EXISTS processing_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS finalized_at TIMESTAMPTZ;

UPDATE tokens
SET rail_uuid = uuid
WHERE rail_uuid IS NULL;

ALTER TABLE tokens
  ALTER COLUMN rail_uuid SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_card_vault_tokens_rail_uuid
  ON tokens(tenant_id, rail_uuid);

UPDATE tokens
SET payment_status = 'paid', finalized_at = COALESCE(finalized_at, updated_at)
WHERE is_paid = TRUE;

ALTER TABLE tokens
  ADD CONSTRAINT tokens_payment_status_paid_consistency
  CHECK (is_paid = (payment_status = 'paid'));

-- +goose Down
ALTER TABLE tokens
  DROP CONSTRAINT IF EXISTS tokens_payment_status_paid_consistency;

ALTER TABLE tokens
  DROP COLUMN IF EXISTS finalized_at,
  DROP COLUMN IF EXISTS processing_at,
  DROP COLUMN IF EXISTS claimed_amount,
  DROP COLUMN IF EXISTS payer_user_id,
  DROP COLUMN IF EXISTS rail_uuid,
  DROP COLUMN IF EXISTS payment_status;
