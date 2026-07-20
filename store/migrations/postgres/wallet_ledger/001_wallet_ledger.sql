-- +goose Up
CREATE TABLE tenants (
  id TEXT PRIMARY KEY CONSTRAINT tenant_id_canonical CHECK (
    id <> 'default' AND length(id) <= 63 AND id ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$'
  ),
  name TEXT NOT NULL CHECK (name <> '' AND name = btrim(name)),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Operator identities are immutable audit principals. Authorization remains in
-- the current Keycloak token and is not persisted in the wallet database.
CREATE TABLE operator_identities (
  id BIGSERIAL PRIMARY KEY,
  issuer TEXT NOT NULL CHECK (issuer <> '' AND issuer = btrim(issuer)),
  subject TEXT NOT NULL CHECK (subject <> '' AND subject = btrim(subject)),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (issuer, subject)
);

CREATE TABLE wallets (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  owner_type TEXT NOT NULL CHECK (owner_type IN ('user', 'system', 'merchant', 'psp')),
  owner_id TEXT NOT NULL,
  user_id BIGINT,
  currency TEXT NOT NULL,
  balance BIGINT NOT NULL DEFAULT 0,
  available_balance BIGINT NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'frozen', 'closed')),
  kyc_tier TEXT NOT NULL,
  version BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (available_balance <= balance),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, id, owner_type, owner_id),
  UNIQUE (tenant_id, owner_type, owner_id, currency)
);

CREATE INDEX idx_wallets_tenant_user ON wallets(tenant_id, user_id)
  WHERE owner_type = 'user' AND user_id IS NOT NULL;
CREATE UNIQUE INDEX uniq_wallets_user_currency ON wallets(tenant_id, user_id, currency)
  WHERE owner_type = 'user' AND user_id IS NOT NULL;
CREATE INDEX idx_wallets_owner ON wallets(tenant_id, owner_type, owner_id);

CREATE TABLE ledger_transactions (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  idempotency_key TEXT NOT NULL,
  currency TEXT NOT NULL,
  reference_type TEXT NOT NULL,
  reference_id TEXT,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed', 'reversed')),
  metadata JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX idx_ledger_tx_reference
  ON ledger_transactions(tenant_id, reference_type, reference_id);
CREATE INDEX idx_ledger_tx_status
  ON ledger_transactions(tenant_id, status, created_at);

CREATE TABLE ledger_entries (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  transaction_id BIGINT NOT NULL,
  wallet_id UUID NOT NULL,
  entry_type TEXT NOT NULL CHECK (entry_type IN ('debit', 'credit')),
  amount BIGINT NOT NULL CHECK (amount > 0),
  currency TEXT NOT NULL,
  balance_after BIGINT NOT NULL,
  wallet_sequence BIGINT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed', 'reversed')),
  counter_entry_id BIGINT,
  description TEXT,
  metadata JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, transaction_id) REFERENCES ledger_transactions(tenant_id, id),
  FOREIGN KEY (tenant_id, wallet_id) REFERENCES wallets(tenant_id, id),
  FOREIGN KEY (tenant_id, counter_entry_id) REFERENCES ledger_entries(tenant_id, id),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, wallet_id, wallet_sequence)
);

CREATE INDEX idx_ledger_entries_wallet
  ON ledger_entries(tenant_id, wallet_id, wallet_sequence DESC);
CREATE INDEX idx_ledger_entries_tx
  ON ledger_entries(tenant_id, transaction_id);

CREATE TABLE balance_holds (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  wallet_id UUID NOT NULL,
  amount BIGINT NOT NULL,
  amount_remaining BIGINT NOT NULL,
  reason TEXT NOT NULL,
  reference_type TEXT NOT NULL,
  reference_id TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'committed', 'released', 'expired', 'captured')),
  expires_at TIMESTAMPTZ NOT NULL,
  released_at TIMESTAMPTZ,
  committed_at TIMESTAMPTZ,
  expired_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  metadata JSONB,
  captured_at TIMESTAMPTZ,
  CHECK (amount > 0),
  CHECK (amount_remaining >= 0 AND amount_remaining <= amount),
  CHECK (expires_at > created_at),
  CHECK ((status = 'released') = (released_at IS NOT NULL)),
  CHECK ((status = 'captured') = (captured_at IS NOT NULL)),
  CHECK (status <> 'committed' OR committed_at IS NOT NULL),
  CHECK (committed_at IS NULL OR status IN ('committed', 'released', 'captured')),
  CHECK ((status = 'expired') = (expired_at IS NOT NULL)),
  CHECK (status NOT IN ('released', 'expired', 'captured') OR amount_remaining = 0),
  CHECK (status NOT IN ('active', 'committed') OR amount_remaining > 0),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, wallet_id) REFERENCES wallets(tenant_id, id),
  UNIQUE (tenant_id, wallet_id, reference_type, reference_id)
);

CREATE INDEX idx_holds_wallet
  ON balance_holds(tenant_id, wallet_id) WHERE status = 'active';
CREATE INDEX idx_holds_expiry
  ON balance_holds(tenant_id, expires_at) WHERE status = 'active';
CREATE INDEX idx_holds_committed_reference
  ON balance_holds(tenant_id, reference_type, reference_id) WHERE status = 'committed';

CREATE TABLE fee_configs (
  id SERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  transaction_type TEXT NOT NULL,
  currency TEXT NOT NULL,
  tier_min BIGINT NOT NULL DEFAULT 0,
  tier_max BIGINT,
  percentage_fee NUMERIC(8,4) NOT NULL,
  flat_fee BIGINT NOT NULL DEFAULT 0,
  min_fee BIGINT NOT NULL DEFAULT 0,
  max_fee BIGINT,
  fee_account_code TEXT,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_by_operator_id BIGINT NOT NULL REFERENCES operator_identities(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  UNIQUE (tenant_id, transaction_type, currency, tier_min)
);

CREATE TABLE exchange_rates (
  id SERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  base_currency TEXT NOT NULL,
  quote_currency TEXT NOT NULL,
  buy_rate NUMERIC(18,8) NOT NULL,
  sell_rate NUMERIC(18,8) NOT NULL,
  spread NUMERIC(8,4),
  set_by_operator_id BIGINT NOT NULL REFERENCES operator_identities(id),
  effective_from TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  effective_to TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (buy_rate > 0 AND sell_rate > 0),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  UNIQUE (tenant_id, base_currency, quote_currency, effective_from)
);

CREATE TABLE transaction_limits (
  id SERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  kyc_tier TEXT NOT NULL,
  transaction_type TEXT NOT NULL,
  currency TEXT NOT NULL,
  daily_limit BIGINT NOT NULL,
  monthly_limit BIGINT NOT NULL,
  per_transaction_limit BIGINT NOT NULL,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  CHECK (daily_limit > 0),
  CHECK (monthly_limit > 0),
  CHECK (per_transaction_limit > 0),
  CHECK (daily_limit <= monthly_limit),
  CHECK (per_transaction_limit <= daily_limit),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  UNIQUE (tenant_id, kyc_tier, transaction_type, currency)
);

CREATE TABLE transaction_limit_period_usage (
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  wallet_id UUID NOT NULL,
  transaction_type TEXT NOT NULL CHECK (transaction_type <> '' AND transaction_type = btrim(transaction_type)),
  currency TEXT NOT NULL CHECK (currency <> '' AND currency = btrim(currency)),
  period_kind TEXT NOT NULL CHECK (period_kind IN ('daily', 'monthly')),
  period_start DATE NOT NULL,
  reserved_amount BIGINT NOT NULL DEFAULT 0 CHECK (reserved_amount >= 0),
  consumed_amount BIGINT NOT NULL DEFAULT 0 CHECK (consumed_amount >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, wallet_id) REFERENCES wallets(tenant_id, id),
  PRIMARY KEY (tenant_id, wallet_id, transaction_type, currency, period_kind, period_start)
);

CREATE TABLE transaction_limit_reservations (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  command_id TEXT NOT NULL CHECK (command_id <> '' AND command_id = btrim(command_id)),
  wallet_id UUID NOT NULL,
  transaction_type TEXT NOT NULL CHECK (transaction_type <> '' AND transaction_type = btrim(transaction_type)),
  currency TEXT NOT NULL CHECK (currency <> '' AND currency = btrim(currency)),
  amount BIGINT NOT NULL CHECK (amount > 0),
  daily_period_start DATE NOT NULL,
  monthly_period_start DATE NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('reserved', 'consumed', 'released')),
  ledger_transaction_id BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  consumed_at TIMESTAMPTZ,
  released_at TIMESTAMPTZ,
  CHECK ((status = 'reserved') = (consumed_at IS NULL AND released_at IS NULL AND ledger_transaction_id IS NULL)),
  CHECK ((status = 'consumed') = (consumed_at IS NOT NULL AND released_at IS NULL AND ledger_transaction_id IS NOT NULL)),
  CHECK ((status = 'released') = (released_at IS NOT NULL AND consumed_at IS NULL AND ledger_transaction_id IS NULL)),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, wallet_id) REFERENCES wallets(tenant_id, id),
  FOREIGN KEY (tenant_id, ledger_transaction_id) REFERENCES ledger_transactions(tenant_id, id),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, command_id)
);

CREATE INDEX idx_transaction_limit_reservations_period
  ON transaction_limit_reservations(
    tenant_id, wallet_id, transaction_type, currency, daily_period_start, monthly_period_start, status
  );

CREATE TABLE manual_transfers (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  workflow_id TEXT NOT NULL,
  idempotency_key TEXT NOT NULL,
  transfer_type TEXT NOT NULL,
  wallet_id UUID,
  amount BIGINT NOT NULL,
  currency TEXT NOT NULL,
  reason TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'approved', 'rejected', 'completed')),
  requested_by_operator_id BIGINT NOT NULL REFERENCES operator_identities(id),
  approved_by_operator_id BIGINT REFERENCES operator_identities(id),
  proof_of_payment TEXT,
  psp_provider TEXT,
  psp_reference TEXT,
  rejection_reason TEXT,
  approval_timeout_seconds INTEGER NOT NULL CHECK (approval_timeout_seconds > 0),
  decision_deadline_at TIMESTAMPTZ NOT NULL,
  requested_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  approved_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  CHECK (approved_by_operator_id IS NULL OR approved_by_operator_id <> requested_by_operator_id),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, wallet_id) REFERENCES wallets(tenant_id, id),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, workflow_id),
  UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX idx_manual_status ON manual_transfers(tenant_id, status);

CREATE TABLE manual_transfer_approvals (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  manual_transfer_id BIGINT NOT NULL,
  decided_by_operator_id BIGINT NOT NULL REFERENCES operator_identities(id),
  decision TEXT NOT NULL CHECK (decision IN ('approved', 'rejected')),
  reason TEXT,
  decided_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, manual_transfer_id) REFERENCES manual_transfers(tenant_id, id),
  UNIQUE (tenant_id, manual_transfer_id, decided_by_operator_id)
);

CREATE INDEX idx_manual_approvals
  ON manual_transfer_approvals(tenant_id, manual_transfer_id);

-- A workflow decision can target either manual_transfers or psp_transactions.
-- PostgreSQL cannot express that decision_kind-dependent parent as one foreign
-- key; the decision store locks and validates the exact typed parent before it
-- inserts this immutable row.
CREATE TABLE workflow_decisions (
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  workflow_id TEXT NOT NULL CHECK (workflow_id <> '' AND workflow_id = btrim(workflow_id) AND length(workflow_id) <= 255),
  decision_kind TEXT NOT NULL CHECK (decision_kind IN ('manual_transfer', 'withdrawal')),
  subject_id BIGINT NOT NULL CHECK (subject_id > 0),
  approved BOOLEAN NOT NULL,
  decided_by_operator_id BIGINT NOT NULL REFERENCES operator_identities(id),
  reason TEXT,
  proof_of_payment TEXT,
  decided_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  PRIMARY KEY (tenant_id, workflow_id),
  CHECK (
    (approved AND proof_of_payment IS NOT NULL AND proof_of_payment <> '' AND proof_of_payment = btrim(proof_of_payment)
      AND length(proof_of_payment) <= 4096
      AND (reason IS NULL OR (reason <> '' AND reason = btrim(reason) AND length(reason) <= 4096)))
    OR
    (NOT approved AND reason IS NOT NULL AND reason <> '' AND reason = btrim(reason)
      AND length(reason) <= 4096 AND proof_of_payment IS NULL)
  )
);

CREATE INDEX idx_workflow_decisions_subject
  ON workflow_decisions(tenant_id, decision_kind, subject_id);

CREATE TABLE wallet_audit_log (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
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
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id)
);

CREATE INDEX idx_audit_actor
  ON wallet_audit_log(tenant_id, actor_type, actor_id, created_at DESC);
CREATE INDEX idx_audit_target
  ON wallet_audit_log(tenant_id, target_type, target_id, created_at DESC);
CREATE INDEX idx_audit_time
  ON wallet_audit_log(tenant_id, created_at DESC);

CREATE TABLE funding_sources (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  wallet_id UUID NOT NULL,
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
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (total_funded >= 0),
  CHECK (total_withdrawn >= 0 AND total_withdrawn <= total_funded),
  CHECK (NOT supports_withdrawal OR (psp_provider IS NOT NULL AND psp_provider <> '' AND psp_provider = btrim(psp_provider))),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, wallet_id) REFERENCES wallets(tenant_id, id),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, wallet_id, source_type, external_reference)
);

CREATE INDEX idx_funding_sources_wallet
  ON funding_sources(tenant_id, wallet_id);
CREATE INDEX idx_funding_sources_psp
  ON funding_sources(tenant_id, psp_provider, external_reference);
CREATE UNIQUE INDEX idx_funding_sources_null_external_reference_unique
  ON funding_sources(tenant_id, wallet_id, source_type)
  WHERE external_reference IS NULL;

CREATE TABLE funding_source_withdrawal_reservations (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  workflow_id TEXT NOT NULL CHECK (workflow_id <> '' AND workflow_id = btrim(workflow_id) AND length(workflow_id) <= 255),
  funding_source_id BIGINT NOT NULL,
  amount BIGINT NOT NULL CHECK (amount > 0),
  currency TEXT NOT NULL CHECK (currency <> '' AND currency = btrim(currency)),
  provider_code TEXT NOT NULL CHECK (provider_code <> '' AND provider_code = btrim(provider_code)),
  status TEXT NOT NULL CHECK (status IN ('reserved', 'consumed', 'released')),
  ledger_entry_id BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  consumed_at TIMESTAMPTZ,
  released_at TIMESTAMPTZ,
  CHECK ((status = 'reserved') = (consumed_at IS NULL AND released_at IS NULL)),
  CHECK ((status = 'consumed') = (consumed_at IS NOT NULL AND ledger_entry_id IS NOT NULL)),
  CHECK ((status = 'released') = (released_at IS NOT NULL)),
  CHECK (status <> 'released' OR ledger_entry_id IS NULL),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, funding_source_id) REFERENCES funding_sources(tenant_id, id),
  FOREIGN KEY (tenant_id, ledger_entry_id) REFERENCES ledger_entries(tenant_id, id),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, workflow_id)
);

CREATE INDEX idx_funding_source_withdrawal_reservations_active
  ON funding_source_withdrawal_reservations(tenant_id, funding_source_id)
  WHERE status = 'reserved';

CREATE TABLE ledger_funding_links (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  ledger_entry_id BIGINT NOT NULL,
  funding_source_id BIGINT NOT NULL,
  withdrawal_reservation_id BIGINT,
  amount BIGINT NOT NULL,
  currency TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, ledger_entry_id) REFERENCES ledger_entries(tenant_id, id),
  FOREIGN KEY (tenant_id, funding_source_id) REFERENCES funding_sources(tenant_id, id),
  FOREIGN KEY (tenant_id, withdrawal_reservation_id) REFERENCES funding_source_withdrawal_reservations(tenant_id, id),
  UNIQUE (tenant_id, ledger_entry_id, funding_source_id)
);

CREATE UNIQUE INDEX idx_ledger_funding_links_withdrawal_reservation
  ON ledger_funding_links(tenant_id, withdrawal_reservation_id)
  WHERE withdrawal_reservation_id IS NOT NULL;

CREATE INDEX idx_funding_links_entry
  ON ledger_funding_links(tenant_id, ledger_entry_id);
CREATE INDEX idx_funding_links_source
  ON ledger_funding_links(tenant_id, funding_source_id);

CREATE TABLE withdrawal_destinations (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  wallet_id UUID NOT NULL,
  destination_type TEXT NOT NULL,
  psp_provider TEXT,
  destination_details JSONB NOT NULL,
  display_name TEXT,
  currency TEXT NOT NULL,
  country TEXT,
  linked_funding_source_id BIGINT NOT NULL,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  last_used_at TIMESTAMPTZ,
  total_withdrawn BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, wallet_id) REFERENCES wallets(tenant_id, id),
  FOREIGN KEY (tenant_id, linked_funding_source_id) REFERENCES funding_sources(tenant_id, id),
  UNIQUE (tenant_id, id)
);

CREATE INDEX idx_destinations_wallet
  ON withdrawal_destinations(tenant_id, wallet_id);
CREATE INDEX idx_destinations_linked
  ON withdrawal_destinations(linked_funding_source_id)
  WHERE linked_funding_source_id IS NOT NULL;

CREATE TABLE ledger_withdrawal_destination_links (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  ledger_entry_id BIGINT NOT NULL,
  destination_id BIGINT NOT NULL,
  amount BIGINT NOT NULL,
  currency TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, ledger_entry_id) REFERENCES ledger_entries(tenant_id, id),
  FOREIGN KEY (tenant_id, destination_id) REFERENCES withdrawal_destinations(tenant_id, id),
  UNIQUE (tenant_id, ledger_entry_id, destination_id)
);

CREATE INDEX idx_withdrawal_destination_links_entry
  ON ledger_withdrawal_destination_links(tenant_id, ledger_entry_id);
CREATE INDEX idx_withdrawal_destination_links_destination
  ON ledger_withdrawal_destination_links(tenant_id, destination_id);

CREATE TABLE p2p_commands (
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  idempotency_key TEXT NOT NULL,
  workflow_id TEXT NOT NULL,
  from_wallet_id UUID NOT NULL,
  to_wallet_id UUID NOT NULL,
  from_owner_type TEXT NOT NULL CHECK (from_owner_type IN ('user', 'system', 'merchant', 'psp')),
  from_owner_id TEXT NOT NULL CHECK (from_owner_id <> '' AND from_owner_id = btrim(from_owner_id)),
  to_owner_type TEXT NOT NULL CHECK (to_owner_type IN ('user', 'system', 'merchant', 'psp')),
  to_owner_id TEXT NOT NULL CHECK (to_owner_id <> '' AND to_owner_id = btrim(to_owner_id)),
  command JSONB NOT NULL,
  run_id TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (tenant_id <> '' AND tenant_id = btrim(tenant_id)),
  CHECK (idempotency_key <> '' AND idempotency_key = btrim(idempotency_key) AND length(idempotency_key) <= 256),
  CHECK (workflow_id <> '' AND workflow_id = btrim(workflow_id) AND length(workflow_id) <= 255),
  CHECK (from_wallet_id <> to_wallet_id),
  CHECK (jsonb_typeof(command) = 'object'),
  CHECK (run_id IS NULL OR (run_id <> '' AND run_id = btrim(run_id) AND length(run_id) <= 255)),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, from_wallet_id, from_owner_type, from_owner_id)
    REFERENCES wallets(tenant_id, id, owner_type, owner_id),
  FOREIGN KEY (tenant_id, to_wallet_id, to_owner_type, to_owner_id)
    REFERENCES wallets(tenant_id, id, owner_type, owner_id),
  PRIMARY KEY (tenant_id, idempotency_key)
);

CREATE TABLE psp_configs (
  id SERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  provider_code TEXT NOT NULL,
  provider_name TEXT NOT NULL,
  api_base_url TEXT NOT NULL,
  enabled_currencies TEXT[],
  idempotency_header_name TEXT NOT NULL
    CHECK (idempotency_header_name <> '' AND idempotency_header_name = btrim(idempotency_header_name)),
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  supports_deposit BOOLEAN NOT NULL DEFAULT TRUE,
  supports_withdrawal BOOLEAN NOT NULL DEFAULT TRUE,
  webhook_auth_mode TEXT NOT NULL DEFAULT 'signature'
    CHECK (webhook_auth_mode IN ('signature', 'ip_allowlist')),
  webhook_allowed_cidrs TEXT[],
  status_check_unauthenticated_webhook BOOLEAN NOT NULL DEFAULT FALSE,
  method_type TEXT NOT NULL DEFAULT 'redirect'
    CHECK (method_type IN ('redirect', 'blocking', 'qr', 'offline', 'bank_transfer', 'card', 'crypto', 'mobile_money')),
  display_name TEXT,
  supported_regions TEXT[],
  min_amount BIGINT,
  max_amount BIGINT,
  deposit_input_schema JSONB,
  withdrawal_input_schema JSONB,
  presentation_schema JSONB,
  deposit_request_method TEXT NOT NULL DEFAULT 'POST',
  deposit_request_path TEXT NOT NULL DEFAULT '/deposits',
  deposit_request_mapping JSONB,
  payout_request_method TEXT NOT NULL DEFAULT 'POST',
  payout_request_path TEXT NOT NULL DEFAULT '/payouts',
  payout_request_mapping JSONB,
  status_request_method TEXT NOT NULL DEFAULT 'GET',
  status_request_path TEXT NOT NULL DEFAULT '/transactions/{transaction_id}',
  status_request_mapping JSONB,
  deposit_response_mapping JSONB NOT NULL CHECK (jsonb_typeof(deposit_response_mapping) = 'object'),
  payout_response_mapping JSONB,
  status_response_mapping JSONB,
  webhook_response_mapping JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  UNIQUE (tenant_id, provider_code)
);

CREATE TABLE deposit_intents (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  intent_reference TEXT NOT NULL CHECK (intent_reference <> '' AND intent_reference = btrim(intent_reference) AND length(intent_reference) <= 128),
  provider_code TEXT NOT NULL CHECK (provider_code <> '' AND provider_code = btrim(provider_code)),
  wallet_id UUID NOT NULL,
  owner_type TEXT NOT NULL CHECK (owner_type IN ('user', 'merchant', 'system', 'psp')),
  owner_id TEXT NOT NULL CHECK (owner_id <> '' AND owner_id = btrim(owner_id)),
  amount BIGINT NOT NULL CHECK (amount > 0),
  currency TEXT NOT NULL CHECK (currency <> '' AND currency = btrim(currency)),
  idempotency_key TEXT NOT NULL CHECK (idempotency_key <> '' AND idempotency_key = btrim(idempotency_key) AND length(idempotency_key) <= 256),
  workflow_id TEXT NOT NULL CHECK (workflow_id <> '' AND workflow_id = btrim(workflow_id) AND length(workflow_id) <= 255),
  run_id TEXT CHECK (run_id IS NULL OR (run_id <> '' AND run_id = btrim(run_id) AND length(run_id) <= 255)),
  metadata JSONB NOT NULL CHECK (jsonb_typeof(metadata) = 'object'),
  region TEXT NOT NULL CHECK (region = btrim(region) AND length(region) <= 128),
  raw_request JSONB NOT NULL CHECK (jsonb_typeof(raw_request) = 'object'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, wallet_id, owner_type, owner_id)
    REFERENCES wallets(tenant_id, id, owner_type, owner_id),
  FOREIGN KEY (tenant_id, provider_code) REFERENCES psp_configs(tenant_id, provider_code),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, intent_reference),
  UNIQUE (tenant_id, provider_code, idempotency_key),
  UNIQUE (tenant_id, workflow_id)
);

CREATE INDEX idx_deposit_intents_wallet
  ON deposit_intents(tenant_id, wallet_id, created_at DESC);

CREATE TABLE psp_transactions (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  psp_provider TEXT NOT NULL,
  psp_transaction_id TEXT,
  idempotency_key TEXT NOT NULL,
  client_reference TEXT NOT NULL,
  direction TEXT NOT NULL CHECK (direction IN ('inbound', 'outbound')),
  amount BIGINT NOT NULL,
  fee_amount BIGINT,
  net_amount BIGINT,
  currency TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('initiated', 'processing', 'pending', 'held', 'failed', 'cancelled', 'success')),
  workflow_id TEXT CHECK (workflow_id IS NULL OR (workflow_id <> '' AND workflow_id = btrim(workflow_id) AND length(workflow_id) <= 255)),
  response_code TEXT,
  response_message TEXT,
  raw_request JSONB,
  raw_response JSONB,
  wallet_id UUID,
  owner_type TEXT CHECK (owner_type IS NULL OR owner_type IN ('user', 'merchant', 'system', 'psp')),
  owner_id TEXT CHECK (owner_id IS NULL OR (owner_id <> '' AND owner_id = btrim(owner_id))),
  withdrawal_destination_id BIGINT,
  allow_return_to_source BOOLEAN,
  approval_timeout_seconds INTEGER CHECK (approval_timeout_seconds > 0),
  decision_deadline_at TIMESTAMPTZ,
  deposit_intent_id BIGINT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  confirmed_at TIMESTAMPTZ,
  last_polled_at TIMESTAMPTZ,
  next_poll_at TIMESTAMPTZ,
  reconciled_at TIMESTAMPTZ,
  retry_count INT NOT NULL DEFAULT 0,
  lock_token TEXT,
  lock_expires_at TIMESTAMPTZ,
  last_error_type TEXT,
  last_error_at TIMESTAMPTZ,
  workflow_signal_payload JSONB,
  workflow_signal_delivered_at TIMESTAMPTZ,
  CHECK (decision_deadline_at IS NULL OR direction = 'outbound'),
  CHECK ((approval_timeout_seconds IS NULL) = (decision_deadline_at IS NULL)),
  CHECK ((direction = 'inbound') = (deposit_intent_id IS NOT NULL)),
  CHECK (
    (direction = 'inbound' AND wallet_id IS NULL AND owner_type IS NULL AND owner_id IS NULL
      AND withdrawal_destination_id IS NULL AND allow_return_to_source IS NULL)
    OR
    (direction = 'outbound' AND wallet_id IS NOT NULL AND owner_type IS NOT NULL AND owner_id IS NOT NULL
      AND allow_return_to_source IS NOT NULL
      AND (allow_return_to_source OR withdrawal_destination_id IS NOT NULL))
  ),
  CHECK (workflow_signal_payload IS NULL OR workflow_id IS NOT NULL),
  CHECK (workflow_signal_payload IS NULL OR status IN ('failed', 'cancelled', 'success')),
  CHECK (workflow_signal_delivered_at IS NULL OR workflow_signal_payload IS NOT NULL),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, deposit_intent_id) REFERENCES deposit_intents(tenant_id, id),
  FOREIGN KEY (tenant_id, wallet_id, owner_type, owner_id)
    REFERENCES wallets(tenant_id, id, owner_type, owner_id),
  FOREIGN KEY (tenant_id, withdrawal_destination_id) REFERENCES withdrawal_destinations(tenant_id, id),
  FOREIGN KEY (tenant_id, psp_provider) REFERENCES psp_configs(tenant_id, provider_code),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, deposit_intent_id),
  UNIQUE (tenant_id, psp_provider, psp_transaction_id),
  UNIQUE (tenant_id, psp_provider, idempotency_key),
  UNIQUE (tenant_id, client_reference)
);

CREATE UNIQUE INDEX uniq_psp_transactions_tenant_workflow
  ON psp_transactions(tenant_id, workflow_id)
  WHERE workflow_id IS NOT NULL;
CREATE INDEX idx_psp_tx_status
  ON psp_transactions(tenant_id, status, created_at);
CREATE INDEX idx_psp_tx_pending_workflow_signal
  ON psp_transactions(tenant_id, created_at)
  WHERE workflow_signal_payload IS NOT NULL AND workflow_signal_delivered_at IS NULL;

CREATE TABLE psp_transaction_amounts (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  psp_transaction_id BIGINT NOT NULL,
  amount_kind TEXT NOT NULL,
  amount BIGINT NOT NULL,
  currency TEXT NOT NULL,
  fx_rate NUMERIC(18, 8),
  fx_base_currency TEXT,
  fx_quote_currency TEXT,
  fx_source TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, psp_transaction_id) REFERENCES psp_transactions(tenant_id, id),
  UNIQUE (tenant_id, psp_transaction_id, amount_kind, currency)
);

CREATE INDEX idx_psp_amounts_tx
  ON psp_transaction_amounts(tenant_id, psp_transaction_id, amount_kind);
CREATE INDEX idx_psp_amounts_currency
  ON psp_transaction_amounts(tenant_id, currency, amount_kind);

CREATE TABLE psp_config_overrides (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  provider_code TEXT NOT NULL,
  region TEXT,
  currency TEXT,
  direction TEXT,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  supports_deposit BOOLEAN NOT NULL DEFAULT TRUE,
  supports_withdrawal BOOLEAN NOT NULL DEFAULT TRUE,
  enabled_currencies TEXT[],
  webhook_auth_mode TEXT
    CHECK (webhook_auth_mode IS NULL OR webhook_auth_mode IN ('signature', 'ip_allowlist')),
  webhook_allowed_cidrs TEXT[],
  status_check_unauthenticated_webhook BOOLEAN,
  method_type TEXT
    CHECK (method_type IS NULL OR method_type IN ('redirect', 'blocking', 'qr', 'offline', 'bank_transfer', 'card', 'crypto', 'mobile_money')),
  display_name TEXT,
  supported_regions TEXT[],
  min_amount BIGINT,
  max_amount BIGINT,
  deposit_input_schema JSONB,
  withdrawal_input_schema JSONB,
  presentation_schema JSONB,
  deposit_request_method TEXT,
  deposit_request_path TEXT,
  deposit_request_mapping JSONB,
  payout_request_method TEXT,
  payout_request_path TEXT,
  payout_request_mapping JSONB,
  status_request_method TEXT,
  status_request_path TEXT,
  status_request_mapping JSONB,
  deposit_response_mapping JSONB,
  payout_response_mapping JSONB,
  status_response_mapping JSONB,
  webhook_response_mapping JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, provider_code) REFERENCES psp_configs(tenant_id, provider_code)
);

CREATE INDEX idx_psp_overrides_scope
  ON psp_config_overrides(tenant_id, provider_code, region, currency, direction);
CREATE INDEX idx_psp_overrides_active
  ON psp_config_overrides(tenant_id, provider_code, is_active);

CREATE TABLE psp_interactions (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  psp_provider TEXT NOT NULL,
  psp_transaction_id TEXT,
  client_reference TEXT,
  direction TEXT CHECK (direction IS NULL OR direction IN ('inbound', 'outbound')),
  interaction_type TEXT NOT NULL,
  idempotency_key TEXT,
  method TEXT,
  url TEXT,
  request_headers JSONB,
  request_body JSONB,
  response_headers JSONB,
  response_body JSONB,
  status_code INT,
  error_message TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (
    (interaction_type IN ('deposit_create', 'payout_send')) = (idempotency_key IS NOT NULL)
  ),
  CHECK (idempotency_key IS NULL OR (idempotency_key <> '' AND idempotency_key = btrim(idempotency_key))),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, psp_provider) REFERENCES psp_configs(tenant_id, provider_code)
);

CREATE INDEX idx_psp_interactions_tx
  ON psp_interactions(tenant_id, psp_provider, psp_transaction_id, created_at DESC);
CREATE INDEX idx_psp_interactions_client_ref
  ON psp_interactions(tenant_id, client_reference, created_at DESC);
CREATE INDEX idx_psp_interactions_type
  ON psp_interactions(tenant_id, interaction_type, created_at DESC);
CREATE UNIQUE INDEX uniq_psp_dispatch_interaction
  ON psp_interactions(tenant_id, psp_provider, interaction_type, idempotency_key)
  WHERE interaction_type IN ('deposit_create', 'payout_send');

-- +goose Down
DROP TABLE psp_interactions;
DROP TABLE psp_config_overrides;
DROP TABLE psp_transaction_amounts;
DROP TABLE psp_transactions;
DROP TABLE deposit_intents;
DROP TABLE psp_configs;
DROP TABLE p2p_commands;
DROP TABLE ledger_withdrawal_destination_links;
DROP TABLE withdrawal_destinations;
DROP TABLE ledger_funding_links;
DROP TABLE funding_source_withdrawal_reservations;
DROP TABLE funding_sources;
DROP TABLE wallet_audit_log;
DROP TABLE workflow_decisions;
DROP TABLE manual_transfer_approvals;
DROP TABLE manual_transfers;
DROP TABLE transaction_limit_reservations;
DROP TABLE transaction_limit_period_usage;
DROP TABLE transaction_limits;
DROP TABLE exchange_rates;
DROP TABLE fee_configs;
DROP TABLE balance_holds;
DROP TABLE ledger_entries;
DROP TABLE ledger_transactions;
DROP TABLE wallets;
DROP TABLE operator_identities;
DROP TABLE tenants;
