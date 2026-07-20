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
)

func TestFundingSourceTotalsFollowIdempotentLedgerLinks(t *testing.T) {
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

	dbName := fmt.Sprintf("noebs_wallet_funding_%d", time.Now().UnixNano())
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

	const tenantID = "tenant-funding"
	if err := basestore.MigrateScope(ctx, db, basestore.MigrationScopeWalletLedger); err != nil {
		t.Fatalf("migrate wallet ledger: %v", err)
	}
	provisionWalletStoreTestTenant(t, ctx, db, tenantID, "Funding Tenant")

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

	source := FundingSource{
		TenantID:           tenantID,
		WalletID:           userWallet.ID,
		SourceType:         "bank_account",
		PSPProvider:        sql.NullString{String: "bankpay", Valid: true},
		ExternalReference:  sql.NullString{String: "acct-ref-1", Valid: true},
		VerificationStatus: "verified",
		VerifiedAt:         sql.NullTime{Time: time.Now().UTC(), Valid: true},
		Currency:           "AED",
		SourceDetails:      []byte(`{"account_last4":"4321"}`),
		SupportsWithdrawal: true,
		WithdrawalMethod:   []byte(`{"account_number":"1234567890","bank_code":"044"}`),
	}
	stored, err := store.UpsertFundingSource(ctx, source)
	if err != nil {
		t.Fatalf("upsert funding source: %v", err)
	}
	if stored.TotalFunded != 0 {
		t.Fatalf("source total_funded after upsert = %d, want 0", stored.TotalFunded)
	}

	mismatch := source
	mismatch.Currency = "USD"
	if _, err := store.UpsertFundingSource(ctx, mismatch); !errors.Is(err, ErrDuplicateFundingSource) {
		t.Fatalf("mismatched funding source error = %v, want %v", err, ErrDuplicateFundingSource)
	}
	sourceDetailsMismatch := source
	sourceDetailsMismatch.SourceDetails = []byte(`{"account_last4":"9999"}`)
	if _, err := store.UpsertFundingSource(ctx, sourceDetailsMismatch); !errors.Is(err, ErrDuplicateFundingSource) {
		t.Fatalf("mismatched funding source details error = %v, want %v", err, ErrDuplicateFundingSource)
	}
	withdrawalMethodMismatch := source
	withdrawalMethodMismatch.WithdrawalMethod = []byte(`{"account_number":"0000000000","bank_code":"044"}`)
	if _, err := store.UpsertFundingSource(ctx, withdrawalMethodMismatch); !errors.Is(err, ErrDuplicateFundingSource) {
		t.Fatalf("mismatched funding source withdrawal method error = %v, want %v", err, ErrDuplicateFundingSource)
	}

	posted, err := store.PostSystemDebitDoubleEntry(ctx, DoubleEntryParams{
		TenantID:       tenantID,
		IdempotencyKey: "deposit-ref:deposit",
		Currency:       "AED",
		ReferenceType:  "deposit",
		ReferenceID:    "deposit-ref",
		DebitWalletID:  treasuryWallet.ID,
		CreditWalletID: userWallet.ID,
		Amount:         1000,
		Description:    "deposit",
	})
	if err != nil {
		t.Fatalf("post deposit ledger entry: %v", err)
	}

	link := LedgerFundingLink{
		TenantID:        tenantID,
		LedgerEntryID:   posted.CreditEntry.ID,
		FundingSourceID: stored.ID,
		Amount:          1000,
		Currency:        "AED",
	}
	createdLink, err := store.CreateFundingLink(ctx, link)
	if err != nil {
		t.Fatalf("create funding link: %v", err)
	}
	got, err := store.GetFundingSourceByID(ctx, tenantID, stored.ID)
	if err != nil {
		t.Fatalf("get funding source: %v", err)
	}
	if got.TotalFunded != 1000 {
		t.Fatalf("source total_funded after link = %d, want 1000", got.TotalFunded)
	}

	replayedLink, err := store.CreateFundingLink(ctx, link)
	if err != nil {
		t.Fatalf("replay funding link: %v", err)
	}
	if replayedLink.ID != createdLink.ID {
		t.Fatalf("replayed link id = %d, want %d", replayedLink.ID, createdLink.ID)
	}
	got, err = store.GetFundingSourceByID(ctx, tenantID, stored.ID)
	if err != nil {
		t.Fatalf("get funding source after replay: %v", err)
	}
	if got.TotalFunded != 1000 {
		t.Fatalf("source total_funded after replay = %d, want 1000", got.TotalFunded)
	}

	mismatchedLink := link
	mismatchedLink.Amount = 900
	if _, err := store.CreateFundingLink(ctx, mismatchedLink); !errors.Is(err, ErrDuplicateFundingLink) {
		t.Fatalf("mismatched funding link error = %v, want %v", err, ErrDuplicateFundingLink)
	}

	withdrawal, err := store.PostDoubleEntry(ctx, DoubleEntryParams{
		TenantID:       tenantID,
		IdempotencyKey: "withdrawal-ref:withdrawal",
		Currency:       "AED",
		ReferenceType:  "withdrawal",
		ReferenceID:    "withdrawal-ref",
		DebitWalletID:  userWallet.ID,
		CreditWalletID: treasuryWallet.ID,
		Amount:         400,
		Description:    "withdrawal",
	})
	if err != nil {
		t.Fatalf("post withdrawal ledger entry: %v", err)
	}
	usageLink := LedgerFundingLink{
		TenantID:        tenantID,
		LedgerEntryID:   withdrawal.DebitEntry.ID,
		FundingSourceID: stored.ID,
		Amount:          400,
		Currency:        "AED",
	}
	if _, err := store.CreateFundingLink(ctx, usageLink); err != nil {
		t.Fatalf("create funding usage link: %v", err)
	}
	got, err = store.GetFundingSourceByID(ctx, tenantID, stored.ID)
	if err != nil {
		t.Fatalf("get funding source after usage: %v", err)
	}
	if got.TotalWithdrawn != 400 {
		t.Fatalf("source total_withdrawn after usage = %d, want 400", got.TotalWithdrawn)
	}
	if _, err := store.CreateFundingLink(ctx, usageLink); err != nil {
		t.Fatalf("replay funding usage link: %v", err)
	}
	got, err = store.GetFundingSourceByID(ctx, tenantID, stored.ID)
	if err != nil {
		t.Fatalf("get funding source after usage replay: %v", err)
	}
	if got.TotalWithdrawn != 400 {
		t.Fatalf("source total_withdrawn after usage replay = %d, want 400", got.TotalWithdrawn)
	}

	destination, err := store.CreateWithdrawalDestination(ctx, WithdrawalDestination{
		TenantID:            tenantID,
		WalletID:            userWallet.ID,
		DestinationType:     "bank_account",
		DestinationDetails:  []byte(`{"account_last4":"4321"}`),
		Currency:            "AED",
		OwnershipStatus:     "verified",
		OwnershipVerifiedAt: sql.NullTime{Time: time.Now().UTC(), Valid: true},
		IsActive:            true,
	})
	if err != nil {
		t.Fatalf("create withdrawal destination: %v", err)
	}
	destinationLink := LedgerWithdrawalDestinationLink{
		TenantID:      tenantID,
		LedgerEntryID: withdrawal.DebitEntry.ID,
		DestinationID: destination.ID,
		Amount:        400,
		Currency:      "AED",
	}
	amountMismatchDestinationLink := destinationLink
	amountMismatchDestinationLink.Amount = 300
	if _, err := store.CreateWithdrawalDestinationLink(ctx, amountMismatchDestinationLink); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("ledger amount mismatch destination link error = %v, want %v", err, ErrInvalidAmount)
	}
	currencyMismatchDestinationLink := destinationLink
	currencyMismatchDestinationLink.Currency = "USD"
	if _, err := store.CreateWithdrawalDestinationLink(ctx, currencyMismatchDestinationLink); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("ledger currency mismatch destination link error = %v, want %v", err, ErrCurrencyMismatch)
	}
	createdDestinationLink, err := store.CreateWithdrawalDestinationLink(ctx, destinationLink)
	if err != nil {
		t.Fatalf("create withdrawal destination link: %v", err)
	}
	gotDestination, err := store.GetWithdrawalDestination(ctx, tenantID, destination.ID)
	if err != nil {
		t.Fatalf("get withdrawal destination: %v", err)
	}
	if gotDestination.TotalWithdrawn != 400 {
		t.Fatalf("destination total_withdrawn after usage = %d, want 400", gotDestination.TotalWithdrawn)
	}
	replayedDestinationLink, err := store.CreateWithdrawalDestinationLink(ctx, destinationLink)
	if err != nil {
		t.Fatalf("replay withdrawal destination link: %v", err)
	}
	if replayedDestinationLink.ID != createdDestinationLink.ID {
		t.Fatalf("replayed destination link id = %d, want %d", replayedDestinationLink.ID, createdDestinationLink.ID)
	}
	gotDestination, err = store.GetWithdrawalDestination(ctx, tenantID, destination.ID)
	if err != nil {
		t.Fatalf("get withdrawal destination after replay: %v", err)
	}
	if gotDestination.TotalWithdrawn != 400 {
		t.Fatalf("destination total_withdrawn after replay = %d, want 400", gotDestination.TotalWithdrawn)
	}
	mismatchedDestinationLink := destinationLink
	mismatchedDestinationLink.Amount = 300
	if _, err := store.CreateWithdrawalDestinationLink(ctx, mismatchedDestinationLink); !errors.Is(err, ErrDuplicateDestinationLink) {
		t.Fatalf("mismatched withdrawal destination link error = %v, want %v", err, ErrDuplicateDestinationLink)
	}
}
