package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

	dbName := fmt.Sprintf("noebs_wallet_holds_%d", time.Now().UnixNano())
	dbURL, err := container.CreateDatabase(ctx, dbName)
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
	if err := basestore.MigrateScope(ctx, db, tenantID, basestore.MigrationScopeWalletLedger); err != nil {
		t.Fatalf("migrate wallet ledger: %v", err)
	}
	if err := basestore.New(db).EnsureTenant(ctx, tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}

	store := New(db)
	userWallet, err := store.EnsureWallet(ctx, EnsureWalletParams{
		TenantID:  tenantID,
		OwnerType: OwnerTypeUser,
		OwnerID:   "user-1",
		UserID:    1,
		Currency:  "AED",
		KYCTier:   KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure user wallet: %v", err)
	}
	treasuryWallet, err := store.EnsureWallet(ctx, EnsureWalletParams{
		TenantID:  tenantID,
		OwnerType: OwnerTypeSystem,
		OwnerID:   SystemTreasury,
		Currency:  "AED",
		KYCTier:   KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure treasury wallet: %v", err)
	}
	feesWallet, err := store.EnsureWallet(ctx, EnsureWalletParams{
		TenantID:  tenantID,
		OwnerType: OwnerTypeSystem,
		OwnerID:   SystemFees,
		Currency:  "AED",
		KYCTier:   KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure fees wallet: %v", err)
	}
	setWalletBalances(t, ctx, db, tenantID, userWallet.ID, 1000, 1000)

	hold, err := store.CreateHold(ctx, HoldParams{
		TenantID:       tenantID,
		WalletID:       userWallet.ID,
		Amount:         1000,
		Reason:         "withdrawal",
		ReferenceType:  "withdrawal",
		ReferenceID:    "withdrawal-ref",
		IdempotencyKey: "withdrawal-ref:hold",
		ExpiresAt:      time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create hold: %v", err)
	}
	assertWalletBalances(t, ctx, store, tenantID, userWallet.ID, 1000, 0)

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
	}
	if _, err := store.PostHeldDoubleEntry(ctx, HeldDoubleEntryParams{HoldID: hold.ID, Entry: withdrawalEntry}); err != nil {
		t.Fatalf("post held withdrawal entry: %v", err)
	}
	assertWalletBalances(t, ctx, store, tenantID, userWallet.ID, 100, 0)
	assertWalletBalances(t, ctx, store, tenantID, treasuryWallet.ID, 900, 900)
	assertHold(t, ctx, db, tenantID, hold.ID, HoldStatusActive, 100, false)

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
		TenantID:  tenantID,
		OwnerType: OwnerTypeSystem,
		OwnerID:   SystemPSPClearing,
		Currency:  "AED",
		KYCTier:   KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure psp clearing wallet: %v", err)
	}
	receiverWallet, err := store.EnsureWallet(ctx, EnsureWalletParams{
		TenantID:  tenantID,
		OwnerType: OwnerTypeUser,
		OwnerID:   "user-2",
		UserID:    2,
		Currency:  "AED",
		KYCTier:   KYCTierUnverified,
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
	}
	txn := LedgerTransaction{
		Currency:      params.Currency,
		ReferenceType: params.ReferenceType,
		ReferenceID:   sql.NullString{String: params.ReferenceID, Valid: true},
	}
	result := &DoubleEntryResult{
		DebitEntry:  &LedgerEntry{WalletID: debitID, Amount: params.Amount, Currency: params.Currency},
		CreditEntry: &LedgerEntry{WalletID: creditID, Amount: params.Amount, Currency: params.Currency},
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testTxn := txn
			testResult := &DoubleEntryResult{
				DebitEntry:  &LedgerEntry{WalletID: debitID, Amount: params.Amount, Currency: params.Currency},
				CreditEntry: &LedgerEntry{WalletID: creditID, Amount: params.Amount, Currency: params.Currency},
			}
			testParams := params
			tc.mutate(&testTxn, testResult, &testParams)
			if existingDoubleEntryMatches(testTxn, testResult, testParams) {
				t.Fatal("existingDoubleEntryMatches() = true, want false")
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
