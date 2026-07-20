package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

var holdTestUserID atomic.Int64

func TestCreateHoldRejectsElapsedDeadlineWithoutReservingFunds(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	source, _ := createHoldTestWallets(t, walletStore, tenantID, "born-expired")
	setWalletBalances(t, ctx, walletStore.DB, tenantID, source.ID, 1_000, 1_000)

	_, err := walletStore.CreateHold(ctx, holdTestParams(tenantID, source.ID, "born-expired", time.Now().UTC().Add(-time.Second)))
	if !errors.Is(err, ErrHoldExpired) {
		t.Fatalf("create elapsed hold error = %v, want %v", err, ErrHoldExpired)
	}
	assertWalletBalances(t, ctx, walletStore, tenantID, source.ID, 1_000, 1_000)
	assertHoldReferenceCount(t, ctx, walletStore.DB, tenantID, "born-expired", 0)
}

func TestExpiredHoldCaptureRestoresAvailableBalanceOnce(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	source, receiver := createHoldTestWallets(t, walletStore, tenantID, "expired")
	setWalletBalances(t, ctx, walletStore.DB, tenantID, source.ID, 1_000, 1_000)

	hold, err := walletStore.CreateHold(ctx, holdTestParams(tenantID, source.ID, "expired", time.Now().UTC().Add(time.Hour)))
	if err != nil {
		t.Fatalf("create hold: %v", err)
	}
	expireHoldDeadline(t, walletStore, tenantID, hold.ID)
	assertWalletBalances(t, ctx, walletStore, tenantID, source.ID, 1_000, 900)

	entry := holdTestEntry(tenantID, source.ID, receiver.ID, "expired")
	_, err = walletStore.PostHeldDoubleEntry(ctx, HeldDoubleEntryParams{HoldID: hold.ID, Entry: entry})
	if !errors.Is(err, ErrHoldExpired) {
		t.Fatalf("capture expired hold error = %v, want %v", err, ErrHoldExpired)
	}
	assertWalletBalances(t, ctx, walletStore, tenantID, source.ID, 1_000, 1_000)
	assertWalletBalances(t, ctx, walletStore, tenantID, receiver.ID, 0, 0)
	assertExpiredHold(t, walletStore, tenantID, hold.ID)

	_, err = walletStore.PostHeldDoubleEntry(ctx, HeldDoubleEntryParams{HoldID: hold.ID, Entry: entry})
	if !errors.Is(err, ErrHoldNotActive) {
		t.Fatalf("expired capture replay error = %v, want %v", err, ErrHoldNotActive)
	}
	if err := walletStore.CommitHold(ctx, tenantID, hold.ID); !errors.Is(err, ErrHoldNotActive) {
		t.Fatalf("commit expired hold error = %v, want %v", err, ErrHoldNotActive)
	}
	assertWalletBalances(t, ctx, walletStore, tenantID, source.ID, 1_000, 1_000)
	exists, err := walletStore.LedgerTransactionExists(ctx, tenantID, entry.IdempotencyKey)
	if err != nil {
		t.Fatalf("check expired ledger transaction: %v", err)
	}
	if exists {
		t.Fatal("expired capture persisted a ledger transaction")
	}
}

func TestExpiredHoldCommitCaptureRaceRestoresAvailableBalanceOnce(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	source, receiver := createHoldTestWallets(t, walletStore, tenantID, "expiry-race")
	setWalletBalances(t, ctx, walletStore.DB, tenantID, source.ID, 1_000, 1_000)

	hold, err := walletStore.CreateHold(ctx, holdTestParams(tenantID, source.ID, "expiry-race", time.Now().UTC().Add(time.Hour)))
	if err != nil {
		t.Fatalf("create hold: %v", err)
	}
	expireHoldDeadline(t, walletStore, tenantID, hold.ID)
	entry := holdTestEntry(tenantID, source.ID, receiver.ID, "expiry-race")

	start := make(chan struct{})
	errorsCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		errorsCh <- walletStore.CommitHold(ctx, tenantID, hold.ID)
	}()
	go func() {
		defer wg.Done()
		<-start
		_, err := walletStore.PostHeldDoubleEntry(ctx, HeldDoubleEntryParams{HoldID: hold.ID, Entry: entry})
		errorsCh <- err
	}()
	close(start)
	wg.Wait()
	close(errorsCh)

	var expired, inactive int
	for err := range errorsCh {
		switch {
		case errors.Is(err, ErrHoldExpired):
			expired++
		case errors.Is(err, ErrHoldNotActive):
			inactive++
		default:
			t.Fatalf("race error = %v, want expired or inactive", err)
		}
	}
	if expired != 1 || inactive != 1 {
		t.Fatalf("race errors: expired=%d inactive=%d, want 1 each", expired, inactive)
	}
	assertWalletBalances(t, ctx, walletStore, tenantID, source.ID, 1_000, 1_000)
	assertWalletBalances(t, ctx, walletStore, tenantID, receiver.ID, 0, 0)
	assertExpiredHold(t, walletStore, tenantID, hold.ID)
}

func TestExpirySweepCommitCaptureRaceRestoresAvailableBalanceOnce(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	source, receiver := createHoldTestWallets(t, walletStore, tenantID, "sweep-race")
	setWalletBalances(t, ctx, walletStore.DB, tenantID, source.ID, 1_000, 1_000)
	hold, err := walletStore.CreateHold(ctx, holdTestParams(tenantID, source.ID, "sweep-race", time.Now().UTC().Add(time.Hour)))
	if err != nil {
		t.Fatalf("create hold: %v", err)
	}
	expireHoldDeadline(t, walletStore, tenantID, hold.ID)
	entry := holdTestEntry(tenantID, source.ID, receiver.ID, "sweep-race")

	type result struct {
		kind  string
		count int
		err   error
	}
	results := make(chan result, 3)
	start := make(chan struct{})
	go func() {
		<-start
		count, err := walletStore.ExpireHolds(ctx, tenantID, 1)
		results <- result{kind: "sweep", count: count, err: err}
	}()
	go func() {
		<-start
		results <- result{kind: "commit", err: walletStore.CommitHold(ctx, tenantID, hold.ID)}
	}()
	go func() {
		<-start
		_, err := walletStore.PostHeldDoubleEntry(ctx, HeldDoubleEntryParams{HoldID: hold.ID, Entry: entry})
		results <- result{kind: "capture", err: err}
	}()
	close(start)

	expiryWinners := 0
	for index := 0; index < 3; index++ {
		result := <-results
		switch {
		case result.kind == "sweep" && result.err == nil && result.count == 1:
			expiryWinners++
		case errors.Is(result.err, ErrHoldExpired):
			expiryWinners++
		case result.kind == "sweep" && result.err == nil && result.count == 0:
		case errors.Is(result.err, ErrHoldNotActive):
		default:
			t.Fatalf("%s race result = count:%d error:%v", result.kind, result.count, result.err)
		}
	}
	if expiryWinners != 1 {
		t.Fatalf("expiry winners = %d, want exactly 1", expiryWinners)
	}
	assertExpiredHold(t, walletStore, tenantID, hold.ID)
	assertWalletBalances(t, ctx, walletStore, tenantID, source.ID, 1_000, 1_000)
	assertWalletBalances(t, ctx, walletStore, tenantID, receiver.ID, 0, 0)
	exists, err := walletStore.LedgerTransactionExists(ctx, tenantID, entry.IdempotencyKey)
	if err != nil {
		t.Fatalf("check race ledger transaction: %v", err)
	}
	if exists {
		t.Fatal("expiry race persisted a ledger transaction")
	}
}

func TestCommittedHoldRemainsCapturableAfterReservationDeadline(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	source, receiver := createHoldTestWallets(t, walletStore, tenantID, "committed")
	setWalletBalances(t, ctx, walletStore.DB, tenantID, source.ID, 1_000, 1_000)

	params := holdTestParams(tenantID, source.ID, "committed", time.Now().UTC().Add(500*time.Millisecond))
	hold, err := walletStore.CreateHold(ctx, params)
	if err != nil {
		t.Fatalf("create hold: %v", err)
	}
	if err := walletStore.CommitHold(ctx, tenantID, hold.ID); err != nil {
		t.Fatalf("commit hold: %v", err)
	}
	if err := walletStore.CommitHold(ctx, tenantID, hold.ID); err != nil {
		t.Fatalf("replay commit hold: %v", err)
	}

	time.Sleep(time.Until(params.ExpiresAt) + 100*time.Millisecond)
	replayed, err := walletStore.CreateHold(ctx, params)
	if err != nil {
		t.Fatalf("replay committed hold after original deadline: %v", err)
	}
	if replayed.ID != hold.ID || replayed.Status != HoldStatusCommitted {
		t.Fatalf("committed hold replay = id:%d status:%s, want id:%d status:%s", replayed.ID, replayed.Status, hold.ID, HoldStatusCommitted)
	}
	entry := holdTestEntry(tenantID, source.ID, receiver.ID, "committed")
	if _, err := walletStore.PostHeldDoubleEntry(ctx, HeldDoubleEntryParams{HoldID: hold.ID, Entry: entry}); err != nil {
		t.Fatalf("capture committed hold: %v", err)
	}
	replayed, err = walletStore.CreateHold(ctx, params)
	if err != nil {
		t.Fatalf("replay captured hold after original deadline: %v", err)
	}
	if replayed.ID != hold.ID || replayed.Status != HoldStatusCaptured {
		t.Fatalf("captured hold replay = id:%d status:%s, want id:%d status:%s", replayed.ID, replayed.Status, hold.ID, HoldStatusCaptured)
	}
	assertWalletBalances(t, ctx, walletStore, tenantID, source.ID, 900, 900)
	assertWalletBalances(t, ctx, walletStore, tenantID, receiver.ID, 100, 100)
	assertHold(t, ctx, walletStore.DB, tenantID, hold.ID, HoldStatusCaptured, 0, true)
}

func TestCreateHoldExactReplayAfterExpiryReturnsTerminalState(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	source, _ := createHoldTestWallets(t, walletStore, tenantID, "expired-replay")
	setWalletBalances(t, ctx, walletStore.DB, tenantID, source.ID, 1_000, 1_000)
	params := holdTestParams(tenantID, source.ID, "expired-replay", time.Now().UTC().Add(500*time.Millisecond))
	hold, err := walletStore.CreateHold(ctx, params)
	if err != nil {
		t.Fatalf("create hold: %v", err)
	}
	time.Sleep(time.Until(params.ExpiresAt) + 100*time.Millisecond)
	if err := walletStore.ReleaseHold(ctx, tenantID, hold.ID); err != nil {
		t.Fatalf("expire hold: %v", err)
	}
	replayed, err := walletStore.CreateHold(ctx, params)
	if err != nil {
		t.Fatalf("replay expired hold: %v", err)
	}
	if replayed.ID != hold.ID || replayed.Status != HoldStatusExpired {
		t.Fatalf("expired hold replay = id:%d status:%s, want id:%d status:%s", replayed.ID, replayed.Status, hold.ID, HoldStatusExpired)
	}
	assertWalletBalances(t, ctx, walletStore, tenantID, source.ID, 1_000, 1_000)
}

func TestReleasePreservesExpiredAndCommittedSemantics(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)

	expiredSource, _ := createHoldTestWallets(t, walletStore, tenantID, "release-expired")
	setWalletBalances(t, ctx, walletStore.DB, tenantID, expiredSource.ID, 1_000, 1_000)
	expired, err := walletStore.CreateHold(ctx, holdTestParams(tenantID, expiredSource.ID, "release-expired", time.Now().UTC().Add(time.Hour)))
	if err != nil {
		t.Fatalf("create hold: %v", err)
	}
	expireHoldDeadline(t, walletStore, tenantID, expired.ID)
	if err := walletStore.ReleaseHold(ctx, tenantID, expired.ID); err != nil {
		t.Fatalf("release elapsed active hold: %v", err)
	}
	if err := walletStore.ReleaseHold(ctx, tenantID, expired.ID); err != nil {
		t.Fatalf("replay expired hold release: %v", err)
	}
	assertExpiredHold(t, walletStore, tenantID, expired.ID)
	assertWalletBalances(t, ctx, walletStore, tenantID, expiredSource.ID, 1_000, 1_000)

	committedSource, _ := createHoldTestWallets(t, walletStore, tenantID, "release-committed")
	setWalletBalances(t, ctx, walletStore.DB, tenantID, committedSource.ID, 1_000, 1_000)
	committed, err := walletStore.CreateHold(ctx, holdTestParams(tenantID, committedSource.ID, "release-committed", time.Now().UTC().Add(time.Hour)))
	if err != nil {
		t.Fatalf("create committed hold: %v", err)
	}
	if err := walletStore.CommitHold(ctx, tenantID, committed.ID); err != nil {
		t.Fatalf("commit hold: %v", err)
	}
	if err := walletStore.ReleaseHold(ctx, tenantID, committed.ID); err != nil {
		t.Fatalf("release committed hold: %v", err)
	}
	if err := walletStore.ReleaseHold(ctx, tenantID, committed.ID); err != nil {
		t.Fatalf("replay committed hold release: %v", err)
	}
	assertWalletBalances(t, ctx, walletStore, tenantID, committedSource.ID, 1_000, 1_000)
	var released BalanceHold
	stmt := walletStore.DB.Rebind(`SELECT * FROM balance_holds WHERE tenant_id = ? AND id = ?`)
	if err := walletStore.DB.GetContext(ctx, &released, stmt, tenantID, committed.ID); err != nil {
		t.Fatalf("load released committed hold: %v", err)
	}
	if released.Status != HoldStatusReleased || released.AmountRemaining != 0 || !released.CommittedAt.Valid || !released.ReleasedAt.Valid || released.ExpiredAt.Valid {
		t.Fatalf("released committed hold = status:%s remaining:%d committed:%v released:%v expired:%v",
			released.Status, released.AmountRemaining, released.CommittedAt.Valid, released.ReleasedAt.Valid, released.ExpiredAt.Valid)
	}
}

func TestCommitCaptureRaceDebitsExactlyOnce(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	source, receiver := createHoldTestWallets(t, walletStore, tenantID, "commit-race")
	setWalletBalances(t, ctx, walletStore.DB, tenantID, source.ID, 1_000, 1_000)

	hold, err := walletStore.CreateHold(ctx, holdTestParams(tenantID, source.ID, "commit-race", time.Now().UTC().Add(time.Hour)))
	if err != nil {
		t.Fatalf("create hold: %v", err)
	}
	entry := holdTestEntry(tenantID, source.ID, receiver.ID, "commit-race")

	start := make(chan struct{})
	commitErr := make(chan error, 1)
	captureErr := make(chan error, 1)
	go func() {
		<-start
		commitErr <- walletStore.CommitHold(ctx, tenantID, hold.ID)
	}()
	go func() {
		<-start
		_, err := walletStore.PostHeldDoubleEntry(ctx, HeldDoubleEntryParams{HoldID: hold.ID, Entry: entry})
		captureErr <- err
	}()
	close(start)

	if err := <-captureErr; err != nil {
		t.Fatalf("capture race error: %v", err)
	}
	if err := <-commitErr; err != nil && !errors.Is(err, ErrHoldNotActive) {
		t.Fatalf("commit race error = %v, want nil or %v", err, ErrHoldNotActive)
	}
	assertWalletBalances(t, ctx, walletStore, tenantID, source.ID, 900, 900)
	assertWalletBalances(t, ctx, walletStore, tenantID, receiver.ID, 100, 100)
	assertHold(t, ctx, walletStore.DB, tenantID, hold.ID, HoldStatusCaptured, 0, true)

	replayed, err := walletStore.PostHeldDoubleEntry(ctx, HeldDoubleEntryParams{HoldID: hold.ID, Entry: entry})
	if err != nil {
		t.Fatalf("capture replay: %v", err)
	}
	if !replayed.Existing {
		t.Fatal("capture replay Existing = false, want true")
	}
	assertWalletBalances(t, ctx, walletStore, tenantID, source.ID, 900, 900)
}

func TestExpireHoldsProcessesBoundedActiveReservationsOnly(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	expired := make([]struct {
		hold   *BalanceHold
		wallet *Wallet
	}, 0, 3)
	for index := 0; index < 3; index++ {
		suffix := fmt.Sprintf("cleanup-%d", index)
		wallet, _ := createHoldTestWallets(t, walletStore, tenantID, suffix)
		setWalletBalances(t, ctx, walletStore.DB, tenantID, wallet.ID, 1_000, 1_000)
		hold, err := walletStore.CreateHold(ctx, holdTestParams(tenantID, wallet.ID, suffix, time.Now().UTC().Add(time.Hour)))
		if err != nil {
			t.Fatalf("create cleanup hold %d: %v", index, err)
		}
		expireHoldDeadline(t, walletStore, tenantID, hold.ID)
		expired = append(expired, struct {
			hold   *BalanceHold
			wallet *Wallet
		}{hold: hold, wallet: wallet})
	}

	committedWallet, _ := createHoldTestWallets(t, walletStore, tenantID, "cleanup-committed")
	setWalletBalances(t, ctx, walletStore.DB, tenantID, committedWallet.ID, 1_000, 1_000)
	committed, err := walletStore.CreateHold(ctx, holdTestParams(tenantID, committedWallet.ID, "cleanup-committed", time.Now().UTC().Add(time.Hour)))
	if err != nil {
		t.Fatalf("create committed cleanup hold: %v", err)
	}
	if err := walletStore.CommitHold(ctx, tenantID, committed.ID); err != nil {
		t.Fatalf("commit cleanup hold: %v", err)
	}
	expireHoldDeadline(t, walletStore, tenantID, committed.ID)

	if count, err := walletStore.ExpireHolds(ctx, tenantID, 2); err != nil || count != 2 {
		t.Fatalf("first expiry sweep = (%d, %v), want (2, nil)", count, err)
	}
	if count, err := walletStore.ExpireHolds(ctx, tenantID, 2); err != nil || count != 1 {
		t.Fatalf("second expiry sweep = (%d, %v), want (1, nil)", count, err)
	}
	if count, err := walletStore.ExpireHolds(ctx, tenantID, 2); err != nil || count != 0 {
		t.Fatalf("third expiry sweep = (%d, %v), want (0, nil)", count, err)
	}
	for _, item := range expired {
		assertExpiredHold(t, walletStore, tenantID, item.hold.ID)
		assertWalletBalances(t, ctx, walletStore, tenantID, item.wallet.ID, 1_000, 1_000)
	}
	assertWalletBalances(t, ctx, walletStore, tenantID, committedWallet.ID, 1_000, 900)
	var storedCommitted BalanceHold
	stmt := walletStore.DB.Rebind(`SELECT * FROM balance_holds WHERE tenant_id = ? AND id = ?`)
	if err := walletStore.DB.GetContext(ctx, &storedCommitted, stmt, tenantID, committed.ID); err != nil {
		t.Fatalf("load committed hold after cleanup: %v", err)
	}
	if storedCommitted.Status != HoldStatusCommitted || !storedCommitted.CommittedAt.Valid || storedCommitted.ExpiredAt.Valid {
		t.Fatalf("committed hold changed by cleanup: %+v", storedCommitted)
	}
}

func TestCreateHoldReplayAndReleaseShareAcyclicLockOrder(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	source, _ := createHoldTestWallets(t, walletStore, tenantID, "replay-release")
	setWalletBalances(t, ctx, walletStore.DB, tenantID, source.ID, 1_000, 1_000)
	params := holdTestParams(tenantID, source.ID, "replay-release", time.Now().UTC().Add(time.Hour))
	hold, err := walletStore.CreateHold(ctx, params)
	if err != nil {
		t.Fatalf("create hold: %v", err)
	}

	raceCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	results := make(chan error, 2)
	start := make(chan struct{})
	go func() {
		<-start
		_, err := walletStore.CreateHold(raceCtx, params)
		results <- err
	}()
	go func() {
		<-start
		results <- walletStore.ReleaseHold(raceCtx, tenantID, hold.ID)
	}()
	close(start)

	for index := 0; index < 2; index++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("replay/release race error: %v", err)
			}
		case <-raceCtx.Done():
			t.Fatalf("replay/release race did not complete: %v", raceCtx.Err())
		}
	}
	assertWalletBalances(t, ctx, walletStore, tenantID, source.ID, 1_000, 1_000)
}

func createHoldTestWallets(t *testing.T, walletStore *Store, tenantID, suffix string) (*Wallet, *Wallet) {
	t.Helper()
	ctx := t.Context()
	source, err := walletStore.EnsureWallet(ctx, EnsureWalletParams{
		TenantID: tenantID, OwnerType: OwnerTypeUser, OwnerID: "hold-source-" + suffix,
		UserID: holdTestUserID.Add(1) + 100, Currency: "AED", KYCTier: KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure source wallet: %v", err)
	}
	receiver, err := walletStore.EnsureWallet(ctx, EnsureWalletParams{
		TenantID: tenantID, OwnerType: OwnerTypeSystem, OwnerID: "hold-receiver-" + suffix,
		Currency: "AED", KYCTier: KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure receiver wallet: %v", err)
	}
	return source, receiver
}

func holdTestParams(tenantID string, walletID uuid.UUID, suffix string, expiresAt time.Time) HoldParams {
	return HoldParams{
		TenantID: tenantID, WalletID: walletID, Amount: 100, Reason: "hold test",
		ReferenceType: "withdrawal", ReferenceID: suffix, IdempotencyKey: suffix + ":hold", ExpiresAt: expiresAt,
	}
}

func holdTestEntry(tenantID string, debitID, creditID uuid.UUID, suffix string) DoubleEntryParams {
	return DoubleEntryParams{
		TenantID: tenantID, IdempotencyKey: suffix + ":ledger", Currency: "AED",
		ReferenceType: "withdrawal", ReferenceID: suffix, DebitWalletID: debitID,
		CreditWalletID: creditID, Amount: 100, Description: "hold test",
	}
}

func assertExpiredHold(t *testing.T, walletStore *Store, tenantID string, holdID int64) {
	t.Helper()
	var hold BalanceHold
	stmt := walletStore.DB.Rebind(`SELECT * FROM balance_holds WHERE tenant_id = ? AND id = ?`)
	if err := walletStore.DB.GetContext(t.Context(), &hold, stmt, tenantID, holdID); err != nil {
		t.Fatalf("load hold: %v", err)
	}
	if hold.Status != HoldStatusExpired || hold.AmountRemaining != 0 || !hold.ExpiredAt.Valid {
		t.Fatalf("expired hold = status:%s remaining:%d expired_at:%v", hold.Status, hold.AmountRemaining, hold.ExpiredAt.Valid)
	}
	if hold.CommittedAt.Valid || hold.CapturedAt.Valid {
		t.Fatalf("expired hold has terminal evidence: committed_at=%v captured_at=%v", hold.CommittedAt.Valid, hold.CapturedAt.Valid)
	}
}

func expireHoldDeadline(t *testing.T, walletStore *Store, tenantID string, holdID int64) {
	t.Helper()
	stmt := walletStore.DB.Rebind(`UPDATE balance_holds
		SET expires_at = clock_timestamp() - INTERVAL '1 second',
			created_at = clock_timestamp() - INTERVAL '2 seconds'
		WHERE tenant_id = ? AND id = ?`)
	if _, err := walletStore.DB.ExecContext(t.Context(), stmt, tenantID, holdID); err != nil {
		t.Fatalf("expire hold deadline: %v", err)
	}
}
