-- +goose Up
-- Keep the historical synthetic marker; new account intake does not require
-- customers to make a test-data declaration. Review is a NoEBS decision.
ALTER TABLE identity_sessions DROP CONSTRAINT identity_sessions_synthetic_check;
ALTER TABLE identity_sessions DROP CONSTRAINT identity_sessions_status_check;
ALTER TABLE identity_sessions DROP CONSTRAINT identity_sessions_check;
ALTER TABLE identity_sessions ADD COLUMN previous_session_id UUID;
ALTER TABLE identity_sessions ADD COLUMN review JSONB;
ALTER TABLE identity_sessions ADD CONSTRAINT identity_sessions_status_check
  CHECK (status IN ('draft', 'submitted', 'discarded', 'approved', 'needs_information', 'rejected', 'withdrawn'));
ALTER TABLE identity_sessions ADD CONSTRAINT identity_sessions_submission_check
  CHECK ((status IN ('submitted', 'approved', 'needs_information', 'rejected')) =
    (submission IS NOT NULL AND submission_sha256 IS NOT NULL));
ALTER TABLE identity_sessions ADD CONSTRAINT identity_sessions_review_check
  CHECK ((status IN ('approved', 'needs_information', 'rejected')) = (review IS NOT NULL));
ALTER TABLE identity_sessions ADD CONSTRAINT identity_sessions_previous_owner_fk
  FOREIGN KEY (tenant_id, user_id, previous_session_id) REFERENCES identity_sessions(tenant_id, user_id, id);
ALTER TABLE identity_sessions ADD CONSTRAINT identity_sessions_previous_not_self
  CHECK (previous_session_id IS NULL OR previous_session_id <> id);
CREATE INDEX identity_sessions_latest_owner ON identity_sessions(tenant_id, user_id, created_at DESC, id DESC);
CREATE INDEX identity_sessions_review_queue ON identity_sessions(tenant_id, updated_at, id) WHERE status = 'submitted';

-- Append-only access and decision receipts. No image bytes or submitted claims
-- are copied into this log. Database login is captured by PostgreSQL itself.
CREATE TABLE identity_review_events (
  id UUID PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  user_id BIGINT,
  session_id UUID,
  actor TEXT NOT NULL CHECK (length(actor) BETWEEN 1 AND 256),
  database_actor TEXT NOT NULL DEFAULT session_user,
  action TEXT NOT NULL CHECK (action IN ('queue_read', 'case_read', 'evidence_read', 'decision', 'withdrawal')),
  revision BIGINT,
  request_sha256 TEXT NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
  details JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  FOREIGN KEY (tenant_id, user_id, session_id) REFERENCES identity_sessions(tenant_id, user_id, id),
  CHECK ((action = 'queue_read' AND user_id IS NULL AND session_id IS NULL AND revision IS NULL) OR
    (action <> 'queue_read' AND user_id IS NOT NULL AND session_id IS NOT NULL AND revision > 0))
);
CREATE INDEX identity_review_events_case ON identity_review_events(tenant_id, user_id, session_id, created_at, id);
CREATE INDEX identity_review_events_access ON identity_review_events(tenant_id, user_id, session_id, actor, revision)
  WHERE action IN ('case_read', 'evidence_read');

-- +goose Down
-- A rollback would misrepresent already-reviewed or non-test cases. Restore the
-- old binary only with admission paused; retain this forward-compatible schema.
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'Identity review contains durable decisions; use a forward migration';
END $$;
-- +goose StatementEnd
