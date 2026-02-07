# Wallet System Implementation Plan

## Overview

A tenant-aware, double-entry ledger wallet system with Temporal workflows, PSP integrations, hierarchical RBAC, and HTMX admin panel.

## Architecture Summary

```
┌─────────────────────────────────────────────────────────────────────┐
│                         HTMX Admin Panel                            │
│   (Wallet ops, approvals, manual transfers, audit viewer)           │
└─────────────────────────────────────────────────────────────────────┘
                                    │
┌─────────────────────────────────────────────────────────────────────┐
│                      Fiber HTTP Layer                               │
│   /wallet/*  (user APIs)    /admin/wallet/*  (BO APIs)              │
└─────────────────────────────────────────────────────────────────────┘
                                    │
┌─────────────────────────────────────────────────────────────────────┐
│                    Temporal Workflows                               │
│   Deposit │ Withdrawal │ P2P │ Reconciliation │ Manual Transfer     │
└─────────────────────────────────────────────────────────────────────┘
                                    │
┌────────────┬────────────┬────────────┬────────────┬─────────────────┐
│  Ledger    │    Fee     │   Rate     │   Limits   │    Audit        │
│  Service   │   Engine   │  Service   │  Enforcer  │    Logger       │
└────────────┴────────────┴────────────┴────────────┴─────────────────┘
                                    │
┌─────────────────────────────────────────────────────────────────────┐
│                    PostgreSQL (Double-Entry)                        │
│   wallets │ ledger_entries │ fees │ rates │ limits │ audit_log      │
└─────────────────────────────────────────────────────────────────────┘
```

---

## Phase 1: Foundation (Database + Core Types)

### 1.1 Database Schema

**File: `wallet/migrations/001_wallet_schema.sql`**

```sql
-- Wallets (one per owner per currency per tenant; owner can be user or system account)
CREATE TABLE wallets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id TEXT NOT NULL,
    owner_type TEXT NOT NULL DEFAULT 'user',     -- user, system, merchant, psp
    owner_id TEXT NOT NULL,                      -- user_id as text or system code (treasury, fees, suspense, psp_clearing)
    user_id BIGINT,                              -- convenience for owner_type='user'
    currency TEXT NOT NULL DEFAULT 'USD',
    balance BIGINT NOT NULL DEFAULT 0,           -- Actual balance (minor units: cents)
    available_balance BIGINT NOT NULL DEFAULT 0, -- Balance minus holds
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'frozen', 'closed')),
    wallet_pin_hash TEXT,                        -- Bcrypt hash of wallet PIN
    kyc_tier TEXT NOT NULL DEFAULT 'unverified', -- unverified, basic, full
    version BIGINT NOT NULL DEFAULT 0,           -- Optimistic locking
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (available_balance <= balance),
    UNIQUE(tenant_id, owner_type, owner_id, currency)
);

-- Ledger transactions (double-entry group + idempotency)
CREATE TABLE ledger_transactions (
    id BIGSERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    currency TEXT NOT NULL,
    reference_type TEXT NOT NULL,                -- deposit, withdrawal, p2p, fee, adjustment
    reference_id TEXT,                           -- Workflow ID or external reference
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed', 'reversed')),
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, idempotency_key)
);

-- Double-entry ledger (2+ entries per transaction)
CREATE TABLE ledger_entries (
    id BIGSERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    transaction_id BIGINT NOT NULL REFERENCES ledger_transactions(id),
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    entry_type TEXT NOT NULL CHECK (entry_type IN ('debit', 'credit')),
    amount BIGINT NOT NULL CHECK (amount > 0),
    currency TEXT NOT NULL,
    balance_after BIGINT NOT NULL,               -- Wallet balance after this entry
    wallet_sequence BIGINT NOT NULL,             -- Monotonic per wallet for audit/replay
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed', 'reversed')),
    counter_entry_id BIGINT REFERENCES ledger_entries(id), -- Paired entry (optional)
    description TEXT,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, wallet_id, wallet_sequence)
);

-- Balance holds (for pending transactions)
CREATE TABLE balance_holds (
    id BIGSERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    amount BIGINT NOT NULL,
    amount_remaining BIGINT NOT NULL,
    reason TEXT NOT NULL,
    reference_type TEXT NOT NULL,                -- deposit, withdrawal, p2p, fee
    reference_id TEXT NOT NULL,                  -- Workflow ID
    idempotency_key TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'released', 'expired', 'canceled')),
    expires_at TIMESTAMPTZ NOT NULL,
    released_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    metadata JSONB,
    CHECK (amount_remaining <= amount),
    UNIQUE(tenant_id, wallet_id, reference_type, reference_id)
);

-- Fees configuration
CREATE TABLE fee_configs (
    id SERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    transaction_type TEXT NOT NULL,              -- deposit, withdrawal, p2p
    currency TEXT NOT NULL DEFAULT 'USD',
    tier_min BIGINT NOT NULL DEFAULT 0,          -- Amount tier start (cents)
    tier_max BIGINT,                             -- Amount tier end (NULL = unlimited)
    percentage_fee NUMERIC(8,4) NOT NULL,        -- e.g., 1.5000 = 1.5%
    flat_fee BIGINT NOT NULL DEFAULT 0,          -- Flat fee in cents
    min_fee BIGINT NOT NULL DEFAULT 0,           -- Minimum fee
    max_fee BIGINT,                              -- Maximum fee (NULL = unlimited)
    fee_account_code TEXT,                       -- System wallet code for fee revenue
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, transaction_type, currency, tier_min)
);

-- Exchange rates (manual entry)
CREATE TABLE exchange_rates (
    id SERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    base_currency TEXT NOT NULL,
    quote_currency TEXT NOT NULL,
    buy_rate NUMERIC(18,8) NOT NULL,             -- Rate to buy base with quote
    sell_rate NUMERIC(18,8) NOT NULL,            -- Rate to sell base for quote
    spread NUMERIC(8,4),                         -- Spread percentage
    set_by TEXT NOT NULL,                        -- Admin who set the rate
    effective_from TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    effective_to TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (buy_rate > 0 AND sell_rate > 0),
    UNIQUE(tenant_id, base_currency, quote_currency, effective_from)
);

-- KYC-based transaction limits
CREATE TABLE transaction_limits (
    id SERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    kyc_tier TEXT NOT NULL,
    transaction_type TEXT NOT NULL,
    currency TEXT NOT NULL DEFAULT 'USD',
    daily_limit BIGINT NOT NULL,
    monthly_limit BIGINT NOT NULL,
    per_transaction_limit BIGINT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    UNIQUE(tenant_id, kyc_tier, transaction_type, currency)
);

-- PSP configurations
CREATE TABLE psp_configs (
    id SERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    provider_code TEXT NOT NULL,                 -- coinsbuy, ebs, etc.
    provider_name TEXT NOT NULL,
    api_base_url TEXT NOT NULL,
    enabled_currencies TEXT[],                   -- Optional currency allow-list
    idempotency_header_name TEXT,                -- Provider-specific idempotency header
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    supports_deposit BOOLEAN NOT NULL DEFAULT TRUE,
    supports_withdrawal BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, provider_code)
);

-- PSP transactions (idempotency + reconciliation)
CREATE TABLE psp_transactions (
    id BIGSERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    psp_provider TEXT NOT NULL,
    psp_transaction_id TEXT,                     -- May be NULL until PSP responds
    idempotency_key TEXT NOT NULL,
    client_reference TEXT NOT NULL,              -- Our reference id (workflow/request id). If PSP lacks one, generate internal ref and route to suspense/manual review.
    direction TEXT NOT NULL CHECK (direction IN ('inbound', 'outbound')),
    amount BIGINT NOT NULL,
    fee_amount BIGINT,                           -- PSP fee (if provided)
    net_amount BIGINT,                           -- Amount after PSP fee
    currency TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('initiated','processing','pending','held','failed','cancelled','success')),
    workflow_id TEXT,
    response_code TEXT,
    response_message TEXT,
    raw_request JSONB,
    raw_response JSONB,
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
    UNIQUE(tenant_id, psp_provider, psp_transaction_id),
    UNIQUE(tenant_id, psp_provider, idempotency_key),
    UNIQUE(tenant_id, client_reference)
);

-- Admin roles (hierarchical RBAC)
CREATE TABLE admin_roles (
    id SERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    role_name TEXT NOT NULL,                     -- viewer, operator, supervisor, admin
    role_level INT NOT NULL,                     -- 1=viewer, 2=operator, 3=supervisor, 4=admin
    permissions JSONB NOT NULL,                  -- Array of permission strings
    UNIQUE(tenant_id, role_name)
);

-- Admin users
CREATE TABLE admin_users (
    id BIGSERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    email TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    role_id INT REFERENCES admin_roles(id),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    totp_secret TEXT,                            -- For 2FA
    last_login_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, email)
);

-- Manual transfer requests (with approval workflow)
CREATE TABLE manual_transfers (
    id BIGSERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    workflow_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    transfer_type TEXT NOT NULL,                 -- manual_credit, manual_debit, manual_withdrawal
    wallet_id UUID REFERENCES wallets(id),
    amount BIGINT NOT NULL,
    currency TEXT NOT NULL,
    reason TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'completed')),
    requested_by BIGINT REFERENCES admin_users(id),
    approved_by BIGINT REFERENCES admin_users(id),
    proof_of_payment TEXT,                       -- URL or base64
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

-- Manual transfer approvals (multi-approver history)
CREATE TABLE manual_transfer_approvals (
    id BIGSERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    manual_transfer_id BIGINT NOT NULL REFERENCES manual_transfers(id),
    approver_id BIGINT NOT NULL REFERENCES admin_users(id),
    decision TEXT NOT NULL CHECK (decision IN ('approved', 'rejected')),
    reason TEXT,
    decided_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, manual_transfer_id, approver_id)
);

-- Comprehensive audit log
CREATE TABLE wallet_audit_log (
    id BIGSERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    actor_type TEXT NOT NULL,                    -- user, admin, system, workflow
    actor_id TEXT NOT NULL,
    target_type TEXT,                            -- wallet, transfer, config, etc.
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

-- Funding Sources (AML: track where money comes from)
CREATE TABLE funding_sources (
    id BIGSERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    source_type TEXT NOT NULL,                   -- crypto, card, bank_transfer, p2p_received
    psp_provider TEXT,                           -- coinsbuy, stripe, etc. (NULL for p2p)
    external_reference TEXT,                     -- PSP's reference ID
    verification_status TEXT NOT NULL DEFAULT 'pending', -- pending, verified, rejected
    verified_at TIMESTAMPTZ,
    verified_by TEXT,                            -- admin ID or 'system'
    currency TEXT NOT NULL,
    -- Source-specific details
    source_details JSONB NOT NULL,               -- {card_last4, card_brand, crypto_address, bank_account, etc.}
    -- Aggregate tracking
    total_funded BIGINT NOT NULL DEFAULT 0,      -- Total amount funded from this source
    last_funded_at TIMESTAMPTZ,
    total_withdrawn BIGINT NOT NULL DEFAULT 0,   -- Total amount withdrawn back to this source
    last_withdrawn_at TIMESTAMPTZ,
    -- For return-to-source
    supports_withdrawal BOOLEAN NOT NULL DEFAULT FALSE,
    withdrawal_method JSONB,                     -- How to send back (card token, bank details, etc.)
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, wallet_id, source_type, external_reference)
);

-- Funding source to ledger entry link (which deposits came from which source)
CREATE TABLE ledger_funding_links (
    id BIGSERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    ledger_entry_id BIGINT NOT NULL REFERENCES ledger_entries(id),
    funding_source_id BIGINT NOT NULL REFERENCES funding_sources(id),
    amount BIGINT NOT NULL,                      -- Amount from this source for this entry
    currency TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, ledger_entry_id, funding_source_id)
);

-- Withdrawal Destinations (verified payout methods)
CREATE TABLE withdrawal_destinations (
    id BIGSERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    destination_type TEXT NOT NULL,              -- card, bank_account, crypto_address
    psp_provider TEXT,                           -- Provider that will process payout
    -- Destination details (encrypted where sensitive)
    destination_details JSONB NOT NULL,          -- {card_token, last4, bank_account, etc.}
    display_name TEXT,                           -- "Visa ending 4242"
    currency TEXT NOT NULL,
    country TEXT,
    -- Ownership verification
    ownership_status TEXT NOT NULL DEFAULT 'unverified' CHECK (ownership_status IN ('unverified', 'pending', 'verified', 'rejected')),
    ownership_verification_method TEXT,          -- micro_deposit, card_verification, document
    ownership_verified_at TIMESTAMPTZ,
    ownership_verified_by TEXT,
    ownership_proof JSONB,                       -- {type, reference, attachment_url}
    -- Linked funding source (for return-to-source)
    linked_funding_source_id BIGINT REFERENCES funding_sources(id),
    is_return_to_source BOOLEAN NOT NULL DEFAULT FALSE,
    -- Status
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    last_used_at TIMESTAMPTZ,
    total_withdrawn BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Ownership verification requests
CREATE TABLE ownership_verifications (
    id BIGSERIAL PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    destination_id BIGINT NOT NULL REFERENCES withdrawal_destinations(id),
    verification_type TEXT NOT NULL,             -- micro_deposit, card_charge, document_upload
    status TEXT NOT NULL DEFAULT 'pending',      -- pending, awaiting_confirmation, verified, failed, expired
    -- For micro-deposits
    micro_deposit_amounts BIGINT[],              -- Minor units (e.g., [32, 17] cents)
    micro_deposit_confirmed_at TIMESTAMPTZ,
    -- For card verification
    card_verification_amount BIGINT,             -- Amount charged (refunded after verification)
    -- For document upload
    document_type TEXT,                          -- id_card, bank_statement, utility_bill
    document_url TEXT,
    -- General
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 3,
    expires_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    workflow_id TEXT,
    reference_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes
CREATE INDEX idx_wallets_tenant_user ON wallets(tenant_id, user_id) WHERE owner_type = 'user' AND user_id IS NOT NULL;
CREATE UNIQUE INDEX uniq_wallets_user_currency ON wallets(tenant_id, user_id, currency) WHERE owner_type = 'user' AND user_id IS NOT NULL;
CREATE INDEX idx_wallets_owner ON wallets(tenant_id, owner_type, owner_id);
CREATE INDEX idx_funding_sources_wallet ON funding_sources(tenant_id, wallet_id);
CREATE INDEX idx_funding_sources_psp ON funding_sources(tenant_id, psp_provider, external_reference);
CREATE INDEX idx_funding_links_entry ON ledger_funding_links(tenant_id, ledger_entry_id);
CREATE INDEX idx_funding_links_source ON ledger_funding_links(tenant_id, funding_source_id);
CREATE INDEX idx_destinations_wallet ON withdrawal_destinations(tenant_id, wallet_id);
CREATE INDEX idx_destinations_linked ON withdrawal_destinations(linked_funding_source_id) WHERE linked_funding_source_id IS NOT NULL;
CREATE INDEX idx_ledger_tx_reference ON ledger_transactions(tenant_id, reference_type, reference_id);
CREATE INDEX idx_ledger_tx_status ON ledger_transactions(tenant_id, status, created_at);
CREATE INDEX idx_ledger_entries_wallet ON ledger_entries(tenant_id, wallet_id, wallet_sequence DESC);
CREATE INDEX idx_ledger_entries_tx ON ledger_entries(tenant_id, transaction_id);
CREATE INDEX idx_holds_wallet ON balance_holds(tenant_id, wallet_id) WHERE status = 'active';
CREATE INDEX idx_holds_expiry ON balance_holds(tenant_id, expires_at) WHERE status = 'active';
CREATE INDEX idx_psp_tx_status ON psp_transactions(tenant_id, status, created_at);
CREATE INDEX idx_audit_actor ON wallet_audit_log(tenant_id, actor_type, actor_id, created_at DESC);
CREATE INDEX idx_audit_target ON wallet_audit_log(tenant_id, target_type, target_id, created_at DESC);
CREATE INDEX idx_audit_time ON wallet_audit_log(tenant_id, created_at DESC);
CREATE INDEX idx_manual_status ON manual_transfers(tenant_id, status);
CREATE INDEX idx_manual_approvals ON manual_transfer_approvals(tenant_id, manual_transfer_id);
```

### 1.1.1 Ledger Invariants & System Wallets

- Invariant: every `ledger_transactions` row must balance (sum credits == sum debits) within a single currency.
- System wallets per tenant+currency: `treasury` (customer funds), `fees` (revenue), `suspense` (unmatched), `psp_clearing` (in-flight settlement), `fx_gain_loss` (FX P&L).
- Posting rule: in one DB transaction, `SELECT ... FOR UPDATE` on involved wallets, insert `ledger_transactions`, insert `ledger_entries` with next `wallet_sequence`, update wallet balances/available, commit.
- Holds reduce `available_balance`; expired holds must be released by a scheduler to avoid stale locks.

### 1.2 Money Type (Safe Calculations)

**File: `wallet/money/money.go`**

Use `shopspring/decimal` for all calculations. Store as `BIGINT` (minor units).

```go
type Money struct {
    Amount   int64  // Minor units (cents)
    Currency string // ISO 4217
}

func (m Money) ToMajor() decimal.Decimal {
    scale := ScaleFor(m.Currency)
    return decimal.New(m.Amount, -scale)
}

func FromMajor(amount decimal.Decimal, currency string) Money {
    scale := ScaleFor(currency)
    quantized := amount.Mul(decimal.New(1, scale)).Round(0)
    return Money{
        Amount:   quantized.IntPart(),
        Currency: currency,
    }
}

// ScaleFor returns currency minor unit scale. Default to 2 if unknown.
func ScaleFor(currency string) int32 {
    if scale, ok := currencyScale[currency]; ok {
        return scale
    }
    return 2
}

var currencyScale = map[string]int32{
    "USD": 2,
    "EUR": 2,
    "JPY": 0,
    "KES": 2,
    "GBP": 2,
}
```

---

## Phase 1B: Funding Source & AML Compliance

### 1B.1 Funding Source Tracking

Every deposit creates/updates a funding source record:

```go
// When deposit is confirmed
func (a *Activities) RecordFundingSource(ctx context.Context, params FundingSourceParams) (*FundingSource, error) {
    // Upsert funding source
    source := &FundingSource{
        TenantID:     params.TenantID,
        WalletID:     params.WalletID,
        SourceType:   params.SourceType,    // "crypto", "card", "bank_transfer"
        PSPProvider:  params.PSPProvider,   // "coinsbuy", "stripe"
        ExternalRef:  params.PSPTransactionID,
        SourceDetails: map[string]interface{}{
            "crypto_address": params.CryptoAddress,
            "card_last4":     params.CardLast4,
            "card_brand":     params.CardBrand,
            // etc.
        },
        VerificationStatus: "verified", // PSP verified it
        SupportsWithdrawal: params.CanWithdrawTo,
    }

    // Create link to ledger entry
    link := &LedgerFundingLink{
        LedgerEntryID:   params.LedgerEntryID,
        FundingSourceID: source.ID,
        Amount:          params.Amount,
    }

    return source, nil
}
```

### 1B.2 Return-to-Source Logic

Withdrawals must go back to original funding source when possible (AML requirement):

```go
type WithdrawalDestinationResolver struct {
    store *store.Store
}

func (r *WithdrawalDestinationResolver) Resolve(ctx context.Context,
    tenantID string, walletID uuid.UUID, amount int64) (*WithdrawalDestination, error) {

    // Step 1: Get all funding sources for this wallet ordered by last funded
    sources, _ := r.store.GetFundingSources(ctx, tenantID, walletID)

    // Step 2: Find sources that support withdrawal with remaining balance
    for _, source := range sources {
        if !source.SupportsWithdrawal {
            continue
        }

        // Calculate how much was funded from this source minus already withdrawn
        fundedFromSource := source.TotalFunded
        withdrawnToSource := r.store.GetWithdrawnToSource(ctx, source.ID)
        availableForReturn := fundedFromSource - withdrawnToSource

        if availableForReturn >= amount {
            // Can return to this source
            return &WithdrawalDestination{
                Type:                 source.SourceType,
                PSPProvider:          source.PSPProvider,
                Details:              source.WithdrawalMethod,
                LinkedFundingSource:  source.ID,
                IsReturnToSource:     true,
            }, nil
        }
    }

    // Step 3: No return-to-source available - user must add verified destination
    return nil, ErrNoReturnToSourceAvailable
}
```

### 1B.3 Ownership Verification for New Destinations

When user adds a new withdrawal destination (not return-to-source):

```go
type OwnershipVerificationService struct {
    store *store.Store
}

func (s *OwnershipVerificationService) InitiateVerification(ctx context.Context,
    tenantID string, destination *WithdrawalDestination) (*OwnershipVerification, error) {

    switch destination.DestinationType {
    case "card":
        // Option 1: Micro-charge that appears on statement
        return s.initiateCardVerification(ctx, tenantID, destination)

    case "bank_account":
        // Option 2: Micro-deposits (two small amounts user must confirm)
        return s.initiateMicroDeposits(ctx, tenantID, destination)

    default:
        // Option 3: Document upload (bank statement, ID)
        return s.initiateDocumentVerification(ctx, tenantID, destination)
    }
}

func (s *OwnershipVerificationService) ConfirmMicroDeposits(ctx context.Context,
    verificationID int64, amount1, amount2 int64) error {

    verification, _ := s.store.GetOwnershipVerification(ctx, verificationID)

    // Check if amounts match (order doesn't matter)
    expected := verification.MicroDepositAmounts
    if (amount1 == expected[0] && amount2 == expected[1]) ||
       (amount1 == expected[1] && amount2 == expected[0]) {
        // Verified!
        return s.store.UpdateDestinationOwnership(ctx, verification.DestinationID,
            "verified", "micro_deposit")
    }

    // Wrong amounts
    verification.Attempts++
    if verification.Attempts >= verification.MaxAttempts {
        return ErrVerificationFailed
    }
    return ErrWrongAmounts
}
```

### 1B.4 Updated Withdrawal Workflow

```
Withdrawal Workflow (with AML compliance):
1. User requests withdrawal of X USD
2. Check limits
3. Resolve destination:
   a. Try return-to-source (preferred, no verification needed)
   b. If no return-to-source: require verified destination
   c. If no verified destination: prompt user to add + verify one
4. If new destination needs verification:
   - Start ownership verification workflow
   - Wait for verification signal (with timeout)
5. Once destination verified:
   - If auto-withdrawal: send to PSP
   - If manual: wait for approval + proof of payment
6. Complete withdrawal, link to destination
```

### 1B.5 Reconciliation with Funding Sources

```go
// During reconciliation, match PSP records to funding sources
func (a *Activities) ReconcileFundingSources(ctx context.Context, params ReconcileParams) error {
    // Get PSP deposits for date range
    pspDeposits := fetchPSPDeposits(params.Provider, params.DateRange)

    for _, pspDeposit := range pspDeposits {
        // Find matching funding source
        source, err := a.store.GetFundingSourceByPSPRef(ctx,
            params.TenantID, params.Provider, pspDeposit.TransactionID)

        if err == ErrNotFound {
            // PSP has deposit we don't have - flag for investigation
            a.auditLog.Record(ctx, AuditEvent{
                Type: "reconciliation_missing_internal",
                Metadata: map[string]interface{}{
                    "psp_transaction": pspDeposit,
                },
            })
            continue
        }

        // Verify amounts match
        if source.TotalFunded != pspDeposit.Amount {
            a.auditLog.Record(ctx, AuditEvent{
                Type: "reconciliation_amount_mismatch",
                Metadata: map[string]interface{}{
                    "internal_amount": source.TotalFunded,
                    "psp_amount":      pspDeposit.Amount,
                },
            })
        }
    }

    return nil
}
```

---

## Phase 2: Core Services

### 2.1 Package Structure

```
wallet/
├── migrations/
│   └── 001_wallet_schema.sql
├── money/
│   └── money.go                 # Safe money type
├── store/
│   ├── wallet.go                # Wallet CRUD
│   ├── ledger.go                # Double-entry operations
│   ├── funding.go               # Funding sources + links
│   ├── destinations.go          # Withdrawal destinations
│   ├── ownership.go             # Ownership verifications
│   ├── fees.go                  # Fee config queries
│   ├── rates.go                 # Exchange rate queries
│   ├── limits.go                # Limit enforcement
│   ├── psp.go                   # PSP transaction tracking
│   └── audit.go                 # Audit log writes
├── fees/
│   └── engine.go                # Fee calculation logic
├── rates/
│   └── service.go               # Rate lookup + conversion
├── limits/
│   └── enforcer.go              # Limit checking
├── psp/
│   ├── provider.go              # Provider interface
│   ├── coinsbuy/                # Coinsbuy implementation
│   └── webhook.go               # Webhook verification
├── security/
│   ├── pin.go                   # Wallet PIN hashing/verify
│   └── totp.go                  # 2FA verification
├── workflow/
│   ├── deposit.go
│   ├── withdrawal.go
│   ├── p2p.go
│   ├── manual_transfer.go
│   └── reconciliation.go
├── activity/
│   ├── ledger.go                # Debit/Credit activities
│   ├── psp.go                   # PSP call activities
│   ├── notification.go
│   └── audit.go
├── worker/
│   └── worker.go                # Temporal worker setup
├── handler/
│   ├── user.go                  # User-facing APIs
│   └── admin.go                 # Admin APIs
├── templates/                   # HTMX templates
│   ├── base.html
│   ├── wallets.html
│   ├── transfers.html
│   ├── approvals.html
│   └── audit.html
├── types.go                     # Shared types
├── service.go                   # Main service struct
└── routes.go                    # Route registration
```

### 2.2 Fee Engine

**File: `wallet/fees/engine.go`**

```go
type FeeEngine struct {
    store *store.Store
}

type FeeResult struct {
    TotalFee      int64           // Total fee in minor units
    PercentageFee int64           // Percentage portion
    FlatFee       int64           // Flat portion
    AppliedTier   *FeeConfig      // Which tier was used
}

func (e *FeeEngine) Calculate(ctx context.Context, tenantID string,
    txType string, amount int64) (*FeeResult, error) {

    // Get applicable fee config for amount tier
    config, err := e.store.GetFeeConfigForAmount(ctx, tenantID, txType, amount)
    if err != nil {
        return nil, err
    }

    // Calculate percentage fee
    percentageFee := decimal.NewFromInt(amount).
        Mul(config.PercentageFee).
        Div(decimal.NewFromInt(100)).
        IntPart()

    // Add flat fee
    totalFee := percentageFee + config.FlatFee

    // Apply min/max bounds
    if totalFee < config.MinFee {
        totalFee = config.MinFee
    }
    if config.MaxFee != nil && totalFee > *config.MaxFee {
        totalFee = *config.MaxFee
    }

    return &FeeResult{
        TotalFee:      totalFee,
        PercentageFee: percentageFee,
        FlatFee:       config.FlatFee,
        AppliedTier:   config,
    }, nil
}
```

### 2.3 Rate Service

**File: `wallet/rates/service.go`**

```go
type RateService struct {
    store *store.Store
}

func (s *RateService) Convert(ctx context.Context, tenantID string,
    amount int64, fromCurrency, toCurrency string) (int64, error) {

    if fromCurrency == toCurrency {
        return amount, nil
    }

    rate, err := s.store.GetActiveRate(ctx, tenantID, fromCurrency, toCurrency)
    if err != nil {
        return 0, err
    }

    // Apply sell rate (user selling fromCurrency)
    converted := decimal.NewFromInt(amount).
        Mul(rate.SellRate).
        Round(0).
        IntPart()

    return converted, nil
}
```

### 2.4 Limits Enforcer

**File: `wallet/limits/enforcer.go`**

```go
type LimitEnforcer struct {
    store *store.Store
}

type LimitCheckResult struct {
    Allowed          bool
    DailyUsed        int64
    DailyRemaining   int64
    MonthlyUsed      int64
    MonthlyRemaining int64
    Reason           string
}

func (e *LimitEnforcer) Check(ctx context.Context, tenantID string,
    walletID uuid.UUID, txType string, amount int64) (*LimitCheckResult, error) {

    wallet, err := e.store.GetWallet(ctx, tenantID, walletID)
    if err != nil {
        return nil, err
    }

    limits, err := e.store.GetLimits(ctx, tenantID, wallet.KYCTier, txType)
    if err != nil {
        return nil, err
    }

    // Get daily/monthly usage
    dailyUsed, err := e.store.GetDailyUsage(ctx, tenantID, walletID, txType)
    monthlyUsed, err := e.store.GetMonthlyUsage(ctx, tenantID, walletID, txType)

    result := &LimitCheckResult{
        DailyUsed:        dailyUsed,
        DailyRemaining:   limits.DailyLimit - dailyUsed,
        MonthlyUsed:      monthlyUsed,
        MonthlyRemaining: limits.MonthlyLimit - monthlyUsed,
    }

    // Check per-transaction limit
    if amount > limits.PerTransactionLimit {
        result.Allowed = false
        result.Reason = "exceeds_per_transaction_limit"
        return result, nil
    }

    // Check daily limit
    if dailyUsed+amount > limits.DailyLimit {
        result.Allowed = false
        result.Reason = "exceeds_daily_limit"
        return result, nil
    }

    // Check monthly limit
    if monthlyUsed+amount > limits.MonthlyLimit {
        result.Allowed = false
        result.Reason = "exceeds_monthly_limit"
        return result, nil
    }

    result.Allowed = true
    return result, nil
}
```

---

## Phase 3: Temporal Workflows

### 3.1 Worker Setup

**File: `wallet/worker/worker.go`**

- Task queues: `wallet-main`, `wallet-reconciliation`
- Register all workflows and activities
- Configurable concurrency per queue

### 3.2 Workflows

**Deposit Workflow** (`workflow/deposit.go`):
1. PSPDispatch: Verify PSP transaction (shared steps + provider-specific adapter)
2. Calculate fees
3. Check limits
4. **Record/update funding source** (track origin for AML)
5. Create hold on system float wallet
6. Credit user wallet (double-entry)
7. **Link ledger entry to funding source**
8. Record audit
9. Send notification

**Withdrawal Workflow** (`workflow/withdrawal.go`):
1. Verify PIN + 2FA (if above threshold)
2. Check limits
3. **Resolve destination**:
   - Try return-to-source first (no verification needed)
   - If no return-to-source: require user's verified destination
   - If destination unverified: trigger ownership verification child workflow
4. **If new destination**: Wait for ownership verification signal
5. Calculate fees
6. Create hold (reserve balance)
7. **If manual**: Wait for approval signal (with timeout)
   - On approval: Attach proof, proceed
   - On rejection: Release hold, audit
8. **If auto**: PSPDispatch payout (shared steps + provider-specific adapter)
9. On PSP success: Finalize debit, release hold, update destination usage
10. On PSP failure: Release hold, compensate
11. Record audit (include destination + source tracking)

**P2P Transfer Workflow** (`workflow/p2p.go`):
1. Verify PIN + 2FA
2. Validate sender has sufficient available balance
3. Check both parties' limits
4. Calculate fees
5. Atomic double-entry: Debit sender → Credit receiver
6. Record audit for both
7. Notify both parties

**Manual Transfer Workflow** (`workflow/manual_transfer.go`):
1. Record request in `manual_transfers`
2. Wait for approval signal (role-based)
3. On approval:
   - Require proof of payment attachment
   - Execute credit/debit
   - Record audit with approver info
4. On rejection: Record reason, audit

**Reconciliation Workflow** (`workflow/reconciliation.go`):
- Scheduled daily via Temporal Schedule
- Fetch PSP transactions for date range
- Match against internal ledger
- Flag discrepancies
- Trigger corrective workflows for missing entries

**PSP Status Poller Workflow** (`workflow/psp_status_poller.go`):
- Scheduled (e.g., every 5 minutes)
- Lock + poll `psp_transactions` in non-terminal states
- Call provider status endpoint
- Update PSP state and signal originating workflow

### 3.3 Key Activities

```go
// Ledger activities (idempotent)
- DebitWallet(ctx, DebitParams) → LedgerEntry
- CreditWallet(ctx, CreditParams) → LedgerEntry
- CreateHold(ctx, HoldParams) → Hold
- ReleaseHold(ctx, holdID) → error
- ExecuteDoubleEntry(ctx, DoubleEntryParams) → DoubleEntryResult

// Funding source activities (AML)
- RecordFundingSource(ctx, FundingSourceParams) → FundingSource
- LinkLedgerToFundingSource(ctx, LinkParams) → LedgerFundingLink
- ResolveWithdrawalDestination(ctx, ResolveParams) → WithdrawalDestination
- GetReturnToSourceOptions(ctx, WalletID) → []FundingSource

// Ownership verification activities
- InitiateOwnershipVerification(ctx, DestinationID) → OwnershipVerification
- SendMicroDeposits(ctx, BankAccount) → MicroDepositResult
- ConfirmMicroDeposits(ctx, VerificationID, amounts) → bool
- VerifyCardOwnership(ctx, CardToken) → bool

// PSP activities
- VerifyPSPDeposit(ctx, VerifyParams) → VerificationResult
- SendWithdrawalToPSP(ctx, WithdrawalParams) → PSPResult
- PollPSPStatus(ctx, PollParams) → StatusResult

// PSP routing (Temporal activity registry)
- PSPDispatch(ctx, PSPDispatchParams) → PSPResult
  - Shared steps: load tenant config, FX rates, limits, PSP config, SOPS secrets
  - Provider-specific steps: call provider adapter (coinsbuy, etc.), normalize response, persist
  - Error mapping: convert provider errors into standardized PSP error types

// Security activities
- VerifyWalletPIN(ctx, walletID, pin) → bool
- Verify2FA(ctx, userID, code) → bool

// Audit activities
- RecordAuditEvent(ctx, AuditEvent) → error
```

---

## Phase 4: Security

### 4.1 Wallet PIN

- Bcrypt hash stored in `wallets.wallet_pin_hash`
- Required for: P2P transfers, withdrawals
- Set during wallet activation
- Reset flow via admin or verified email/SMS

### 4.2 2FA (TOTP)

- Using `pquerna/otp/totp` (already in codebase)
- Required when: amount > configurable threshold
- Stored in `admin_users.totp_secret` (admin) / separate user 2FA table

### 4.3 Tiered Security

| Amount Range | Security Required |
|--------------|-------------------|
| 0 - 100 USD  | PIN only |
| 100 - 1000 USD | PIN + 2FA |
| > 1000 USD   | PIN + 2FA + Manual approval |

Thresholds are config-driven per tenant.

---

## Phase 5: RBAC

### 5.1 Role Hierarchy

| Role | Level | Permissions |
|------|-------|-------------|
| viewer | 1 | View wallets, transactions, audit logs |
| operator | 2 | + Approve small transfers (< threshold) |
| supervisor | 3 | + Approve large transfers, view all tenants |
| admin | 4 | + Manage configs, users, roles |

### 5.2 Permission Checking

```go
type Permission string
const (
    PermViewWallets      Permission = "wallet:view"
    PermApproveSmall     Permission = "transfer:approve:small"
    PermApproveLarge     Permission = "transfer:approve:large"
    PermManageConfig     Permission = "config:manage"
    PermManageUsers      Permission = "users:manage"
    PermViewAudit        Permission = "audit:view"
    PermManualCredit     Permission = "wallet:manual_credit"
    PermManualDebit      Permission = "wallet:manual_debit"
)

func (r *Role) HasPermission(p Permission) bool {
    // Check role level and explicit permissions
}
```

---

## Phase 6: HTMX Admin Panel

### 6.1 Routes

```
GET  /admin/wallet                      # Dashboard
GET  /admin/wallet/wallets              # List wallets (HTMX partial)
GET  /admin/wallet/wallets/:id          # Wallet detail
GET  /admin/wallet/transactions         # Transaction list
GET  /admin/wallet/pending              # Pending approvals
POST /admin/wallet/approve/:workflow_id # Approve transfer
POST /admin/wallet/reject/:workflow_id  # Reject transfer
GET  /admin/wallet/manual               # Manual transfer form
POST /admin/wallet/manual               # Submit manual transfer
GET  /admin/wallet/audit                # Audit log viewer
GET  /admin/wallet/rates                # Rate management
POST /admin/wallet/rates                # Set new rate
GET  /admin/wallet/fees                 # Fee config
POST /admin/wallet/fees                 # Update fee config
```

### 6.2 HTMX Patterns

- `hx-get` for partial page loads
- `hx-post` for form submissions
- `hx-target` for targeted updates
- `hx-trigger="revealed"` for infinite scroll on audit logs
- `hx-vals` for CSRF tokens
- Server-Sent Events for real-time approval notifications

### 6.3 Response Format

HTMX endpoints return HTML fragments only (no JSON/HATEOAS).

---

## Phase 7: PSP Abstraction

**Secrets source**: Provider credentials + webhook verification secrets are stored in SOPS, not in the database. The loader derives the SOPS path from `tenant_id` + `provider_code` and merges DB config + SOPS secrets at runtime.

### 7.1 Provider Interface

**File: `wallet/psp/provider.go`**

```go
type Provider interface {
    // Verify incoming deposit
    VerifyDeposit(ctx context.Context, txID string) (*DepositVerification, error)

    // Send outgoing withdrawal/payout
    SendPayout(ctx context.Context, req PayoutRequest) (*PayoutResult, error)

    // Poll for transaction status
    GetTransactionStatus(ctx context.Context, txID string) (*TxStatus, error)

    // Verify webhook signature
    VerifyWebhook(payload []byte, signature string) bool

    // Provider info
    Code() string
    SupportedOperations() []Operation
}
```

### 7.2 Implementation Pattern

```go
// wallet/psp/coinsbuy/provider.go
type CoinsbuyProvider struct {
    config   *PSPConfig
    client   *http.Client
}

func (p *CoinsbuyProvider) VerifyDeposit(ctx context.Context, txID string) (*DepositVerification, error) {
    // Call Coinsbuy API
    // Map response to internal types
}
```

### 7.3 Config + Secret Loader (Mapper)

Runtime loader merges DB config with SOPS secrets:

```
LoadPSP(ctx, tenantID, providerCode):
  cfg := psp_configs where tenant_id + provider_code
  cached := cache.Get(tenantID, providerCode)
  if cached != nil && !cached.Expired():
      return cached.Value
  secrets := sops["noebs.psp."+tenantID+"."+providerCode]
  merged := Merge(cfg, secrets)  // normalized PSPConfig used by Provider
  cache.Set(tenantID, providerCode, merged, ttl=5m)  // TTL configurable; invalidate on SOPS rotation/config change
  return merged
```

### 7.4 Webhook Callback Routing

1. Webhook payload contains `client_reference` (our reference id). If missing, create internal reference and route to `suspense`/manual review.
2. Lookup `psp_transactions` by `client_reference` to obtain `psp_provider` and tenant.
3. Load PSP config via `LoadPSP` (DB + SOPS).
4. Verify webhook signature using provider secrets.
5. Dispatch to provider‑specific handler; normalize response; update `psp_transactions`.
6. Signal or start Temporal workflow using `client_reference`.

### 7.5 Standardized PSP Errors

All PSP activities return typed errors for consistent handling:

- `ErrPSPNotRegistered` (no provider registered for tenant + provider_code)
- `ErrPSPSecretMissing` (SOPS secrets missing or unreadable)
- `ErrPSPConfigInvalid` (missing required config fields)
- `ErrPSPWebhookInvalid` (signature/nonce/timestamp invalid)
- `ErrPSPTemporary` (retryable transport/5xx)
- `ErrPSPPermanent` (non-retryable 4xx business errors)

### 7.6 PSP Transaction State Machine + Retry Poller

**State definitions**
- `initiated` → request created internally, PSP not yet contacted
- `processing` → PSP request sent, awaiting result
- `pending` → PSP accepted but settlement/confirmation pending (non-terminal)
- `held` → requires manual review/AML/ops action (non-terminal but blocked)
- `success` → PSP confirmed success (terminal)
- `failed` → PSP confirmed failure (terminal)
- `cancelled` → cancelled by user/admin (terminal)

**State transitions (rules)**
- `initiated` → `processing` when PSPDispatch starts
- `processing` → `success` on provider success response
- `processing` → `pending` on provider "pending/queued" response
- `processing` → `failed` on provider permanent error (e.g., 4xx)
- `processing` → `held` on AML/manual review required
- `pending` → `success` or `failed` via status poll or callback
- `held` → `success` or `failed` via manual resolution + PSP update
- `pending`/`processing` → `cancelled` if user/admin cancels before completion

**Error-to-state mapping**
- `ErrPSPTemporary` → keep `pending` (schedule retry; backoff)
- `ErrPSPPermanent` → `failed`
- `ErrPSPWebhookInvalid` → no state change; record error, request manual review if repeated
- `ErrPSPNotRegistered` / `ErrPSPSecretMissing` / `ErrPSPConfigInvalid` → `held` (ops intervention)

**Retry Poller (Temporal schedule)**
- Cron workflow scans `psp_transactions` where `status IN ('initiated','processing','pending')`
  and `next_poll_at <= now()` and lock is free.
- Lock acquisition uses a lease (`lock_token`, `lock_expires_at`) or `SELECT ... FOR UPDATE SKIP LOCKED`
  to ensure only one worker handles a transaction at a time.
- Poller calls provider `GetTransactionStatus`, updates `status`, schedules `next_poll_at`, increments `retry_count`.
- If `success`: signal the originating workflow (via `client_reference`) to finalize ledger posting.
- If `failed/cancelled`: signal workflow to compensate/release holds.

**Concurrency + idempotency**
- Webhook and poller can race; both must use the same lock/lease to update `psp_transactions`.
- Ledger posting must be idempotent using `ledger_transactions.idempotency_key = client_reference`
  so repeated success events cannot double‑credit.

---

## Phase 8: Configuration

### 8.1 New Config Fields

Add to `ebs_fields.NoebsConfig`:

```go
// Temporal
TemporalEnabled     bool   `json:"temporal_enabled"`
TemporalHost        string `json:"temporal_host"`
TemporalPort        string `json:"temporal_port"`
TemporalNamespace   string `json:"temporal_namespace"`

// Wallet
WalletEnabled              bool  `json:"wallet_enabled"`
WalletPINRequired          bool  `json:"wallet_pin_required"`
Wallet2FAThreshold         int64 `json:"wallet_2fa_threshold"`        // Amount in cents
WalletApprovalThreshold    int64 `json:"wallet_approval_threshold"`   // Amount requiring manual approval
WalletDefaultCurrency      string `json:"wallet_default_currency"`
```

### 8.2 Secrets (SOPS)

Add to `secrets.yaml`:
```yaml
noebs:
  temporal_namespace: "default"
  psp:
    default_tenant:
      coinsbuy:
        api_key: "..."
        api_secret: "..."
        webhook_secret: "..."
        webhook_public_key: "..." # if provider uses asymmetric verification
```

---

## Agent Execution Plan (Parallel Workstreams)

### Agent A: Ledger + Database Core
- Owns migrations, ledger invariants, system wallets, holds, and wallet CRUD.
- Delivers: `wallets`, `ledger_transactions`, `ledger_entries`, `balance_holds`, indexes, and posting rules.
- Hand‑off: stable DB contract + store APIs for other agents.

### Agent B: PSP + Temporal Orchestration
- Owns PSP registry, loader (DB + SOPS), PSPDispatch activity, webhook routing, status poller workflow, and reconciliation.
- Delivers: provider interface, typed PSP errors, state machine + transitions, locking strategy, poller schedule.
- Hand‑off: stable PSPDispatch + webhook contract for workflows.

### Agent C: Workflows (Deposit/Withdraw/P2P/Manual)
- Owns Temporal workflows and activity wiring using the stable ledger + PSPDispatch APIs.
- Delivers: deterministic workflows, idempotency, signal handling, compensation paths.
- Hand‑off: workflow interfaces for handlers + admin panel.

### Agent D: Security + RBAC
- Owns PIN/2FA, admin RBAC tables/middleware, approval rules, and audit fields.
- Delivers: permission checks, manual transfer approval rules, wallet PIN flows.
- Hand‑off: auth/authorization hooks for handlers + UI.

### Agent E: Admin + User UI (HTMX)
- Owns admin routes, templates, wallet views, approvals, audit viewer, and configuration UI.
- Delivers: HTML‑only HTMX flows, SSE for approvals, no JSON/HATEOAS.
- Hand‑off: UI endpoints for ops usage.

### Agent F: Testing + QA Harness
- Owns unit/integration tests, PSP scenario matrix, and Testcontainers setup.
- Delivers: deterministic PSP mocks, webhook replay tests, poller race tests, workflow replay tests.
- Hand‑off: CI‑ready test suite and scenario coverage report.

### Coordination Rules
- Agent A ships DB/store contracts first; all others build on those interfaces.
- Agent B + C sync on PSPDispatch inputs/outputs and error types before workflow implementation.
- Agent F pairs with each agent to encode regressions as tests during development, not after.
- Each agent publishes a short “contract” doc (tables, APIs, workflows, or routes) for cross‑agent integration.

---

## Critical Files to Modify

| File | Change |
|------|--------|
| `cli/config.go` | Register wallet routes, start Temporal worker |
| `ebs_fields/fields.go` | Add wallet config fields |
| `config.yaml` | Add wallet configuration |
| `secrets.yaml` | Add PSP credentials |
| `wallet/psp/loader.go` | Merge DB config + SOPS secrets into PSPConfig |
| `wallet/psp/registry.go` | PSP provider registry used by Temporal PSPDispatch |
| `docker-compose.yaml` | Add Temporal services |

---

## Verification Plan

### Unit Tests
- Money calculations (edge cases, overflow)
- Fee engine (tier boundaries)
- Limit enforcer (daily rollover)
- Double-entry balance consistency
- Return-to-source resolver logic
- Ownership verification state machine

### Integration Tests
- Temporal workflow replay tests
- PSP mock integration
- PSP scenario matrix (success, pending, timeout, reversal, duplicate webhook, malformed payload)
- End-to-end deposit → P2P → withdrawal flow
- Funding source tracking through deposit flow
- Ownership verification workflow completion
- Webhook + poller race (lock prevents double credit)

### Testcontainers
- Postgres + Temporal + fake PSP server in CI for deterministic end-to-end tests
- Webhook replay + idempotency verification under concurrent delivery

### Manual Testing
1. Create wallet via user API
2. Simulate PSP webhook deposit (crypto via Coinsbuy)
3. Verify balance credited AND funding source recorded
4. Execute P2P transfer
5. Request withdrawal:
   a. First withdrawal → should use return-to-source (Coinsbuy)
   b. Second withdrawal (exceeds original deposit) → prompt for new destination
6. Add new card as withdrawal destination
7. Complete ownership verification (micro-charge)
8. Complete withdrawal to new card
9. Approve via admin panel (for manual flow)
10. Verify audit trail shows full provenance (source → wallet → destination)

### Temporal Testing
- Use Temporal test framework for workflow determinism
- Test compensation paths (PSP failures)
- Test signal handling (approval timeout)
- Test ownership verification timeout and retry
- Test return-to-source fallback logic
- Test PSP status poller retries + state transitions

### AML Compliance Testing
- Verify all deposits link to funding sources
- Verify return-to-source prioritization works
- Verify new destinations require ownership proof
- Verify expired card scenario (user adds new card, must verify)
- Verify reconciliation catches unlinked transactions
