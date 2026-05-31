-- +goose Up
DELETE FROM beneficiaries stale
USING beneficiaries keep
WHERE stale.tenant_id = keep.tenant_id
  AND stale.user_id = keep.user_id
  AND stale.data = keep.data
  AND (
    stale.updated_at < keep.updated_at
    OR (stale.updated_at = keep.updated_at AND stale.id < keep.id)
  );

CREATE UNIQUE INDEX IF NOT EXISTS idx_beneficiaries_identity_unique
  ON beneficiaries(tenant_id, user_id, data);

-- +goose Down
DROP INDEX IF EXISTS idx_beneficiaries_identity_unique;
