-- +goose Up
-- This cutover deliberately destroys the application-owned credential model.
-- Keycloak is the sole identity authority; no legacy credential data is kept.
DROP TABLE IF EXISTS password_recovery_credentials;
DROP TABLE IF EXISTS used_refresh_tokens;
DROP TABLE IF EXISTS otp_challenges;
DROP TABLE IF EXISTS auth_rate_limits;
DROP TABLE IF EXISTS auth_accounts;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS login_metrics;
DROP TABLE IF EXISTS passports;
DROP TABLE IF EXISTS kyc;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS tenants;

CREATE TABLE tenants (
  id TEXT PRIMARY KEY CONSTRAINT tenant_id_not_reserved CHECK (
    id = btrim(id) AND id <> '' AND lower(id) <> 'default'
  ),
  name TEXT,
  created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE users (
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  issuer TEXT NOT NULL CHECK (issuer = btrim(issuer) AND issuer <> ''),
  subject TEXT NOT NULL CHECK (subject = btrim(subject) AND subject <> ''),
  id BIGINT GENERATED ALWAYS AS IDENTITY,
  fullname TEXT NOT NULL CHECK (fullname = btrim(fullname) AND fullname <> ''),
  username TEXT CHECK (username IS NULL OR (username = btrim(username) AND username <> '')),
  gender TEXT CHECK (gender IS NULL OR (gender = btrim(gender) AND gender <> '')),
  birthday TEXT CHECK (birthday IS NULL OR (birthday = btrim(birthday) AND birthday <> '')),
  email TEXT CHECK (email IS NULL OR (email = btrim(email) AND email <> '')),
  device_token TEXT CHECK (device_token IS NULL OR (device_token = btrim(device_token) AND device_token <> '')),
  language TEXT CHECK (language IS NULL OR (language = btrim(language) AND language <> '')),
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, issuer, subject),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, username),
  UNIQUE (tenant_id, email)
);

CREATE TABLE kyc (
  tenant_id TEXT NOT NULL,
  user_id BIGINT NOT NULL,
  selfie TEXT,
  passport_img TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, user_id),
  FOREIGN KEY (tenant_id, user_id) REFERENCES users(tenant_id, id)
);

CREATE TABLE passports (
  tenant_id TEXT NOT NULL,
  user_id BIGINT NOT NULL,
  birth_date TIMESTAMPTZ,
  issue_date TIMESTAMPTZ,
  expiration_date TIMESTAMPTZ,
  national_number TEXT,
  passport_number TEXT,
  gender TEXT,
  nationality TEXT,
  holder_name TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, user_id),
  FOREIGN KEY (tenant_id, user_id) REFERENCES users(tenant_id, id)
);

-- +goose Down
-- Forward-only authority cutover. The application credential records removed
-- above cannot be reconstructed, so the schema version must never move back.
-- +goose StatementBegin
DO $$
BEGIN
  RAISE EXCEPTION 'identity auth migration 104 is irreversible';
END
$$;
-- +goose StatementEnd
