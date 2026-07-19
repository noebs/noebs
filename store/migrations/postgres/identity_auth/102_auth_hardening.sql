-- +goose Up
CREATE TABLE IF NOT EXISTS auth_rate_limits (
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  action TEXT NOT NULL,
  subject_hash TEXT NOT NULL CHECK (length(subject_hash) = 64),
  attempt_count INTEGER NOT NULL CHECK (attempt_count > 0),
  window_started_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, action, subject_hash)
);
CREATE INDEX IF NOT EXISTS idx_auth_rate_limits_updated_at ON auth_rate_limits(updated_at);

CREATE TABLE IF NOT EXISTS otp_challenges (
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  mobile TEXT NOT NULL,
  code_digest TEXT NOT NULL CHECK (length(code_digest) = 64),
  expires_at TIMESTAMPTZ NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  max_attempts INTEGER NOT NULL CHECK (max_attempts > 0),
  consumed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, mobile)
);
CREATE INDEX IF NOT EXISTS idx_otp_challenges_expires_at ON otp_challenges(expires_at);

CREATE TABLE IF NOT EXISTS used_refresh_tokens (
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  token_hash TEXT NOT NULL CHECK (length(token_hash) = 64),
  user_id BIGINT NOT NULL CHECK (user_id > 0),
  expires_at TIMESTAMPTZ NOT NULL,
  consumed_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, token_hash)
);
CREATE INDEX IF NOT EXISTS idx_used_refresh_tokens_expires_at ON used_refresh_tokens(expires_at);

-- +goose Down
DROP TABLE IF EXISTS used_refresh_tokens;
DROP TABLE IF EXISTS otp_challenges;
DROP TABLE IF EXISTS auth_rate_limits;
