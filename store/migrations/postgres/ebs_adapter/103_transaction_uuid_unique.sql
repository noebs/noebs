-- +goose Up
DELETE FROM transactions older
USING transactions newer
WHERE older.tenant_id = newer.tenant_id
  AND older.uuid = newer.uuid
  AND older.uuid IS NOT NULL
  AND btrim(older.uuid) <> ''
  AND older.id < newer.id;

CREATE UNIQUE INDEX IF NOT EXISTS idx_ebs_transactions_tenant_uuid_unique
  ON transactions(tenant_id, uuid)
  WHERE uuid IS NOT NULL AND btrim(uuid) <> '';

-- +goose Down
DROP INDEX IF EXISTS idx_ebs_transactions_tenant_uuid_unique;
