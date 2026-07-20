package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/testdb"
	basestore "github.com/adonese/noebs/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestHeldWithdrawalSettlementCommitsWholeAggregateAndReplays(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	store, db, tenantID := newAtomicSettlementTestStore(t, ctx)
	fixture := newHeldWithdrawalFixture(t, ctx, store, db, tenantID, true, true)

	posted, err := store.PostHeldWithdrawalSettlement(ctx, fixture.Params)
	if err != nil {
		t.Fatalf("post held withdrawal: %v", err)
	}
	if posted.Existing || len(posted.Transfers) != 2 {
		t.Fatalf("posted settlement = %+v, want new two-leg settlement", posted)
	}
	assertHeldWithdrawalCommitted(t, ctx, fixture, posted.TransactionID, 4)

	replayed, err := store.PostHeldWithdrawalSettlement(ctx, fixture.Params)
	if err != nil {
		t.Fatalf("replay held withdrawal: %v", err)
	}
	if !replayed.Existing || replayed.TransactionID != posted.TransactionID || len(replayed.Transfers) != 2 {
		t.Fatalf("replayed settlement = %+v, want existing transaction %d", replayed, posted.TransactionID)
	}
	assertHeldWithdrawalCommitted(t, ctx, fixture, posted.TransactionID, 4)

	mismatch := fixture.Params
	mismatch.Settlement.Transfers = append([]SettlementTransfer(nil), fixture.Params.Settlement.Transfers...)
	mismatch.Settlement.Transfers[1].Description = "different fee"
	if _, err := store.PostHeldWithdrawalSettlement(ctx, mismatch); !errors.Is(err, ErrDuplicateTransaction) {
		t.Fatalf("mismatched replay error = %v, want %v", err, ErrDuplicateTransaction)
	}
}

func TestHeldWithdrawalSettlementSupportsNoFeeAndReturnToSource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	store, db, tenantID := newAtomicSettlementTestStore(t, ctx)
	fixture := newHeldWithdrawalFixture(t, ctx, store, db, tenantID, false, false)

	posted, err := store.PostHeldWithdrawalSettlement(ctx, fixture.Params)
	if err != nil {
		t.Fatalf("post fee-free held withdrawal: %v", err)
	}
	if len(posted.Transfers) != 1 {
		t.Fatalf("fee-free transfers = %d, want 1", len(posted.Transfers))
	}
	assertWalletBalances(t, ctx, store, tenantID, fixture.UserID, 300, 300)
	assertWalletBalances(t, ctx, store, tenantID, fixture.TreasuryID, 700, 700)
	assertHold(t, ctx, db, tenantID, fixture.Hold.ID, HoldStatusCaptured, 0, true)
	assertSettlementRowCounts(t, ctx, db, tenantID, 1, 2)
	assertSettlementLinkCounts(t, ctx, db, tenantID, posted.TransactionID, 1, 0)
}

func TestHeldWithdrawalSettlementRollsBackLateFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	store, db, tenantID := newAtomicSettlementTestStore(t, ctx)
	fixture := newHeldWithdrawalFixture(t, ctx, store, db, tenantID, true, true)

	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_held_withdrawal_source_update()
		RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'injected funding source failure';
		END
		$$`); err != nil {
		t.Fatalf("create failure function: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_held_withdrawal_source_update
		BEFORE UPDATE ON funding_sources
		FOR EACH ROW EXECUTE FUNCTION fail_held_withdrawal_source_update()`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	if _, err := store.PostHeldWithdrawalSettlement(ctx, fixture.Params); err == nil {
		t.Fatal("held withdrawal succeeded despite injected funding source failure")
	}
	assertHeldWithdrawalUncommitted(t, ctx, fixture)
}

func TestConcurrentHeldWithdrawalSettlementCommitsOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	store, db, tenantID := newAtomicSettlementTestStore(t, ctx)
	fixture := newHeldWithdrawalFixture(t, ctx, store, db, tenantID, true, true)

	start := make(chan struct{})
	results := make(chan *MultiLegSettlementResult, 2)
	errorsCh := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			result, err := store.PostHeldWithdrawalSettlement(ctx, fixture.Params)
			results <- result
			errorsCh <- err
		}()
	}
	ready.Wait()
	close(start)

	var transactionID int64
	var existing int
	for range 2 {
		if err := <-errorsCh; err != nil {
			t.Fatalf("concurrent held withdrawal: %v", err)
		}
		result := <-results
		if result == nil {
			t.Fatal("concurrent held withdrawal returned nil result")
		}
		if transactionID == 0 {
			transactionID = result.TransactionID
		} else if result.TransactionID != transactionID {
			t.Fatalf("concurrent transaction id = %d, want %d", result.TransactionID, transactionID)
		}
		if result.Existing {
			existing++
		}
	}
	if existing != 1 {
		t.Fatalf("existing concurrent results = %d, want 1", existing)
	}
	assertHeldWithdrawalCommitted(t, ctx, fixture, transactionID, 4)
}

func TestHeldWithdrawalSettlementExcludesCompetingDebit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	store, db, tenantID := newAtomicSettlementTestStore(t, ctx)
	fixture := newHeldWithdrawalFixture(t, ctx, store, db, tenantID, true, true)
	competingRecipientID := uuid.MustParse("30000000-0000-0000-0000-000000000010")
	insertSettlementTestWallet(t, ctx, db, tenantID, competingRecipientID, OwnerTypeUser, "withdrawal-competitor", 0)

	blocker, err := db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback() }()
	var locked uuid.UUID
	if err := blocker.GetContext(ctx, &locked,
		db.Rebind(`SELECT id FROM wallets WHERE tenant_id = ? AND id = ? FOR UPDATE`),
		tenantID,
		fixture.FeesID,
	); err != nil {
		t.Fatalf("lock fee wallet: %v", err)
	}

	settlementResult := make(chan error, 1)
	go func() {
		_, err := store.PostHeldWithdrawalSettlement(ctx, fixture.Params)
		settlementResult <- err
	}()
	waitForWalletLock(t, ctx, db, tenantID, fixture.UserID)

	competingResult := make(chan error, 1)
	go func() {
		_, err := store.PostDoubleEntry(ctx, DoubleEntryParams{
			TenantID: tenantID, IdempotencyKey: "held-withdrawal-competitor", Currency: "AED",
			ReferenceType: "test", ReferenceID: "held-withdrawal-competitor",
			DebitWalletID: fixture.UserID, CreditWalletID: competingRecipientID, Amount: 300,
		})
		competingResult <- err
	}()
	if err := blocker.Rollback(); err != nil {
		t.Fatalf("release fee wallet: %v", err)
	}
	if err := <-settlementResult; err != nil {
		t.Fatalf("held withdrawal settlement: %v", err)
	}
	if err := <-competingResult; !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("competing debit error = %v, want %v", err, ErrInsufficientFunds)
	}
	assertWalletBalances(t, ctx, store, tenantID, fixture.UserID, 200, 200)
	assertWalletBalances(t, ctx, store, tenantID, competingRecipientID, 0, 0)
	assertSettlementRowCounts(t, ctx, db, tenantID, 1, 4)
}

func TestHeldWithdrawalSettlementRejectsAggregateOverflowWithoutMutation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	store, db, tenantID := newAtomicSettlementTestStore(t, ctx)
	fixture := newHeldWithdrawalFixture(t, ctx, store, db, tenantID, true, true)

	if _, err := db.ExecContext(ctx, db.Rebind(`UPDATE funding_sources
		SET total_funded = ?, total_withdrawn = ?
		WHERE tenant_id = ? AND id = ?`),
		int64(math.MaxInt64), int64(math.MaxInt64-50), tenantID, fixture.Source.ID,
	); err != nil {
		t.Fatalf("prepare source overflow: %v", err)
	}
	if _, err := store.PostHeldWithdrawalSettlement(ctx, fixture.Params); !errors.Is(err, ErrAmountOverflow) {
		t.Fatalf("source overflow error = %v, want %v", err, ErrAmountOverflow)
	}
	assertHeldWithdrawalUncommitted(t, ctx, fixture)
	var totalWithdrawn int64
	if err := db.GetContext(ctx, &totalWithdrawn,
		db.Rebind(`SELECT total_withdrawn FROM funding_sources WHERE tenant_id = ? AND id = ?`),
		tenantID,
		fixture.Source.ID,
	); err != nil {
		t.Fatalf("load source total after overflow: %v", err)
	}
	if totalWithdrawn != math.MaxInt64-50 {
		t.Fatalf("source total after overflow = %d, want %d", totalWithdrawn, int64(math.MaxInt64-50))
	}
}

func TestMultiLegSettlementRejectsWalletOverflowWithoutMutation(t *testing.T) {
	t.Run("credit max", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		store, db, tenantID := newAtomicSettlementTestStore(t, ctx)
		debitID := uuid.MustParse("10000000-0000-0000-0000-000000000020")
		creditID := uuid.MustParse("20000000-0000-0000-0000-000000000020")
		insertSettlementTestWallet(t, ctx, db, tenantID, debitID, OwnerTypeUser, "overflow-debit", 1_000)
		insertSettlementTestWallet(t, ctx, db, tenantID, creditID, OwnerTypeSystem, "overflow-credit", math.MaxInt64-50)
		insertSettlementTestLimit(t, ctx, db, tenantID, "p2p", "AED", 1_000, 1_000, 1_000)
		params := MultiLegSettlementParams{
			TenantID: tenantID, IdempotencyKey: "overflow-credit", Currency: "AED",
			ReferenceType: "p2p", ReferenceID: "overflow-credit",
			Transfers: []SettlementTransfer{{DebitWalletID: debitID, CreditWalletID: creditID, Amount: 100}},
			LimitUsage: LimitUsageParams{
				TenantID: tenantID, CommandID: "overflow-credit", WalletID: debitID,
				TransactionType: "p2p", Currency: "AED", Amount: 100,
			},
		}
		if _, err := store.PostMultiLegSettlement(ctx, params); !errors.Is(err, ErrAmountOverflow) {
			t.Fatalf("credit overflow error = %v, want %v", err, ErrAmountOverflow)
		}
		assertWalletBalances(t, ctx, store, tenantID, debitID, 1_000, 1_000)
		assertWalletBalances(t, ctx, store, tenantID, creditID, math.MaxInt64-50, math.MaxInt64-50)
		assertSettlementRowCounts(t, ctx, db, tenantID, 0, 0)
	})

	t.Run("system debit min", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		store, db, tenantID := newAtomicSettlementTestStore(t, ctx)
		debitID := uuid.MustParse("10000000-0000-0000-0000-000000000021")
		creditID := uuid.MustParse("20000000-0000-0000-0000-000000000021")
		insertSettlementTestWallet(t, ctx, db, tenantID, debitID, OwnerTypeSystem, SystemTreasury, math.MinInt64+50)
		insertSettlementTestWallet(t, ctx, db, tenantID, creditID, OwnerTypeUser, "underflow-credit", 0)
		insertSettlementTestLimit(t, ctx, db, tenantID, "deposit", "AED", 1_000, 1_000, 1_000)
		params := MultiLegSettlementParams{
			TenantID: tenantID, IdempotencyKey: "underflow-debit", Currency: "AED",
			ReferenceType: "deposit", ReferenceID: "underflow-debit",
			Transfers: []SettlementTransfer{{DebitWalletID: debitID, CreditWalletID: creditID, Amount: 100}},
			LimitUsage: LimitUsageParams{
				TenantID: tenantID, CommandID: "underflow-debit", WalletID: creditID,
				TransactionType: "deposit", Currency: "AED", Amount: 100,
			},
		}
		if _, err := store.PostSystemFundedMultiLegSettlement(ctx, params); !errors.Is(err, ErrAmountOverflow) {
			t.Fatalf("system debit underflow error = %v, want %v", err, ErrAmountOverflow)
		}
		assertWalletBalances(t, ctx, store, tenantID, debitID, math.MinInt64+50, math.MinInt64+50)
		assertWalletBalances(t, ctx, store, tenantID, creditID, 0, 0)
		assertSettlementRowCounts(t, ctx, db, tenantID, 0, 0)
	})
}

func TestReleaseHoldRejectsAvailableBalanceOverflow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	store, db, tenantID := newAtomicSettlementTestStore(t, ctx)
	walletID := uuid.MustParse("10000000-0000-0000-0000-000000000030")
	insertSettlementTestWallet(t, ctx, db, tenantID, walletID, OwnerTypeUser, "hold-overflow", math.MaxInt64)
	var holdID int64
	if err := db.GetContext(ctx, &holdID, db.Rebind(`INSERT INTO balance_holds(
		tenant_id, wallet_id, amount, amount_remaining, reason, reference_type, reference_id,
		idempotency_key, status, expires_at, created_at
	) VALUES(?, ?, 1, 1, 'test', 'test', 'hold-overflow', 'hold-overflow', 'active',
		clock_timestamp() + INTERVAL '1 hour', clock_timestamp())
	RETURNING id`), tenantID, walletID); err != nil {
		t.Fatalf("insert overflow hold: %v", err)
	}
	if err := store.ReleaseHold(ctx, tenantID, holdID); !errors.Is(err, ErrAmountOverflow) {
		t.Fatalf("release hold overflow error = %v, want %v", err, ErrAmountOverflow)
	}
	assertWalletBalances(t, ctx, store, tenantID, walletID, math.MaxInt64, math.MaxInt64)
	assertHold(t, ctx, db, tenantID, holdID, HoldStatusActive, 1, false)
}

func TestMultiLegSettlementExcludesCompetingDebitAcrossEveryLeg(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	store, db, tenantID := newAtomicSettlementTestStore(t, ctx)

	senderID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	recipientID := uuid.MustParse("20000000-0000-0000-0000-000000000001")
	competingRecipientID := uuid.MustParse("30000000-0000-0000-0000-000000000001")
	feesID := uuid.MustParse("f0000000-0000-0000-0000-000000000001")
	insertSettlementTestWallet(t, ctx, db, tenantID, senderID, OwnerTypeUser, "sender", 1_000)
	insertSettlementTestWallet(t, ctx, db, tenantID, recipientID, OwnerTypeUser, "recipient", 0)
	insertSettlementTestWallet(t, ctx, db, tenantID, competingRecipientID, OwnerTypeUser, "competitor", 0)
	insertSettlementTestWallet(t, ctx, db, tenantID, feesID, OwnerTypeSystem, SystemFees, 0)
	insertSettlementTestLimit(t, ctx, db, tenantID, "p2p", "AED", 1_000, 1_000, 1_000)

	blocker, err := db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback() }()
	var locked uuid.UUID
	lockStmt := db.Rebind(`SELECT id FROM wallets WHERE tenant_id = ? AND id = ? FOR UPDATE`)
	if err := blocker.GetContext(ctx, &locked, lockStmt, tenantID, feesID); err != nil {
		t.Fatalf("lock fee wallet: %v", err)
	}

	settlement := MultiLegSettlementParams{
		TenantID:       tenantID,
		IdempotencyKey: "p2p:settlement",
		Currency:       "AED",
		ReferenceType:  "p2p",
		ReferenceID:    "p2p-reference",
		Transfers: []SettlementTransfer{
			{DebitWalletID: senderID, CreditWalletID: recipientID, Amount: 700, Description: "p2p"},
			{DebitWalletID: senderID, CreditWalletID: feesID, Amount: 100, Description: "p2p fee"},
		},
		LimitUsage: LimitUsageParams{
			TenantID: tenantID, CommandID: "p2p:command", WalletID: senderID,
			TransactionType: "p2p", Currency: "AED", Amount: 700,
		},
	}
	settlementResult := make(chan error, 1)
	go func() {
		_, err := store.PostMultiLegSettlement(ctx, settlement)
		settlementResult <- err
	}()
	waitForWalletLock(t, ctx, db, tenantID, senderID)

	competingResult := make(chan error, 1)
	go func() {
		_, err := store.PostDoubleEntry(ctx, DoubleEntryParams{
			TenantID: tenantID, IdempotencyKey: "competing-debit", Currency: "AED",
			ReferenceType: "test", ReferenceID: "competing-debit",
			DebitWalletID: senderID, CreditWalletID: competingRecipientID, Amount: 300,
		})
		competingResult <- err
	}()
	if err := blocker.Rollback(); err != nil {
		t.Fatalf("release fee wallet: %v", err)
	}
	if err := <-settlementResult; err != nil {
		t.Fatalf("multi-leg settlement: %v", err)
	}
	if err := <-competingResult; !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("competing debit error = %v, want %v", err, ErrInsufficientFunds)
	}

	assertSettlementTestBalance(t, ctx, store, tenantID, senderID, 200)
	assertSettlementTestBalance(t, ctx, store, tenantID, recipientID, 700)
	assertSettlementTestBalance(t, ctx, store, tenantID, feesID, 100)
	assertSettlementTestBalance(t, ctx, store, tenantID, competingRecipientID, 0)
	assertSettlementRowCounts(t, ctx, db, tenantID, 1, 4)

	replayed, err := store.PostMultiLegSettlement(ctx, settlement)
	if err != nil {
		t.Fatalf("replay multi-leg settlement: %v", err)
	}
	if !replayed.Existing || len(replayed.Transfers) != 2 {
		t.Fatalf("replay result = %+v, want existing two-leg settlement", replayed)
	}
	assertSettlementRowCounts(t, ctx, db, tenantID, 1, 4)
}

func TestConcurrentDepositSettlementsReserveCreditPolarityUsage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	store, db, tenantID := newAtomicSettlementTestStore(t, ctx)

	treasuryID := uuid.MustParse("10000000-0000-0000-0000-000000000002")
	userID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	insertSettlementTestWallet(t, ctx, db, tenantID, treasuryID, OwnerTypeSystem, SystemTreasury, 0)
	insertSettlementTestWallet(t, ctx, db, tenantID, userID, OwnerTypeUser, "deposit-user", 0)
	insertSettlementTestLimit(t, ctx, db, tenantID, "deposit", "AED", 600, 600, 500)

	settlement := func(index int) MultiLegSettlementParams {
		command := fmt.Sprintf("deposit:%d", index)
		return MultiLegSettlementParams{
			TenantID:       tenantID,
			IdempotencyKey: command + ":ledger",
			Currency:       "AED",
			ReferenceType:  "deposit",
			ReferenceID:    command,
			Transfers: []SettlementTransfer{{
				DebitWalletID: treasuryID, CreditWalletID: userID, Amount: 400, Description: "deposit",
			}},
			LimitUsage: LimitUsageParams{
				TenantID: tenantID, CommandID: command, WalletID: userID,
				TransactionType: "deposit", Currency: "AED", Amount: 400,
			},
		}
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for index := 1; index <= 2; index++ {
		params := settlement(index)
		go func() {
			ready.Done()
			<-start
			_, err := store.PostSystemFundedMultiLegSettlement(ctx, params)
			results <- err
		}()
	}
	ready.Wait()
	close(start)

	var succeeded int
	var limited int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrTransactionLimitExceeded):
			limited++
		default:
			t.Fatalf("concurrent deposit error: %v", err)
		}
	}
	if succeeded != 1 || limited != 1 {
		t.Fatalf("concurrent outcomes = success:%d limited:%d, want 1/1", succeeded, limited)
	}
	assertSettlementTestBalance(t, ctx, store, tenantID, treasuryID, -400)
	assertSettlementTestBalance(t, ctx, store, tenantID, userID, 400)
	assertSettlementRowCounts(t, ctx, db, tenantID, 1, 2)

	var reservationCount int
	if err := db.GetContext(ctx, &reservationCount,
		db.Rebind(`SELECT COUNT(*) FROM transaction_limit_reservations WHERE tenant_id = ? AND status = 'consumed'`),
		tenantID,
	); err != nil {
		t.Fatalf("count consumed limit reservations: %v", err)
	}
	if reservationCount != 1 {
		t.Fatalf("consumed limit reservations = %d, want 1", reservationCount)
	}
	var usageRows int
	if err := db.GetContext(ctx, &usageRows,
		db.Rebind(`SELECT COUNT(*) FROM transaction_limit_period_usage
			WHERE tenant_id = ? AND reserved_amount = 0 AND consumed_amount = 400`),
		tenantID,
	); err != nil {
		t.Fatalf("count limit period usage: %v", err)
	}
	if usageRows != 2 {
		t.Fatalf("period rows with consumed deposit credit = %d, want 2", usageRows)
	}
}

func TestLimitUsageReservationReplayAndReleaseRestoreCapacity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	store, db, tenantID := newAtomicSettlementTestStore(t, ctx)

	walletID := uuid.MustParse("20000000-0000-0000-0000-000000000003")
	insertSettlementTestWallet(t, ctx, db, tenantID, walletID, OwnerTypeUser, "limit-user", 0)
	insertSettlementTestLimit(t, ctx, db, tenantID, "deposit", "AED", 500, 500, 500)
	first := LimitUsageParams{
		TenantID: tenantID, CommandID: "deposit:first", WalletID: walletID,
		TransactionType: "deposit", Currency: "AED", Amount: 500,
	}
	reserved, err := store.ReserveLimitUsage(ctx, first)
	if err != nil {
		t.Fatalf("reserve full capacity: %v", err)
	}
	replayed, err := store.ReserveLimitUsage(ctx, first)
	if err != nil {
		t.Fatalf("replay reservation: %v", err)
	}
	if replayed.ID != reserved.ID || replayed.Status != LimitReservationStatusReserved {
		t.Fatalf("replayed reservation = %+v, want id %d reserved", replayed, reserved.ID)
	}
	mismatch := first
	mismatch.Amount--
	if _, err := store.ReserveLimitUsage(ctx, mismatch); !errors.Is(err, ErrDuplicateLimitReservation) {
		t.Fatalf("mismatched replay error = %v, want %v", err, ErrDuplicateLimitReservation)
	}
	second := first
	second.CommandID = "deposit:second"
	if _, err := store.ReserveLimitUsage(ctx, second); !errors.Is(err, ErrTransactionLimitExceeded) {
		t.Fatalf("reserve beyond capacity error = %v, want %v", err, ErrTransactionLimitExceeded)
	}
	if err := store.ReleaseLimitUsage(ctx, first); err != nil {
		t.Fatalf("release reservation: %v", err)
	}
	if err := store.ReleaseLimitUsage(ctx, first); err != nil {
		t.Fatalf("replay release: %v", err)
	}
	if _, err := store.ReserveLimitUsage(ctx, second); err != nil {
		t.Fatalf("reserve restored capacity: %v", err)
	}

	var rows int
	if err := db.GetContext(ctx, &rows,
		db.Rebind(`SELECT COUNT(*) FROM transaction_limit_period_usage
			WHERE tenant_id = ? AND reserved_amount = 500 AND consumed_amount = 0`),
		tenantID,
	); err != nil {
		t.Fatalf("count restored usage rows: %v", err)
	}
	if rows != 2 {
		t.Fatalf("period rows with restored capacity = %d, want 2", rows)
	}
}

func newAtomicSettlementTestStore(t *testing.T, ctx context.Context) (*Store, *basestore.DB, string) {
	t.Helper()
	container, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	const dbName = "wallet_ledger"
	dbURL, err := container.CreateDatabaseForRole(ctx, dbName, "wallet_ledger_migrate")
	if err != nil {
		t.Fatalf("create wallet database: %v", err)
	}
	db, err := basestore.OpenFromConfig(dbURL, basestore.DriverPostgres)
	if err != nil {
		t.Fatalf("open wallet database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		dropCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = container.DropDatabase(dropCtx, dbName)
	})
	if err := basestore.MigrateScope(ctx, db, basestore.MigrationScopeWalletLedger); err != nil {
		t.Fatalf("migrate wallet database: %v", err)
	}
	const tenantID = "tenant-atomic"
	provisionWalletStoreTestTenant(t, ctx, db, tenantID, "Atomic Settlement Tenant")
	return New(db), db, tenantID
}

func insertSettlementTestWallet(
	t *testing.T,
	ctx context.Context,
	db *basestore.DB,
	tenantID string,
	walletID uuid.UUID,
	ownerType string,
	ownerID string,
	balance int64,
) {
	t.Helper()
	stmt := db.Rebind(`INSERT INTO wallets(
		id, tenant_id, owner_type, owner_id, currency, balance, available_balance, status, kyc_tier
	) VALUES(?, ?, ?, ?, 'AED', ?, ?, 'active', ?)`)
	if _, err := db.ExecContext(ctx, stmt,
		walletID,
		tenantID,
		ownerType,
		ownerID,
		balance,
		balance,
		KYCTierUnverified,
	); err != nil {
		t.Fatalf("insert wallet %s: %v", ownerID, err)
	}
}

func insertSettlementTestLimit(
	t *testing.T,
	ctx context.Context,
	db *basestore.DB,
	tenantID string,
	transactionType string,
	currency string,
	daily int64,
	monthly int64,
	perTransaction int64,
) {
	t.Helper()
	stmt := db.Rebind(`INSERT INTO transaction_limits(
		tenant_id, kyc_tier, transaction_type, currency,
		daily_limit, monthly_limit, per_transaction_limit, is_active
	) VALUES(?, ?, ?, ?, ?, ?, ?, TRUE)`)
	if _, err := db.ExecContext(ctx, stmt,
		tenantID,
		KYCTierUnverified,
		transactionType,
		currency,
		daily,
		monthly,
		perTransaction,
	); err != nil {
		t.Fatalf("insert %s limit: %v", transactionType, err)
	}
}

func waitForWalletLock(
	t *testing.T,
	ctx context.Context,
	db *basestore.DB,
	tenantID string,
	walletID uuid.UUID,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	stmt := db.Rebind(`SELECT id FROM wallets WHERE tenant_id = ? AND id = ? FOR UPDATE NOWAIT`)
	for time.Now().Before(deadline) {
		tx, err := db.BeginTxx(ctx, nil)
		if err != nil {
			t.Fatalf("begin lock probe: %v", err)
		}
		var id uuid.UUID
		err = tx.GetContext(ctx, &id, stmt, tenantID, walletID)
		_ = tx.Rollback()
		if err == nil {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			return
		}
		t.Fatalf("probe wallet lock: %v", err)
	}
	t.Fatal("multi-leg settlement did not acquire sender lock")
}

func assertSettlementTestBalance(
	t *testing.T,
	ctx context.Context,
	store *Store,
	tenantID string,
	walletID uuid.UUID,
	want int64,
) {
	t.Helper()
	wallet, err := store.GetWallet(ctx, tenantID, walletID)
	if err != nil {
		t.Fatalf("get wallet %s: %v", walletID, err)
	}
	if wallet.Balance != want || wallet.AvailableBalance != want {
		t.Fatalf("wallet %s balances = %d/%d, want %d/%d", walletID, wallet.Balance, wallet.AvailableBalance, want, want)
	}
}

func assertSettlementRowCounts(
	t *testing.T,
	ctx context.Context,
	db *basestore.DB,
	tenantID string,
	wantTransactions int,
	wantEntries int,
) {
	t.Helper()
	var transactions int
	if err := db.GetContext(ctx, &transactions,
		db.Rebind(`SELECT COUNT(*) FROM ledger_transactions WHERE tenant_id = ?`),
		tenantID,
	); err != nil {
		t.Fatalf("count ledger transactions: %v", err)
	}
	var entries int
	if err := db.GetContext(ctx, &entries,
		db.Rebind(`SELECT COUNT(*) FROM ledger_entries WHERE tenant_id = ?`),
		tenantID,
	); err != nil {
		t.Fatalf("count ledger entries: %v", err)
	}
	if transactions != wantTransactions || entries != wantEntries {
		t.Fatalf("ledger rows = transactions:%d entries:%d, want %d/%d", transactions, entries, wantTransactions, wantEntries)
	}
}

type heldWithdrawalFixture struct {
	Store       *Store
	DB          *basestore.DB
	TenantID    string
	UserID      uuid.UUID
	TreasuryID  uuid.UUID
	FeesID      uuid.UUID
	Hold        *BalanceHold
	Source      *FundingSource
	Reservation *FundingSourceWithdrawalReservationResult
	Destination *WithdrawalDestination
	Params      HeldWithdrawalSettlementParams
}

func newHeldWithdrawalFixture(
	t *testing.T,
	ctx context.Context,
	store *Store,
	db *basestore.DB,
	tenantID string,
	withFee bool,
	withDestination bool,
) heldWithdrawalFixture {
	t.Helper()
	userID := uuid.MustParse("10000000-0000-0000-0000-000000000010")
	treasuryID := uuid.MustParse("e0000000-0000-0000-0000-000000000010")
	feesID := uuid.MustParse("f0000000-0000-0000-0000-000000000010")
	insertSettlementTestWallet(t, ctx, db, tenantID, userID, OwnerTypeUser, "withdrawal-user", 1_000)
	insertSettlementTestWallet(t, ctx, db, tenantID, treasuryID, OwnerTypeSystem, SystemTreasury, 0)
	if withFee {
		insertSettlementTestWallet(t, ctx, db, tenantID, feesID, OwnerTypeSystem, SystemFees, 0)
	}
	insertSettlementTestLimit(t, ctx, db, tenantID, "withdrawal", "AED", 10_000, 10_000, 10_000)
	if _, err := db.ExecContext(ctx, `INSERT INTO psp_configs(
		tenant_id, provider_code, provider_name, api_base_url,
		idempotency_header_name, deposit_response_mapping
	) VALUES($1, 'bankpay', 'Bank Pay', 'https://psp.invalid', 'Idempotency-Key', '{}')`, tenantID); err != nil {
		t.Fatalf("insert withdrawal PSP config: %v", err)
	}

	const principal = int64(700)
	fee := int64(0)
	if withFee {
		fee = 100
	}
	limitUsage := LimitUsageParams{
		TenantID: tenantID, CommandID: "withdrawal:atomic", WalletID: userID,
		TransactionType: "withdrawal", Currency: "AED", Amount: principal,
	}
	if _, err := store.ReserveLimitUsage(ctx, limitUsage); err != nil {
		t.Fatalf("reserve withdrawal limit: %v", err)
	}
	hold, err := store.CreateHold(ctx, HoldParams{
		TenantID: tenantID, WalletID: userID, Amount: principal + fee,
		Reason: "withdrawal", ReferenceType: "withdrawal", ReferenceID: "atomic",
		IdempotencyKey: "atomic:hold", ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create withdrawal hold: %v", err)
	}
	if err := store.CommitHold(ctx, tenantID, hold.ID); err != nil {
		t.Fatalf("commit withdrawal hold: %v", err)
	}
	hold, err = loadSettlementTestHold(ctx, db, tenantID, hold.ID)
	if err != nil {
		t.Fatalf("reload committed withdrawal hold: %v", err)
	}

	source, err := store.UpsertFundingSource(ctx, FundingSource{
		TenantID:           tenantID,
		WalletID:           userID,
		SourceType:         "bank_account",
		PSPProvider:        sql.NullString{String: "bankpay", Valid: true},
		ExternalReference:  sql.NullString{String: "atomic-account", Valid: true},
		VerificationStatus: FundingSourceStatusVerified,
		VerifiedAt:         sql.NullTime{Time: time.Now().UTC(), Valid: true},
		Currency:           "AED",
		SourceDetails:      []byte(`{"account":"atomic"}`),
		SupportsWithdrawal: true,
		WithdrawalMethod:   []byte(`{"account":"atomic"}`),
	})
	if err != nil {
		t.Fatalf("create withdrawal funding source: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		db.Rebind(`UPDATE funding_sources SET total_funded = ? WHERE tenant_id = ? AND id = ?`),
		int64(10_000), tenantID, source.ID,
	); err != nil {
		t.Fatalf("fund withdrawal source: %v", err)
	}
	source.TotalFunded = 10_000
	reservation, err := store.ReserveFundingSourceWithdrawal(ctx, ReserveFundingSourceWithdrawalParams{
		TenantID: tenantID, WorkflowID: "withdrawal-atomic", CandidateSourceIDs: []int64{source.ID},
		WalletID: userID, Amount: principal, Currency: "AED", ProviderCode: "bankpay",
	})
	if err != nil {
		t.Fatalf("reserve withdrawal funding source: %v", err)
	}

	var destination *WithdrawalDestination
	if withDestination {
		destination, err = store.CreateWithdrawalDestination(ctx, WithdrawalDestination{
			TenantID:              tenantID,
			WalletID:              userID,
			DestinationType:       source.SourceType,
			PSPProvider:           source.PSPProvider,
			DestinationDetails:    source.WithdrawalMethod,
			Currency:              source.Currency,
			LinkedFundingSourceID: source.ID,
			IsActive:              true,
		})
		if err != nil {
			t.Fatalf("create withdrawal destination: %v", err)
		}
	}
	transfers := []SettlementTransfer{{
		DebitWalletID: userID, CreditWalletID: treasuryID, Amount: principal, Description: "withdrawal",
	}}
	if withFee {
		transfers = append(transfers, SettlementTransfer{
			DebitWalletID: userID, CreditWalletID: feesID, Amount: fee, Description: "withdrawal fee",
		})
	}
	destinationID := int64(0)
	if destination != nil {
		destinationID = destination.ID
	}
	return heldWithdrawalFixture{
		Store: store, DB: db, TenantID: tenantID,
		UserID: userID, TreasuryID: treasuryID, FeesID: feesID,
		Hold: hold, Source: source, Reservation: reservation, Destination: destination,
		Params: HeldWithdrawalSettlementParams{
			HoldID: hold.ID,
			Settlement: MultiLegSettlementParams{
				TenantID: tenantID, IdempotencyKey: "atomic:withdrawal", Currency: "AED",
				ReferenceType: "withdrawal", ReferenceID: "atomic", Transfers: transfers,
				LimitUsage: limitUsage,
			},
			FundingSourceID:            source.ID,
			FundingReservationID:       reservation.Reservation.ID,
			WithdrawalDestinationID:    destinationID,
			FundingTransferIndex:       0,
			FundingReservationProvider: "bankpay",
		},
	}
}

func loadSettlementTestHold(
	ctx context.Context,
	db *basestore.DB,
	tenantID string,
	holdID int64,
) (*BalanceHold, error) {
	var hold BalanceHold
	err := db.GetContext(ctx, &hold,
		db.Rebind(`SELECT * FROM balance_holds WHERE tenant_id = ? AND id = ?`),
		tenantID,
		holdID,
	)
	return &hold, err
}

func assertHeldWithdrawalCommitted(
	t *testing.T,
	ctx context.Context,
	fixture heldWithdrawalFixture,
	transactionID int64,
	wantEntries int,
) {
	t.Helper()
	assertWalletBalances(t, ctx, fixture.Store, fixture.TenantID, fixture.UserID, 200, 200)
	assertWalletBalances(t, ctx, fixture.Store, fixture.TenantID, fixture.TreasuryID, 700, 700)
	if len(fixture.Params.Settlement.Transfers) == 2 {
		assertWalletBalances(t, ctx, fixture.Store, fixture.TenantID, fixture.FeesID, 100, 100)
	}
	assertHold(t, ctx, fixture.DB, fixture.TenantID, fixture.Hold.ID, HoldStatusCaptured, 0, true)
	assertSettlementRowCounts(t, ctx, fixture.DB, fixture.TenantID, 1, wantEntries)
	wantDestinationLinks := 0
	if fixture.Destination != nil {
		wantDestinationLinks = 1
	}
	assertSettlementLinkCounts(t, ctx, fixture.DB, fixture.TenantID, transactionID, 1, wantDestinationLinks)

	var limitReservation LimitUsageReservation
	if err := fixture.DB.GetContext(ctx, &limitReservation,
		fixture.DB.Rebind(`SELECT * FROM transaction_limit_reservations WHERE tenant_id = ? AND command_id = ?`),
		fixture.TenantID,
		fixture.Params.Settlement.LimitUsage.CommandID,
	); err != nil {
		t.Fatalf("load consumed limit reservation: %v", err)
	}
	if limitReservation.Status != LimitReservationStatusConsumed ||
		!limitReservation.LedgerTransactionID.Valid ||
		limitReservation.LedgerTransactionID.Int64 != transactionID {
		t.Fatalf("limit reservation = %+v, want consumed transaction %d", limitReservation, transactionID)
	}
	var consumedPeriods int
	if err := fixture.DB.GetContext(ctx, &consumedPeriods,
		fixture.DB.Rebind(`SELECT COUNT(*) FROM transaction_limit_period_usage
			WHERE tenant_id = ? AND wallet_id = ? AND transaction_type = 'withdrawal'
			  AND currency = 'AED' AND reserved_amount = 0 AND consumed_amount = 700`),
		fixture.TenantID,
		fixture.UserID,
	); err != nil {
		t.Fatalf("load consumed limit periods: %v", err)
	}
	if consumedPeriods != 2 {
		t.Fatalf("consumed limit periods = %d, want 2", consumedPeriods)
	}

	var fundingReservation FundingSourceWithdrawalReservation
	if err := fixture.DB.GetContext(ctx, &fundingReservation,
		fixture.DB.Rebind(`SELECT * FROM funding_source_withdrawal_reservations WHERE tenant_id = ? AND id = ?`),
		fixture.TenantID,
		fixture.Reservation.Reservation.ID,
	); err != nil {
		t.Fatalf("load consumed funding reservation: %v", err)
	}
	if fundingReservation.Status != FundingSourceReservationConsumed || !fundingReservation.LedgerEntryID.Valid {
		t.Fatalf("funding reservation = %+v, want consumed with ledger entry", fundingReservation)
	}
	var sourceTotal int64
	if err := fixture.DB.GetContext(ctx, &sourceTotal,
		fixture.DB.Rebind(`SELECT total_withdrawn FROM funding_sources WHERE tenant_id = ? AND id = ?`),
		fixture.TenantID,
		fixture.Source.ID,
	); err != nil {
		t.Fatalf("load funding source total: %v", err)
	}
	if sourceTotal != 700 {
		t.Fatalf("funding source total = %d, want 700", sourceTotal)
	}
	if fixture.Destination != nil {
		var destinationTotal int64
		if err := fixture.DB.GetContext(ctx, &destinationTotal,
			fixture.DB.Rebind(`SELECT total_withdrawn FROM withdrawal_destinations WHERE tenant_id = ? AND id = ?`),
			fixture.TenantID,
			fixture.Destination.ID,
		); err != nil {
			t.Fatalf("load destination total: %v", err)
		}
		if destinationTotal != 700 {
			t.Fatalf("destination total = %d, want wallet debit 700", destinationTotal)
		}
	}
}

func assertHeldWithdrawalUncommitted(t *testing.T, ctx context.Context, fixture heldWithdrawalFixture) {
	t.Helper()
	wantAvailable := int64(300)
	if len(fixture.Params.Settlement.Transfers) == 2 {
		wantAvailable = 200
	}
	assertWalletBalances(t, ctx, fixture.Store, fixture.TenantID, fixture.UserID, 1_000, wantAvailable)
	assertWalletBalances(t, ctx, fixture.Store, fixture.TenantID, fixture.TreasuryID, 0, 0)
	if len(fixture.Params.Settlement.Transfers) == 2 {
		assertWalletBalances(t, ctx, fixture.Store, fixture.TenantID, fixture.FeesID, 0, 0)
	}
	holdAmount, err := heldWithdrawalAmount(fixture.Params)
	if err != nil {
		t.Fatalf("sum held withdrawal: %v", err)
	}
	assertHold(t, ctx, fixture.DB, fixture.TenantID, fixture.Hold.ID, HoldStatusCommitted, holdAmount, false)
	assertSettlementRowCounts(t, ctx, fixture.DB, fixture.TenantID, 0, 0)
	assertSettlementLinkCounts(t, ctx, fixture.DB, fixture.TenantID, 0, 0, 0)

	var limitReservation LimitUsageReservation
	if err := fixture.DB.GetContext(ctx, &limitReservation,
		fixture.DB.Rebind(`SELECT * FROM transaction_limit_reservations WHERE tenant_id = ? AND command_id = ?`),
		fixture.TenantID,
		fixture.Params.Settlement.LimitUsage.CommandID,
	); err != nil {
		t.Fatalf("load reserved limit: %v", err)
	}
	if limitReservation.Status != LimitReservationStatusReserved || limitReservation.LedgerTransactionID.Valid {
		t.Fatalf("limit reservation after rollback = %+v", limitReservation)
	}
	var reservedPeriods int
	if err := fixture.DB.GetContext(ctx, &reservedPeriods,
		fixture.DB.Rebind(`SELECT COUNT(*) FROM transaction_limit_period_usage
			WHERE tenant_id = ? AND wallet_id = ? AND transaction_type = 'withdrawal'
			  AND currency = 'AED' AND reserved_amount = 700 AND consumed_amount = 0`),
		fixture.TenantID,
		fixture.UserID,
	); err != nil {
		t.Fatalf("load reserved limit periods: %v", err)
	}
	if reservedPeriods != 2 {
		t.Fatalf("reserved limit periods after rollback = %d, want 2", reservedPeriods)
	}
	var fundingReservation FundingSourceWithdrawalReservation
	if err := fixture.DB.GetContext(ctx, &fundingReservation,
		fixture.DB.Rebind(`SELECT * FROM funding_source_withdrawal_reservations WHERE tenant_id = ? AND id = ?`),
		fixture.TenantID,
		fixture.Reservation.Reservation.ID,
	); err != nil {
		t.Fatalf("load reserved funding source: %v", err)
	}
	if fundingReservation.Status != FundingSourceReservationReserved || fundingReservation.LedgerEntryID.Valid {
		t.Fatalf("funding reservation after rollback = %+v", fundingReservation)
	}
	if fixture.Destination != nil {
		var total int64
		if err := fixture.DB.GetContext(ctx, &total,
			fixture.DB.Rebind(`SELECT total_withdrawn FROM withdrawal_destinations WHERE tenant_id = ? AND id = ?`),
			fixture.TenantID,
			fixture.Destination.ID,
		); err != nil {
			t.Fatalf("load destination after rollback: %v", err)
		}
		if total != 0 {
			t.Fatalf("destination total after rollback = %d, want 0", total)
		}
	}
}

func assertSettlementLinkCounts(
	t *testing.T,
	ctx context.Context,
	db *basestore.DB,
	tenantID string,
	transactionID int64,
	wantFunding int,
	wantDestination int,
) {
	t.Helper()
	querySuffix := ""
	args := []any{tenantID}
	if transactionID > 0 {
		querySuffix = " AND entry.transaction_id = ?"
		args = append(args, transactionID)
	}
	var funding int
	if err := db.GetContext(ctx, &funding, db.Rebind(`SELECT COUNT(*) FROM ledger_funding_links link
		JOIN ledger_entries entry ON entry.tenant_id = link.tenant_id AND entry.id = link.ledger_entry_id
		WHERE link.tenant_id = ?`+querySuffix), args...); err != nil {
		t.Fatalf("count funding links: %v", err)
	}
	var destinations int
	if err := db.GetContext(ctx, &destinations, db.Rebind(`SELECT COUNT(*) FROM ledger_withdrawal_destination_links link
		JOIN ledger_entries entry ON entry.tenant_id = link.tenant_id AND entry.id = link.ledger_entry_id
		WHERE link.tenant_id = ?`+querySuffix), args...); err != nil {
		t.Fatalf("count destination links: %v", err)
	}
	if funding != wantFunding || destinations != wantDestination {
		t.Fatalf("settlement links = funding:%d destination:%d, want %d/%d", funding, destinations, wantFunding, wantDestination)
	}
}
