-- +goose Up
ALTER TABLE otp_challenges
  ADD COLUMN purpose TEXT NOT NULL DEFAULT 'signin'
  CHECK (purpose IN ('signin', 'password_recovery'));

ALTER TABLE otp_challenges DROP CONSTRAINT otp_challenges_pkey;
ALTER TABLE otp_challenges ADD PRIMARY KEY (tenant_id, mobile, purpose);

ALTER TABLE users
  ADD COLUMN session_epoch BIGINT NOT NULL DEFAULT 1 CHECK (session_epoch > 0);

CREATE TABLE password_recovery_credentials (
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  token_hash TEXT NOT NULL CHECK (length(token_hash) = 64),
  user_id BIGINT NOT NULL REFERENCES users(id),
  expires_at TIMESTAMPTZ NOT NULL,
  consumed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, token_hash)
);
CREATE INDEX idx_password_recovery_credentials_user
  ON password_recovery_credentials(tenant_id, user_id);
CREATE INDEX idx_password_recovery_credentials_expires_at
  ON password_recovery_credentials(expires_at);

-- +goose Down
DROP TABLE IF EXISTS password_recovery_credentials;

ALTER TABLE users DROP COLUMN session_epoch;

DELETE FROM otp_challenges WHERE purpose <> 'signin';
ALTER TABLE otp_challenges DROP CONSTRAINT otp_challenges_pkey;
ALTER TABLE otp_challenges DROP COLUMN purpose;
ALTER TABLE otp_challenges ADD PRIMARY KEY (tenant_id, mobile);
