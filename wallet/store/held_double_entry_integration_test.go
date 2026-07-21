package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/testdb"
	basestore "github.com/adonese/noebs/store"
	"github.com/google/uuid"
)

func TestLedgerAccountingForHeldAndSystemDebits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	container, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatalf("start postgres container: %v", err)
	}
	defer func() {
		_ = container.Terminate(context.Background())
	}()

	const dbName = "wallet_ledger"
	dbURL, err := container.CreateDatabaseForRole(ctx, dbName, "wallet_ledger_migrate")
	if err != nil {
		t.Fatalf("create database: %v", err)
	}

	db, err := basestore.OpenFromConfig(dbURL, basestore.DriverPostgres)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() {
		_ = db.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer dropCancel()
		_ = container.DropDatabase(dropCtx, dbName)
	}()

	const tenantID = "tenant-a"
	if err := basestore.MigrateScope(ctx, db, basestore.MigrationScopeWalletLedger); err != nil {
		t.Fatalf("migrate wallet ledger: %v", err)
	}
	provisionWalletStoreTestTenant(t, ctx, db, tenantID, "Held Entry Tenant")

	store := New(db)
	aedUnitID := testCurrencyUnitID(t, ctx, store, "AED")
	userWallet, err := store.EnsureWallet(ctx, EnsureWalletParams{
		TenantID:       tenantID,
		OwnerType:      OwnerTypeUser,
		OwnerID:        "user-1",
		UserID:         1,
		Currency:       "AED",
		CurrencyUnitID: aedUnitID,
		KYCTier:        KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure user wallet: %v", err)
	}
	treasuryWallet, err := store.EnsureWallet(ctx, EnsureWalletParams{
		TenantID:       tenantID,
		OwnerType:      OwnerTypeSystem,
		OwnerID:        SystemTreasury,
		Currency:       "AED",
		CurrencyUnitID: aedUnitID,
		KYCTier:        KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure treasury wallet: %v", err)
	}
	feesWallet, err := store.EnsureWallet(ctx, EnsureWalletParams{
		TenantID:       tenantID,
		OwnerType:      OwnerTypeSystem,
		OwnerID:        SystemFees,
		Currency:       "AED",
		CurrencyUnitID: aedUnitID,
		KYCTier:        KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure fees wallet: %v", err)
	}
	setWalletBalances(t, ctx, db, tenantID, userWallet.ID, 1000, 1000)

	holdParams := HoldParams{
		TenantID:       tenantID,
		WalletID:       userWallet.ID,
		Amount:         1000,
		Reason:         "withdrawal",
		ReferenceType:  "withdrawal",
		ReferenceID:    "withdrawal-ref",
		IdempotencyKey: "withdrawal-ref:hold",
		ExpiresAt:      time.Now().UTC().Add(time.Hour),
		Metadata:       json.RawMessage(`{"purpose":"withdrawal","sequence":1}`),
	}
	hold, err := store.CreateHold(ctx, holdParams)
	if err != nil {
		t.Fatalf("create hold: %v", err)
	}
	assertWalletBalances(t, ctx, store, tenantID, userWallet.ID, 1000, 0)

	replayedHold, err := store.CreateHold(ctx, holdParams)
	if err != nil {
		t.Fatalf("idempotent create hold replay: %v", err)
	}
	if replayedHold.ID != hold.ID {
		t.Fatalf("idempotent hold id = %d, want %d", replayedHold.ID, hold.ID)
	}
	assertWalletBalances(t, ctx, store, tenantID, userWallet.ID, 1000, 0)

	amountMismatchHold := holdParams
	amountMismatchHold.Amount = 900
	_, err = store.CreateHold(ctx, amountMismatchHold)
	if !errors.Is(err, ErrDuplicateHold) {
		t.Fatalf("idempotent hold amount mismatch error = %v, want %v", err, ErrDuplicateHold)
	}
	expiryMismatchHold := holdParams
	expiryMismatchHold.ExpiresAt = holdParams.ExpiresAt.Add(time.Minute)
	_, err = store.CreateHold(ctx, expiryMismatchHold)
	if !errors.Is(err, ErrDuplicateHold) {
		t.Fatalf("idempotent hold expiry mismatch error = %v, want %v", err, ErrDuplicateHold)
	}
	metadataMismatchHold := holdParams
	metadataMismatchHold.Metadata = json.RawMessage(`{"purpose":"withdrawal","sequence":2}`)
	_, err = store.CreateHold(ctx, metadataMismatchHold)
	if !errors.Is(err, ErrDuplicateHold) {
		t.Fatalf("idempotent hold metadata mismatch error = %v, want %v", err, ErrDuplicateHold)
	}

	_, err = store.PostDoubleEntry(ctx, DoubleEntryParams{
		TenantID:       tenantID,
		IdempotencyKey: "normal-debit",
		Currency:       "AED",
		ReferenceType:  "withdrawal",
		ReferenceID:    "normal-debit",
		DebitWalletID:  userWallet.ID,
		CreditWalletID: treasuryWallet.ID,
		Amount:         900,
		Description:    "normal debit should not spend reserved funds",
	})
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("normal PostDoubleEntry() error = %v, want %v", err, ErrInsufficientFunds)
	}

	withdrawalEntry := DoubleEntryParams{
		TenantID:       tenantID,
		IdempotencyKey: "withdrawal-ref:withdrawal",
		Currency:       "AED",
		ReferenceType:  "withdrawal",
		ReferenceID:    "withdrawal-ref",
		DebitWalletID:  userWallet.ID,
		CreditWalletID: treasuryWallet.ID,
		Amount:         900,
		Description:    "withdrawal",
		Metadata:       json.RawMessage(`{"purpose":"withdrawal","sequence":1}`),
	}
	if _, err := store.PostHeldDoubleEntry(ctx, HeldDoubleEntryParams{HoldID: hold.ID, Entry: withdrawalEntry}); err != nil {
		t.Fatalf("post held withdrawal entry: %v", err)
	}
	assertWalletBalances(t, ctx, store, tenantID, userWallet.ID, 100, 0)
	assertWalletBalances(t, ctx, store, tenantID, treasuryWallet.ID, 900, 900)
	assertHold(t, ctx, db, tenantID, hold.ID, HoldStatusActive, 100, false)

	descriptionMismatchEntry := withdrawalEntry
	descriptionMismatchEntry.Description = "other withdrawal"
	_, err = store.PostHeldDoubleEntry(ctx, HeldDoubleEntryParams{HoldID: hold.ID, Entry: descriptionMismatchEntry})
	if !errors.Is(err, ErrDuplicateTransaction) {
		t.Fatalf("idempotent entry description mismatch error = %v, want %v", err, ErrDuplicateTransaction)
	}
	metadataMismatchEntry := withdrawalEntry
	metadataMismatchEntry.Metadata = json.RawMessage(`{"purpose":"withdrawal","sequence":2}`)
	_, err = store.PostHeldDoubleEntry(ctx, HeldDoubleEntryParams{HoldID: hold.ID, Entry: metadataMismatchEntry})
	if !errors.Is(err, ErrDuplicateTransaction) {
		t.Fatalf("idempotent entry metadata mismatch error = %v, want %v", err, ErrDuplicateTransaction)
	}

	feeEntry := DoubleEntryParams{
		TenantID:       tenantID,
		IdempotencyKey: "withdrawal-ref:fee",
		Currency:       "AED",
		ReferenceType:  "fee",
		ReferenceID:    "withdrawal-ref",
		DebitWalletID:  userWallet.ID,
		CreditWalletID: feesWallet.ID,
		Amount:         100,
		Description:    "withdrawal fee",
	}
	if _, err := store.PostHeldDoubleEntry(ctx, HeldDoubleEntryParams{HoldID: hold.ID, Entry: feeEntry}); err != nil {
		t.Fatalf("post held fee entry: %v", err)
	}
	assertWalletBalances(t, ctx, store, tenantID, userWallet.ID, 0, 0)
	assertWalletBalances(t, ctx, store, tenantID, feesWallet.ID, 100, 100)
	assertHold(t, ctx, db, tenantID, hold.ID, HoldStatusCaptured, 0, true)

	if _, err := store.PostHeldDoubleEntry(ctx, HeldDoubleEntryParams{HoldID: hold.ID, Entry: withdrawalEntry}); err != nil {
		t.Fatalf("idempotent held withdrawal entry: %v", err)
	}
	assertWalletBalances(t, ctx, store, tenantID, userWallet.ID, 0, 0)
	assertWalletBalances(t, ctx, store, tenantID, treasuryWallet.ID, 900, 900)
	assertHold(t, ctx, db, tenantID, hold.ID, HoldStatusCaptured, 0, true)

	if err := store.ReleaseHold(ctx, tenantID, hold.ID); err != nil {
		t.Fatalf("release captured hold: %v", err)
	}
	assertWalletBalances(t, ctx, store, tenantID, userWallet.ID, 0, 0)
	assertHold(t, ctx, db, tenantID, hold.ID, HoldStatusCaptured, 0, true)

	clearingWallet, err := store.EnsureWallet(ctx, EnsureWalletParams{
		TenantID:       tenantID,
		OwnerType:      OwnerTypeSystem,
		OwnerID:        SystemPSPClearing,
		Currency:       "AED",
		CurrencyUnitID: aedUnitID,
		KYCTier:        KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure psp clearing wallet: %v", err)
	}
	receiverWallet, err := store.EnsureWallet(ctx, EnsureWalletParams{
		TenantID:       tenantID,
		OwnerType:      OwnerTypeUser,
		OwnerID:        "user-2",
		UserID:         2,
		Currency:       "AED",
		CurrencyUnitID: aedUnitID,
		KYCTier:        KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure receiver wallet: %v", err)
	}

	systemCreditEntry := DoubleEntryParams{
		TenantID:       tenantID,
		IdempotencyKey: "deposit-ref:deposit",
		Currency:       "AED",
		ReferenceType:  "deposit",
		ReferenceID:    "deposit-ref",
		DebitWalletID:  clearingWallet.ID,
		CreditWalletID: receiverWallet.ID,
		Amount:         500,
		Description:    "deposit",
	}
	_, err = store.PostDoubleEntry(ctx, systemCreditEntry)
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("normal system debit error = %v, want %v", err, ErrInsufficientFunds)
	}
	if _, err := store.PostSystemDebitDoubleEntry(ctx, systemCreditEntry); err != nil {
		t.Fatalf("post system debit entry: %v", err)
	}
	assertWalletBalances(t, ctx, store, tenantID, clearingWallet.ID, -500, -500)
	assertWalletBalances(t, ctx, store, tenantID, receiverWallet.ID, 500, 500)

	replayed, err := store.PostSystemDebitDoubleEntry(ctx, systemCreditEntry)
	if err != nil {
		t.Fatalf("idempotent system debit replay: %v", err)
	}
	if !replayed.Existing {
		t.Fatal("idempotent system debit replay Existing = false, want true")
	}
	assertWalletBalances(t, ctx, store, tenantID, clearingWallet.ID, -500, -500)
	assertWalletBalances(t, ctx, store, tenantID, receiverWallet.ID, 500, 500)

	amountMismatch := systemCreditEntry
	amountMismatch.Amount = 600
	_, err = store.PostSystemDebitDoubleEntry(ctx, amountMismatch)
	if !errors.Is(err, ErrDuplicateTransaction) {
		t.Fatalf("idempotent amount mismatch error = %v, want %v", err, ErrDuplicateTransaction)
	}

	userDebitEntry := systemCreditEntry
	userDebitEntry.IdempotencyKey = "not-system-debit"
	userDebitEntry.DebitWalletID = receiverWallet.ID
	userDebitEntry.CreditWalletID = feesWallet.ID
	userDebitEntry.ReferenceID = "not-system-debit"
	_, err = store.PostSystemDebitDoubleEntry(ctx, userDebitEntry)
	if !errors.Is(err, ErrSystemDebitWalletRequired) {
		t.Fatalf("user system-debit error = %v, want %v", err, ErrSystemDebitWalletRequired)
	}
}

func TestCreateHoldInsufficientFundsRollsBack(t *testing.T) {
	ctx, store, tenantID := newWalletStoreIntegration(t)

	wallet, err := store.EnsureWallet(ctx, EnsureWalletParams{
		TenantID:       tenantID,
		OwnerType:      OwnerTypeUser,
		OwnerID:        "user-hold-rollback",
		UserID:         42,
		Currency:       "AED",
		CurrencyUnitID: testCurrencyUnitID(t, ctx, store, "AED"),
		KYCTier:        KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure wallet: %v", err)
	}
	setWalletBalances(t, ctx, store.DB, tenantID, wallet.ID, 100, 100)

	_, err = store.CreateHold(ctx, HoldParams{
		TenantID:       tenantID,
		WalletID:       wallet.ID,
		Amount:         200,
		Reason:         "withdrawal",
		ReferenceType:  "withdrawal",
		ReferenceID:    "insufficient-hold",
		IdempotencyKey: "insufficient-hold",
		ExpiresAt:      time.Now().UTC().Add(time.Hour),
	})
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("CreateHold() error = %v, want %v", err, ErrInsufficientFunds)
	}
	assertWalletBalances(t, ctx, store, tenantID, wallet.ID, 100, 100)

	var count int
	stmt := store.DB.Rebind("SELECT COUNT(*) FROM balance_holds WHERE tenant_id = ? AND reference_id = ?")
	if err := store.DB.GetContext(ctx, &count, stmt, tenantID, "insufficient-hold"); err != nil {
		t.Fatalf("count holds: %v", err)
	}
	if count != 0 {
		t.Fatalf("hold rows after insufficient funds = %d, want 0", count)
	}
}

func TestBalanceMutationsRejectInactiveWallets(t *testing.T) {
	ctx, store, tenantID := newWalletStoreIntegration(t)
	aedUnitID := testCurrencyUnitID(t, ctx, store, "AED")

	sourceWallet, err := store.EnsureWallet(ctx, EnsureWalletParams{
		TenantID:       tenantID,
		OwnerType:      OwnerTypeUser,
		OwnerID:        "inactive-source",
		UserID:         51,
		Currency:       "AED",
		CurrencyUnitID: aedUnitID,
		KYCTier:        KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure source wallet: %v", err)
	}
	receiverWallet, err := store.EnsureWallet(ctx, EnsureWalletParams{
		TenantID:       tenantID,
		OwnerType:      OwnerTypeUser,
		OwnerID:        "inactive-receiver",
		UserID:         52,
		Currency:       "AED",
		CurrencyUnitID: aedUnitID,
		KYCTier:        KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure receiver wallet: %v", err)
	}
	clearingWallet, err := store.EnsureWallet(ctx, EnsureWalletParams{
		TenantID:       tenantID,
		OwnerType:      OwnerTypeSystem,
		OwnerID:        SystemPSPClearing,
		Currency:       "AED",
		CurrencyUnitID: aedUnitID,
		KYCTier:        KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure clearing wallet: %v", err)
	}
	setWalletBalances(t, ctx, store.DB, tenantID, sourceWallet.ID, 1000, 1000)
	setWalletStatus(t, ctx, store.DB, tenantID, sourceWallet.ID, WalletStatusFrozen)

	_, err = store.CreateHold(ctx, HoldParams{
		TenantID:       tenantID,
		WalletID:       sourceWallet.ID,
		Amount:         100,
		Reason:         "inactive",
		ReferenceType:  "withdrawal",
		ReferenceID:    "inactive-hold",
		IdempotencyKey: "inactive-hold",
		ExpiresAt:      time.Now().UTC().Add(time.Hour),
	})
	if !errors.Is(err, ErrWalletInactive) {
		t.Fatalf("CreateHold(inactive wallet) error = %v, want %v", err, ErrWalletInactive)
	}
	assertWalletBalances(t, ctx, store, tenantID, sourceWallet.ID, 1000, 1000)
	assertHoldReferenceCount(t, ctx, store.DB, tenantID, "inactive-hold", 0)

	_, err = store.PostDoubleEntry(ctx, DoubleEntryParams{
		TenantID:       tenantID,
		IdempotencyKey: "inactive-debit",
		Currency:       "AED",
		ReferenceType:  "p2p",
		ReferenceID:    "inactive-debit",
		DebitWalletID:  sourceWallet.ID,
		CreditWalletID: receiverWallet.ID,
		Amount:         100,
	})
	if !errors.Is(err, ErrWalletInactive) {
		t.Fatalf("PostDoubleEntry(inactive debit) error = %v, want %v", err, ErrWalletInactive)
	}
	exists, err := store.LedgerTransactionExists(ctx, tenantID, "inactive-debit")
	if err != nil {
		t.Fatalf("check inactive debit ledger transaction: %v", err)
	}
	if exists {
		t.Fatal("inactive debit ledger transaction persisted")
	}
	assertWalletBalances(t, ctx, store, tenantID, sourceWallet.ID, 1000, 1000)
	assertWalletBalances(t, ctx, store, tenantID, receiverWallet.ID, 0, 0)

	setWalletStatus(t, ctx, store.DB, tenantID, sourceWallet.ID, WalletStatusActive)
	setWalletStatus(t, ctx, store.DB, tenantID, receiverWallet.ID, WalletStatusClosed)
	_, err = store.PostSystemDebitDoubleEntry(ctx, DoubleEntryParams{
		TenantID:       tenantID,
		IdempotencyKey: "inactive-credit",
		Currency:       "AED",
		ReferenceType:  "deposit",
		ReferenceID:    "inactive-credit",
		DebitWalletID:  clearingWallet.ID,
		CreditWalletID: receiverWallet.ID,
		Amount:         100,
	})
	if !errors.Is(err, ErrWalletInactive) {
		t.Fatalf("PostSystemDebitDoubleEntry(inactive credit) error = %v, want %v", err, ErrWalletInactive)
	}
	exists, err = store.LedgerTransactionExists(ctx, tenantID, "inactive-credit")
	if err != nil {
		t.Fatalf("check inactive credit ledger transaction: %v", err)
	}
	if exists {
		t.Fatal("inactive credit ledger transaction persisted")
	}
	assertWalletBalances(t, ctx, store, tenantID, clearingWallet.ID, 0, 0)
	assertWalletBalances(t, ctx, store, tenantID, receiverWallet.ID, 0, 0)
}

func TestExistingDoubleEntryMatches(t *testing.T) {
	debitID := uuid.New()
	creditID := uuid.New()
	params := DoubleEntryParams{
		TenantID:       "tenant",
		IdempotencyKey: "entry-1",
		Currency:       "AED",
		ReferenceType:  "deposit",
		ReferenceID:    "deposit-ref",
		DebitWalletID:  debitID,
		CreditWalletID: creditID,
		Amount:         500,
		Description:    "deposit",
		Metadata:       json.RawMessage(`{"purpose":"deposit","sequence":1}`),
	}
	txn := LedgerTransaction{
		IdempotencyKey: params.IdempotencyKey,
		Currency:       params.Currency,
		CurrencyUnitID: 11,
		ReferenceType:  params.ReferenceType,
		ReferenceID:    sql.NullString{String: params.ReferenceID, Valid: true},
		Status:         "completed",
		Metadata:       RawJSON(`{"sequence":1,"purpose":"deposit"}`),
	}
	result := &DoubleEntryResult{
		DebitEntry:  matchingTestLedgerEntry(debitID, "debit", params),
		CreditEntry: matchingTestLedgerEntry(creditID, "credit", params),
	}
	if !existingDoubleEntryMatches(txn, result, params) {
		t.Fatal("existingDoubleEntryMatches() = false, want true")
	}

	cases := []struct {
		name   string
		mutate func(*LedgerTransaction, *DoubleEntryResult, *DoubleEntryParams)
	}{
		{
			name: "amount",
			mutate: func(txn *LedgerTransaction, result *DoubleEntryResult, params *DoubleEntryParams) {
				params.Amount++
			},
		},
		{
			name: "debit-wallet",
			mutate: func(txn *LedgerTransaction, result *DoubleEntryResult, params *DoubleEntryParams) {
				params.DebitWalletID = uuid.New()
			},
		},
		{
			name: "credit-wallet",
			mutate: func(txn *LedgerTransaction, result *DoubleEntryResult, params *DoubleEntryParams) {
				params.CreditWalletID = uuid.New()
			},
		},
		{
			name: "currency",
			mutate: func(txn *LedgerTransaction, result *DoubleEntryResult, params *DoubleEntryParams) {
				params.Currency = "USD"
			},
		},
		{
			name: "currency-unit",
			mutate: func(txn *LedgerTransaction, result *DoubleEntryResult, params *DoubleEntryParams) {
				result.CreditEntry.CurrencyUnitID++
			},
		},
		{
			name: "reference",
			mutate: func(txn *LedgerTransaction, result *DoubleEntryResult, params *DoubleEntryParams) {
				params.ReferenceID = "other-ref"
			},
		},
		{
			name: "missing-entry",
			mutate: func(txn *LedgerTransaction, result *DoubleEntryResult, params *DoubleEntryParams) {
				result.CreditEntry = nil
			},
		},
		{
			name: "transaction-status",
			mutate: func(txn *LedgerTransaction, result *DoubleEntryResult, params *DoubleEntryParams) {
				txn.Status = "pending"
			},
		},
		{
			name: "description",
			mutate: func(txn *LedgerTransaction, result *DoubleEntryResult, params *DoubleEntryParams) {
				params.Description = "other"
			},
		},
		{
			name: "metadata",
			mutate: func(txn *LedgerTransaction, result *DoubleEntryResult, params *DoubleEntryParams) {
				params.Metadata = json.RawMessage(`{"purpose":"deposit","sequence":2}`)
			},
		},
		{
			name: "entry-status",
			mutate: func(txn *LedgerTransaction, result *DoubleEntryResult, params *DoubleEntryParams) {
				result.DebitEntry.Status = "pending"
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testTxn := txn
			testResult := &DoubleEntryResult{
				DebitEntry:  matchingTestLedgerEntry(debitID, "debit", params),
				CreditEntry: matchingTestLedgerEntry(creditID, "credit", params),
			}
			testParams := params
			tc.mutate(&testTxn, testResult, &testParams)
			if existingDoubleEntryMatches(testTxn, testResult, testParams) {
				t.Fatal("existingDoubleEntryMatches() = true, want false")
			}
		})
	}
}

func matchingTestLedgerEntry(walletID uuid.UUID, entryType string, params DoubleEntryParams) *LedgerEntry {
	return &LedgerEntry{
		WalletID:       walletID,
		EntryType:      entryType,
		Amount:         params.Amount,
		Currency:       params.Currency,
		CurrencyUnitID: 11,
		Status:         "completed",
		Description:    sql.NullString{String: params.Description, Valid: params.Description != ""},
		Metadata:       RawJSON(`{"sequence":1,"purpose":"deposit"}`),
	}
}

func TestExistingHoldCreateMatches(t *testing.T) {
	walletID := uuid.New()
	params := HoldParams{
		TenantID:       "tenant",
		WalletID:       walletID,
		Amount:         500,
		Reason:         "withdrawal",
		ReferenceType:  "withdrawal",
		ReferenceID:    "withdrawal-ref",
		IdempotencyKey: "withdrawal-ref:hold",
		ExpiresAt:      time.Now().UTC().Add(time.Hour),
		Metadata:       json.RawMessage(`{"purpose":"withdrawal","sequence":1}`),
	}
	hold := BalanceHold{
		TenantID:        params.TenantID,
		WalletID:        params.WalletID,
		Amount:          params.Amount,
		AmountRemaining: params.Amount,
		Reason:          params.Reason,
		ReferenceType:   params.ReferenceType,
		ReferenceID:     params.ReferenceID,
		IdempotencyKey:  params.IdempotencyKey,
		Status:          HoldStatusActive,
		ExpiresAt:       params.ExpiresAt,
		Metadata:        RawJSON(`{"sequence":1,"purpose":"withdrawal"}`),
	}
	if !existingHoldCreateMatches(hold, params) {
		t.Fatal("existingHoldCreateMatches() = false, want true")
	}
	for _, status := range []string{HoldStatusActive, HoldStatusCommitted, HoldStatusReleased, HoldStatusExpired, HoldStatusCaptured} {
		lifecycleState := hold
		lifecycleState.Status = status
		lifecycleState.AmountRemaining = 0
		if !existingHoldCreateMatches(lifecycleState, params) {
			t.Fatalf("existingHoldCreateMatches(%s lifecycle) = false, want true", status)
		}
	}

	cases := []struct {
		name   string
		mutate func(*BalanceHold, *HoldParams)
	}{
		{
			name: "amount",
			mutate: func(hold *BalanceHold, params *HoldParams) {
				params.Amount++
			},
		},
		{
			name: "wallet",
			mutate: func(hold *BalanceHold, params *HoldParams) {
				params.WalletID = uuid.New()
			},
		},
		{
			name: "idempotency",
			mutate: func(hold *BalanceHold, params *HoldParams) {
				params.IdempotencyKey = "other-key"
			},
		},
		{
			name: "expires-at",
			mutate: func(hold *BalanceHold, params *HoldParams) {
				params.ExpiresAt = params.ExpiresAt.Add(time.Minute)
			},
		},
		{
			name: "metadata",
			mutate: func(hold *BalanceHold, params *HoldParams) {
				params.Metadata = json.RawMessage(`{"purpose":"withdrawal","sequence":2}`)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testHold := hold
			testParams := params
			tc.mutate(&testHold, &testParams)
			if existingHoldCreateMatches(testHold, testParams) {
				t.Fatal("existingHoldCreateMatches() = true, want false")
			}
		})
	}
}

func TestPostHeldDoubleEntryValidation(t *testing.T) {
	params := HeldDoubleEntryParams{
		HoldID: 1,
		Entry: DoubleEntryParams{
			TenantID:       "tenant",
			IdempotencyKey: "entry-1",
			Currency:       "AED",
			ReferenceType:  "withdrawal",
			ReferenceID:    "withdrawal-ref",
			DebitWalletID:  uuid.New(),
			CreditWalletID: uuid.New(),
			Amount:         100,
		},
	}

	bad := params
	bad.HoldID = 0
	_, err := (&Store{}).PostHeldDoubleEntry(context.Background(), bad)
	if !errors.Is(err, ErrInvalidHoldID) {
		t.Fatalf("missing hold id error = %v, want %v", err, ErrInvalidHoldID)
	}

	bad = params
	bad.Entry.TenantID = ""
	_, err = (&Store{}).PostHeldDoubleEntry(context.Background(), bad)
	if !errors.Is(err, ErrMissingTenantID) {
		t.Fatalf("missing tenant error = %v, want %v", err, ErrMissingTenantID)
	}

	_, err = (&Store{}).PostSystemDebitDoubleEntry(context.Background(), bad.Entry)
	if !errors.Is(err, ErrMissingTenantID) {
		t.Fatalf("system debit missing tenant error = %v, want %v", err, ErrMissingTenantID)
	}
}

func setWalletBalances(t *testing.T, ctx context.Context, db *basestore.DB, tenantID string, walletID uuid.UUID, balance, available int64) {
	t.Helper()
	stmt := db.Rebind(`UPDATE wallets
		SET balance = ?, available_balance = ?, updated_at = ?
		WHERE tenant_id = ? AND id = ?`)
	if _, err := db.ExecContext(ctx, stmt, balance, available, time.Now().UTC(), tenantID, walletID); err != nil {
		t.Fatalf("set wallet balances: %v", err)
	}
}

func setWalletStatus(t *testing.T, ctx context.Context, db *basestore.DB, tenantID string, walletID uuid.UUID, status string) {
	t.Helper()
	stmt := db.Rebind(`UPDATE wallets
		SET status = ?, updated_at = ?
		WHERE tenant_id = ? AND id = ?`)
	if _, err := db.ExecContext(ctx, stmt, status, time.Now().UTC(), tenantID, walletID); err != nil {
		t.Fatalf("set wallet status: %v", err)
	}
}

func assertHoldReferenceCount(t *testing.T, ctx context.Context, db *basestore.DB, tenantID, referenceID string, want int) {
	t.Helper()
	var count int
	stmt := db.Rebind("SELECT COUNT(*) FROM balance_holds WHERE tenant_id = ? AND reference_id = ?")
	if err := db.GetContext(ctx, &count, stmt, tenantID, referenceID); err != nil {
		t.Fatalf("count holds: %v", err)
	}
	if count != want {
		t.Fatalf("hold rows for %s = %d, want %d", referenceID, count, want)
	}
}

func assertWalletBalances(t *testing.T, ctx context.Context, store *Store, tenantID string, walletID uuid.UUID, balance, available int64) {
	t.Helper()
	wallet, err := store.GetWallet(ctx, tenantID, walletID)
	if err != nil {
		t.Fatalf("get wallet %s: %v", walletID, err)
	}
	if wallet.Balance != balance || wallet.AvailableBalance != available {
		t.Fatalf("wallet %s balances = balance:%d available:%d, want balance:%d available:%d",
			walletID, wallet.Balance, wallet.AvailableBalance, balance, available)
	}
}

func assertHold(t *testing.T, ctx context.Context, db *basestore.DB, tenantID string, holdID int64, status string, remaining int64, captured bool) {
	t.Helper()
	stmt := db.Rebind("SELECT * FROM balance_holds WHERE tenant_id = ? AND id = ?")
	var hold BalanceHold
	if err := db.GetContext(ctx, &hold, stmt, tenantID, holdID); err != nil {
		t.Fatalf("get hold: %v", err)
	}
	if hold.Status != status || hold.AmountRemaining != remaining {
		t.Fatalf("hold = status:%q remaining:%d, want status:%q remaining:%d",
			hold.Status, hold.AmountRemaining, status, remaining)
	}
	if hold.CapturedAt.Valid != captured {
		t.Fatalf("hold captured_at valid = %v, want %v", hold.CapturedAt.Valid, captured)
	}
}
