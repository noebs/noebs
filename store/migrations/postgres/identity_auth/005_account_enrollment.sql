-- +goose Up
-- Global identity enrollment precedes tenant admission. Domain profiles and
-- wallets keep their existing tenant-scoped identities and constraints.
CREATE TABLE account_enrollments (
  issuer TEXT NOT NULL CHECK (issuer <> ''),
  subject TEXT NOT NULL CHECK (subject <> ''),
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  progress JSONB NOT NULL CHECK (
    jsonb_typeof(progress) = 'object' AND progress ? 'complete'
    AND jsonb_typeof(progress->'complete') = 'boolean'
    AND progress - 'complete' = '{}'::jsonb
  ),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (issuer, subject, tenant_id)
);

-- +goose Down
-- Removing the receipt would allow a completed account to enroll again after
-- membership revocation. Keep the receipt when rolling back application code.
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'Account enrollment receipts must be retained; use a forward migration';
END $$;
-- +goose StatementEnd
