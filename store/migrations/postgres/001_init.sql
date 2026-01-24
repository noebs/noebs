-- +goose Up
CREATE TABLE IF NOT EXISTS schema_migrations (
  version TEXT PRIMARY KEY,
  applied_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS tenants (
  id TEXT PRIMARY KEY,
  name TEXT,
  created_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
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
  firebase_token TEXT,
  is_password_otp BOOLEAN NOT NULL DEFAULT FALSE,
  main_card TEXT,
  main_expdate TEXT,
  language TEXT,
  is_verified BOOLEAN NOT NULL DEFAULT FALSE,
  mobile TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  deleted_at TIMESTAMP,
  UNIQUE(tenant_id, mobile),
  UNIQUE(tenant_id, username),
  UNIQUE(tenant_id, email)
);
CREATE INDEX IF NOT EXISTS idx_users_tenant_mobile ON users(tenant_id, mobile);

CREATE TABLE IF NOT EXISTS auth_accounts (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  user_id BIGINT NOT NULL REFERENCES users(id),
  provider TEXT NOT NULL,
  provider_user_id TEXT NOT NULL,
  email TEXT,
  email_verified BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  UNIQUE(tenant_id, provider, provider_user_id)
);
CREATE INDEX IF NOT EXISTS idx_auth_accounts_user ON auth_accounts(tenant_id, user_id);

CREATE TABLE IF NOT EXISTS cards (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  user_id BIGINT NOT NULL REFERENCES users(id),
  pan TEXT NOT NULL,
  expiry TEXT,
  name TEXT,
  ipin TEXT,
  is_main BOOLEAN NOT NULL DEFAULT FALSE,
  is_valid BOOLEAN,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  deleted_at TIMESTAMP,
  UNIQUE(tenant_id, user_id, pan)
);
CREATE INDEX IF NOT EXISTS idx_cards_user ON cards(tenant_id, user_id);
CREATE INDEX IF NOT EXISTS idx_cards_pan ON cards(tenant_id, pan);

CREATE TABLE IF NOT EXISTS cache_cards (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  pan TEXT NOT NULL,
  expiry TEXT,
  name TEXT,
  is_valid BOOLEAN,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  UNIQUE(tenant_id, pan)
);

CREATE TABLE IF NOT EXISTS cache_billers (
  tenant_id TEXT NOT NULL,
  mobile TEXT NOT NULL,
  biller_id TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  PRIMARY KEY (tenant_id, mobile)
);

CREATE TABLE IF NOT EXISTS beneficiaries (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  user_id BIGINT NOT NULL REFERENCES users(id),
  data TEXT NOT NULL,
  bill_type TEXT NOT NULL,
  name TEXT,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_beneficiaries_user ON beneficiaries(tenant_id, user_id);

CREATE TABLE IF NOT EXISTS tokens (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  user_id BIGINT NOT NULL REFERENCES users(id),
  amount INTEGER,
  cart_id TEXT,
  uuid TEXT NOT NULL,
  note TEXT,
  to_card TEXT,
  is_paid BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  UNIQUE(tenant_id, uuid)
);

CREATE TABLE IF NOT EXISTS transactions (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  token_id BIGINT,
  uuid TEXT,
  response_code INTEGER,
  response_message TEXT,
  response_status TEXT,
  tran_date_time TEXT,
  tran_amount NUMERIC,
  tran_fee NUMERIC,
  pan TEXT,
  sender_pan TEXT,
  receiver_pan TEXT,
  terminal_id TEXT,
  system_trace_audit_number INTEGER,
  approval_code TEXT,
  service_id TEXT,
  merchant_id TEXT,
  bill_type TEXT,
  bill_to TEXT,
  bill_info2 TEXT,
  payload JSONB,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_transactions_pan ON transactions(tenant_id, pan);
CREATE INDEX IF NOT EXISTS idx_transactions_sender_pan ON transactions(tenant_id, sender_pan);
CREATE INDEX IF NOT EXISTS idx_transactions_receiver_pan ON transactions(tenant_id, receiver_pan);
CREATE INDEX IF NOT EXISTS idx_transactions_uuid ON transactions(tenant_id, uuid);
CREATE INDEX IF NOT EXISTS idx_transactions_terminal_id ON transactions(tenant_id, terminal_id);
CREATE INDEX IF NOT EXISTS idx_transactions_system_trace ON transactions(tenant_id, system_trace_audit_number);

CREATE TABLE IF NOT EXISTS push_data (
  uuid TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  type TEXT,
  date BIGINT,
  to_device TEXT,
  title TEXT,
  body TEXT,
  call_to_action TEXT,
  phone TEXT,
  is_read BOOLEAN NOT NULL DEFAULT FALSE,
  device_id TEXT,
  user_mobile TEXT,
  ebs_uuid TEXT,
  payment_request JSONB,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  deleted_at TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_push_user ON push_data(tenant_id, user_mobile);
CREATE INDEX IF NOT EXISTS idx_push_phone ON push_data(tenant_id, phone);

CREATE TABLE IF NOT EXISTS api_keys (
  tenant_id TEXT NOT NULL,
  email TEXT NOT NULL,
  api_key TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL,
  PRIMARY KEY (tenant_id, email)
);

CREATE TABLE IF NOT EXISTS login_metrics (
  tenant_id TEXT NOT NULL,
  mobile TEXT NOT NULL,
  login_count INTEGER NOT NULL DEFAULT 0,
  window_started_at TIMESTAMP NOT NULL,
  suspicious_count INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (tenant_id, mobile)
);

CREATE TABLE IF NOT EXISTS meter_names (
  tenant_id TEXT NOT NULL,
  nec TEXT NOT NULL,
  name TEXT NOT NULL,
  PRIMARY KEY (tenant_id, nec)
);

CREATE TABLE IF NOT EXISTS kyc (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  user_mobile TEXT NOT NULL,
  mobile TEXT NOT NULL,
  selfie TEXT,
  passport_img TEXT,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  UNIQUE(tenant_id, mobile)
);

CREATE TABLE IF NOT EXISTS passports (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  mobile TEXT NOT NULL,
  birth_date TIMESTAMP,
  issue_date TIMESTAMP,
  expiration_date TIMESTAMP,
  national_number TEXT,
  passport_number TEXT,
  gender TEXT,
  nationality TEXT,
  holder_name TEXT,
  created_at TIMESTAMP NOT NULL,
  updated_at TIMESTAMP NOT NULL,
  UNIQUE(tenant_id, mobile)
);

CREATE TABLE IF NOT EXISTS merchant_issues (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  terminal_id TEXT NOT NULL,
  latitude NUMERIC,
  longitude NUMERIC,
  reported_at TIMESTAMP NOT NULL,
  created_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_merchant_issues_terminal ON merchant_issues(tenant_id, terminal_id);

-- +goose Down
