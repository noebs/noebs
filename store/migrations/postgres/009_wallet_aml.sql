-- +goose Up
CREATE TABLE IF NOT EXISTS funding_sources (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  wallet_id UUID NOT NULL REFERENCES wallets(id),
  source_type TEXT NOT NULL,
  psp_provider TEXT,
  external_reference TEXT,
  verification_status TEXT NOT NULL DEFAULT 'pending',
  verified_at TIMESTAMPTZ,
  verified_by TEXT,
  currency TEXT NOT NULL,
  source_details JSONB NOT NULL,
  total_funded BIGINT NOT NULL DEFAULT 0,
  last_funded_at TIMESTAMPTZ,
  total_withdrawn BIGINT NOT NULL DEFAULT 0,
  last_withdrawn_at TIMESTAMPTZ,
  supports_withdrawal BOOLEAN NOT NULL DEFAULT FALSE,
  withdrawal_method JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(tenant_id, wallet_id, source_type, external_reference)
);

CREATE TABLE IF NOT EXISTS ledger_funding_links (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  ledger_entry_id BIGINT NOT NULL REFERENCES ledger_entries(id),
  funding_source_id BIGINT NOT NULL REFERENCES funding_sources(id),
  amount BIGINT NOT NULL,
  currency TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(tenant_id, ledger_entry_id, funding_source_id)
);

CREATE TABLE IF NOT EXISTS withdrawal_destinations (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  wallet_id UUID NOT NULL REFERENCES wallets(id),
  destination_type TEXT NOT NULL,
  psp_provider TEXT,
  destination_details JSONB NOT NULL,
  display_name TEXT,
  currency TEXT NOT NULL,
  country TEXT,
  ownership_status TEXT NOT NULL DEFAULT 'unverified'
    CHECK (ownership_status IN ('unverified', 'pending', 'verified', 'rejected')),
  ownership_verification_method TEXT,
  ownership_verified_at TIMESTAMPTZ,
  ownership_verified_by TEXT,
  ownership_proof JSONB,
  linked_funding_source_id BIGINT REFERENCES funding_sources(id),
  is_return_to_source BOOLEAN NOT NULL DEFAULT FALSE,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  last_used_at TIMESTAMPTZ,
  total_withdrawn BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS ownership_verifications (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  destination_id BIGINT NOT NULL REFERENCES withdrawal_destinations(id),
  verification_type TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  micro_deposit_amounts BIGINT[],
  micro_deposit_confirmed_at TIMESTAMPTZ,
  card_verification_amount BIGINT,
  document_type TEXT,
  document_url TEXT,
  attempts INT NOT NULL DEFAULT 0,
  max_attempts INT NOT NULL DEFAULT 3,
  expires_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ,
  workflow_id TEXT,
  reference_id TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_funding_sources_wallet ON funding_sources(tenant_id, wallet_id);
CREATE INDEX IF NOT EXISTS idx_funding_sources_psp ON funding_sources(tenant_id, psp_provider, external_reference);
CREATE INDEX IF NOT EXISTS idx_funding_links_entry ON ledger_funding_links(tenant_id, ledger_entry_id);
CREATE INDEX IF NOT EXISTS idx_funding_links_source ON ledger_funding_links(tenant_id, funding_source_id);
CREATE INDEX IF NOT EXISTS idx_destinations_wallet ON withdrawal_destinations(tenant_id, wallet_id);
CREATE INDEX IF NOT EXISTS idx_destinations_linked ON withdrawal_destinations(linked_funding_source_id)
  WHERE linked_funding_source_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_destinations_linked;
DROP INDEX IF EXISTS idx_destinations_wallet;
DROP INDEX IF EXISTS idx_funding_links_source;
DROP INDEX IF EXISTS idx_funding_links_entry;
DROP INDEX IF EXISTS idx_funding_sources_psp;
DROP INDEX IF EXISTS idx_funding_sources_wallet;

DROP TABLE IF EXISTS ownership_verifications;
DROP TABLE IF EXISTS withdrawal_destinations;
DROP TABLE IF EXISTS ledger_funding_links;
DROP TABLE IF EXISTS funding_sources;
