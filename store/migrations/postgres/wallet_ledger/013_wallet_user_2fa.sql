-- +goose Up
CREATE TABLE IF NOT EXISTS wallet_user_2fa (
  tenant_id TEXT NOT NULL,
  user_id BIGINT NOT NULL,
  secret TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  enabled_at TIMESTAMPTZ,
  disabled_at TIMESTAMPTZ,
  last_used_at TIMESTAMPTZ,
  PRIMARY KEY (tenant_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_wallet_user_2fa_enabled ON wallet_user_2fa(tenant_id, enabled);

-- +goose Down
DROP INDEX IF EXISTS idx_wallet_user_2fa_enabled;
DROP TABLE IF EXISTS wallet_user_2fa;
