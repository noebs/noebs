-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS tenants (
  id TEXT PRIMARY KEY CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(id)) <> 'default'),
  name TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wallets (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id TEXT NOT NULL,
  owner_type TEXT NOT NULL CHECK (owner_type IN ('user', 'system', 'merchant', 'psp')),
  owner_id TEXT NOT NULL,
  user_id BIGINT,
  currency TEXT NOT NULL,
  balance BIGINT NOT NULL DEFAULT 0,
  available_balance BIGINT NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'frozen', 'closed')),
  wallet_pin_hash TEXT,
  kyc_tier TEXT NOT NULL DEFAULT 'unverified',
  version BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (available_balance <= balance),
  UNIQUE(tenant_id, owner_type, owner_id, currency)
);

CREATE TABLE IF NOT EXISTS ledger_transactions (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  currency TEXT NOT NULL,
  reference_type TEXT NOT NULL,
  reference_id TEXT,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed', 'reversed')),
  metadata JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(tenant_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS ledger_entries (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  transaction_id BIGINT NOT NULL REFERENCES ledger_transactions(id),
  wallet_id UUID NOT NULL REFERENCES wallets(id),
  entry_type TEXT NOT NULL CHECK (entry_type IN ('debit', 'credit')),
  amount BIGINT NOT NULL CHECK (amount > 0),
  currency TEXT NOT NULL,
  balance_after BIGINT NOT NULL,
  wallet_sequence BIGINT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed', 'reversed')),
  counter_entry_id BIGINT REFERENCES ledger_entries(id),
  description TEXT,
  metadata JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(tenant_id, wallet_id, wallet_sequence)
);

CREATE TABLE IF NOT EXISTS balance_holds (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  wallet_id UUID NOT NULL REFERENCES wallets(id),
  amount BIGINT NOT NULL,
  amount_remaining BIGINT NOT NULL,
  reason TEXT NOT NULL,
  reference_type TEXT NOT NULL,
  reference_id TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'released', 'expired', 'canceled')),
  expires_at TIMESTAMPTZ NOT NULL,
  released_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  metadata JSONB,
  CHECK (amount_remaining <= amount),
  UNIQUE(tenant_id, wallet_id, reference_type, reference_id)
);

CREATE INDEX IF NOT EXISTS idx_wallets_tenant_user ON wallets(tenant_id, user_id)
  WHERE owner_type = 'user' AND user_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uniq_wallets_user_currency ON wallets(tenant_id, user_id, currency)
  WHERE owner_type = 'user' AND user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_wallets_owner ON wallets(tenant_id, owner_type, owner_id);

CREATE INDEX IF NOT EXISTS idx_ledger_tx_reference ON ledger_transactions(tenant_id, reference_type, reference_id);
CREATE INDEX IF NOT EXISTS idx_ledger_tx_status ON ledger_transactions(tenant_id, status, created_at);

CREATE INDEX IF NOT EXISTS idx_ledger_entries_wallet ON ledger_entries(tenant_id, wallet_id, wallet_sequence DESC);
CREATE INDEX IF NOT EXISTS idx_ledger_entries_tx ON ledger_entries(tenant_id, transaction_id);

CREATE INDEX IF NOT EXISTS idx_holds_wallet ON balance_holds(tenant_id, wallet_id) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_holds_expiry ON balance_holds(tenant_id, expires_at) WHERE status = 'active';

-- +goose Down
DROP INDEX IF EXISTS idx_holds_expiry;
DROP INDEX IF EXISTS idx_holds_wallet;

DROP INDEX IF EXISTS idx_ledger_entries_tx;
DROP INDEX IF EXISTS idx_ledger_entries_wallet;

DROP INDEX IF EXISTS idx_ledger_tx_status;
DROP INDEX IF EXISTS idx_ledger_tx_reference;

DROP INDEX IF EXISTS idx_wallets_owner;
DROP INDEX IF EXISTS uniq_wallets_user_currency;
DROP INDEX IF EXISTS idx_wallets_tenant_user;

DROP TABLE IF EXISTS balance_holds;
DROP TABLE IF EXISTS ledger_entries;
DROP TABLE IF EXISTS ledger_transactions;
DROP TABLE IF EXISTS wallets;
