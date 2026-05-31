-- +goose Up
ALTER TABLE balance_holds
  ADD COLUMN IF NOT EXISTS captured_at TIMESTAMPTZ;

ALTER TABLE balance_holds
  DROP CONSTRAINT IF EXISTS balance_holds_status_check;

ALTER TABLE balance_holds
  ADD CONSTRAINT balance_holds_status_check
  CHECK (status IN ('active', 'released', 'expired', 'canceled', 'captured'));

-- +goose Down
UPDATE balance_holds
SET status = 'released',
    released_at = COALESCE(released_at, captured_at)
WHERE status = 'captured';

ALTER TABLE balance_holds
  DROP CONSTRAINT IF EXISTS balance_holds_status_check;

ALTER TABLE balance_holds
  ADD CONSTRAINT balance_holds_status_check
  CHECK (status IN ('active', 'released', 'expired', 'canceled'));

ALTER TABLE balance_holds
  DROP COLUMN IF EXISTS captured_at;
