-- +goose Up
CREATE TABLE IF NOT EXISTS admin_roles (
  id SERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  role_name TEXT NOT NULL,
  role_level INT NOT NULL,
  permissions JSONB NOT NULL,
  UNIQUE(tenant_id, role_name)
);

CREATE TABLE IF NOT EXISTS admin_users (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  email TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  role_id INT REFERENCES admin_roles(id),
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  totp_secret TEXT,
  last_login_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(tenant_id, email)
);

CREATE TABLE IF NOT EXISTS manual_transfers (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  workflow_id TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  transfer_type TEXT NOT NULL,
  wallet_id UUID REFERENCES wallets(id),
  amount BIGINT NOT NULL,
  currency TEXT NOT NULL,
  reason TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'completed')),
  requested_by BIGINT REFERENCES admin_users(id),
  approved_by BIGINT REFERENCES admin_users(id),
  proof_of_payment TEXT,
  psp_provider TEXT,
  psp_reference TEXT,
  rejection_reason TEXT,
  requested_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  approved_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  CHECK (approved_by IS NULL OR approved_by <> requested_by),
  UNIQUE(tenant_id, workflow_id),
  UNIQUE(tenant_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS manual_transfer_approvals (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  manual_transfer_id BIGINT NOT NULL REFERENCES manual_transfers(id),
  approver_id BIGINT NOT NULL REFERENCES admin_users(id),
  decision TEXT NOT NULL CHECK (decision IN ('approved', 'rejected')),
  reason TEXT,
  decided_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(tenant_id, manual_transfer_id, approver_id)
);

CREATE TABLE IF NOT EXISTS wallet_audit_log (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  event_type TEXT NOT NULL,
  actor_type TEXT NOT NULL,
  actor_id TEXT NOT NULL,
  target_type TEXT,
  target_id TEXT,
  action TEXT NOT NULL,
  old_value JSONB,
  new_value JSONB,
  metadata JSONB,
  ip_address INET,
  user_agent TEXT,
  workflow_id TEXT,
  request_id TEXT,
  trace_id TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_manual_status ON manual_transfers(tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_manual_approvals ON manual_transfer_approvals(tenant_id, manual_transfer_id);

CREATE INDEX IF NOT EXISTS idx_audit_actor ON wallet_audit_log(tenant_id, actor_type, actor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_target ON wallet_audit_log(tenant_id, target_type, target_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_time ON wallet_audit_log(tenant_id, created_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_audit_time;
DROP INDEX IF EXISTS idx_audit_target;
DROP INDEX IF EXISTS idx_audit_actor;

DROP INDEX IF EXISTS idx_manual_approvals;
DROP INDEX IF EXISTS idx_manual_status;

DROP TABLE IF EXISTS wallet_audit_log;
DROP TABLE IF EXISTS manual_transfer_approvals;
DROP TABLE IF EXISTS manual_transfers;
DROP TABLE IF EXISTS admin_users;
DROP TABLE IF EXISTS admin_roles;
