-- +goose Up
CREATE TABLE IF NOT EXISTS tenants (
  id TEXT PRIMARY KEY CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(id)) <> 'default'),
  name TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS users (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  password TEXT,
  fullname TEXT,
  username TEXT,
  gender TEXT,
  birthday TEXT,
  email TEXT,
  is_merchant BOOLEAN NOT NULL DEFAULT FALSE,
  public_key TEXT,
  device_id TEXT,
  otp TEXT,
  signed_otp TEXT,
  device_token TEXT,
  is_password_otp BOOLEAN NOT NULL DEFAULT FALSE,
  main_card TEXT,
  main_card_enc TEXT,
  main_expdate TEXT,
  language TEXT,
  is_verified BOOLEAN NOT NULL DEFAULT FALSE,
  mobile TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  deleted_at TIMESTAMPTZ,
  UNIQUE(tenant_id, mobile),
  UNIQUE(tenant_id, username),
  UNIQUE(tenant_id, email)
);
CREATE INDEX IF NOT EXISTS idx_identity_users_tenant_mobile ON users(tenant_id, mobile);

CREATE TABLE IF NOT EXISTS auth_accounts (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  user_id BIGINT NOT NULL REFERENCES users(id),
  provider TEXT NOT NULL,
  provider_user_id TEXT NOT NULL,
  email TEXT,
  email_verified BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  UNIQUE(tenant_id, provider, provider_user_id)
);
CREATE INDEX IF NOT EXISTS idx_identity_auth_accounts_user ON auth_accounts(tenant_id, user_id);

CREATE TABLE IF NOT EXISTS api_keys (
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  email TEXT NOT NULL,
  api_key TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id, email)
);

CREATE TABLE IF NOT EXISTS login_metrics (
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  mobile TEXT NOT NULL,
  login_count INTEGER NOT NULL DEFAULT 0,
  window_started_at TIMESTAMPTZ NOT NULL,
  suspicious_count INTEGER NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ,
  PRIMARY KEY (tenant_id, mobile)
);

CREATE TABLE IF NOT EXISTS kyc (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  user_mobile TEXT NOT NULL,
  mobile TEXT NOT NULL,
  selfie TEXT,
  passport_img TEXT,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  UNIQUE(tenant_id, mobile)
);

CREATE TABLE IF NOT EXISTS passports (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CHECK (lower(btrim(tenant_id)) <> 'default'),
  mobile TEXT NOT NULL,
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
  UNIQUE(tenant_id, mobile)
);

-- +goose Down
