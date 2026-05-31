-- +goose Up
CREATE UNIQUE INDEX IF NOT EXISTS idx_ownership_verifications_workflow_replay
  ON ownership_verifications(tenant_id, destination_id, workflow_id)
  WHERE workflow_id IS NOT NULL AND btrim(workflow_id) <> '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_ownership_verifications_reference_replay
  ON ownership_verifications(tenant_id, destination_id, reference_id)
  WHERE reference_id IS NOT NULL AND btrim(reference_id) <> '';

-- +goose Down
DROP INDEX IF EXISTS idx_ownership_verifications_reference_replay;
DROP INDEX IF EXISTS idx_ownership_verifications_workflow_replay;
