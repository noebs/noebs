-- +goose Up
-- Evidence intake is NoEBS-owned. No verification result or wallet tier is
-- inferred from a camera upload, OCR output or client-reported head movement.
CREATE TABLE identity_sessions (
  tenant_id TEXT NOT NULL,
  user_id BIGINT NOT NULL,
  id UUID NOT NULL,
  document_type TEXT NOT NULL CHECK (document_type IN ('passport', 'national_id')),
  synthetic BOOLEAN NOT NULL CHECK (synthetic),
  status TEXT NOT NULL CHECK (status IN ('draft', 'submitted', 'discarded')),
  revision BIGINT NOT NULL CHECK (revision > 0),
  submission JSONB,
  submission_sha256 TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, user_id, id),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, user_id) REFERENCES users(tenant_id, id),
  CHECK ((status = 'submitted') = (submission IS NOT NULL AND submission_sha256 IS NOT NULL)),
  CHECK (submission_sha256 IS NULL OR submission_sha256 ~ '^[0-9a-f]{64}$')
);

CREATE TABLE identity_evidence (
  tenant_id TEXT NOT NULL,
  user_id BIGINT NOT NULL,
  session_id UUID NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('document_front', 'document_back', 'selfie')),
  content_type TEXT NOT NULL CHECK (content_type = 'image/jpeg'),
  image_bytes BYTEA NOT NULL CHECK (octet_length(image_bytes) BETWEEN 1 AND 2097152),
  sha256 TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, user_id, session_id, kind),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, user_id, session_id) REFERENCES identity_sessions(tenant_id, user_id, id)
);

-- +goose Down
DROP TABLE identity_evidence;
DROP TABLE identity_sessions;
