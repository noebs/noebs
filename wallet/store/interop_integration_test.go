package store

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/adonese/noebs/internal/testdb"
	basestore "github.com/adonese/noebs/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// This suite uses real PostgreSQL logins, not SET ROLE or the migration login,
// for every application operation. NOEBS_TEST_POSTGRES_URL may point only at a
// disposable test cluster: the shared testdb fixture resets wallet_ledger.
func TestInteropLedgerRegression(t *testing.T) {
	f := newInteropFixture(t)

	t.Run("runtime admits commands but cannot manufacture protocol outcomes", func(t *testing.T) {
		n := f.tenant(t, "authority", false)
		q := n.outgoingQuote(t, 1000)
		for _, role := range []struct {
			store *Store
			name  string
		}{{f.runtime, "wallet_ledger_runtime"}, {f.worker, "wallet_ledger_worker"}} {
			var actual string
			interopMust(t, role.store.DB.GetContext(f.ctx, &actual, `SELECT current_user`))
			if actual != role.name {
				t.Fatalf("actual login = %q, want %q", actual, role.name)
			}
		}
		_, err := f.runtime.ClaimInteropQuote(f.ctx, n.id, uuid.New())
		interopSQLState(t, err, "42501")
		_, err = f.runtime.DB.ExecContext(f.ctx, `UPDATE interop_quotes SET status='FAILED' WHERE id=$1`, q.ID)
		interopSQLState(t, err, "42501")
		incoming := n.quote("IN", 100)
		_, err = f.runtime.CreateInteropQuote(f.ctx, incoming)
		interopSQLState(t, err, "42501")
		n.ready(t, q)
		tr := n.request(t, q)
		_, err = f.runtime.DB.ExecContext(f.ctx, `INSERT INTO interop_transfers(id,tenant_id,quote_id,owner_id,idempotency_key,status) VALUES($1,$2,$3,$4,$5,'PENDING')`, tr.ID, n.id, q.ID, n.user.OwnerID, "forged")
		interopSQLState(t, err, "42501")
		token := n.claim(t, tr)
		_, err = f.runtime.ArmInteropTransfer(f.ctx, n.id, tr.ID, token)
		interopSQLState(t, err, "42501")
		interopSQLState(t, f.runtime.MarkInteropSubmitted(f.ctx, n.id, tr.ID, token, RawJSON(`{}`)), "42501")
		_, err = f.runtime.RecordInteropEvent(f.ctx, n.event(tr, "COMMITTED"))
		interopSQLState(t, err, "42501")
		_, err = f.worker.DB.ExecContext(f.ctx, `UPDATE interop_bindings SET enabled=false WHERE tenant_id=$1`, n.id)
		interopSQLState(t, err, "42501")
		n.balance(t, n.user.ID, 10000, 10000)
		n.count(t, `SELECT count(*) FROM balance_holds WHERE tenant_id=$1`, 0)
		n.count(t, `SELECT count(*) FROM ledger_transactions WHERE tenant_id=$1 AND reference_type='mojaloop'`, 0)
	})

	t.Run("stale quote claimant cannot replace current agreement", func(t *testing.T) {
		n := f.tenant(t, "quote-lease", false)
		q := n.outgoingQuote(t, 800)
		old, fresh := uuid.New(), uuid.New()
		_, err := f.worker.ClaimInteropQuote(f.ctx, n.id, old)
		interopMust(t, err)
		_, err = f.migrate.ExecContext(f.ctx, `UPDATE interop_quotes SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, q.ID)
		interopMust(t, err)
		claimed, err := f.worker.ClaimInteropQuote(f.ctx, n.id, fresh)
		interopMust(t, err)
		if claimed.ID != q.ID || claimed.LeaseToken.UUID != fresh {
			t.Fatal("expired quote was not reclaimed with the new lease")
		}
		interopError(t, f.worker.CompleteInteropQuote(f.ctx, n.id, q.ID, old, nil, nil, time.Time{}, "old_timeout"), ErrInteropState)
		interopMust(t, f.worker.CompleteInteropQuote(f.ctx, n.id, q.ID, fresh, RawJSON(`{"agreement":"current"}`), RawJSON(`{"currentState":"WAITING_FOR_QUOTE_ACCEPTANCE"}`), time.Now().Add(time.Hour), ""))
		interopError(t, f.worker.CompleteInteropQuote(f.ctx, n.id, q.ID, old, RawJSON(`{"agreement":"stale"}`), RawJSON(`{}`), time.Now().Add(time.Hour), ""), ErrInteropState)
		stored, err := f.runtime.GetInteropQuote(f.ctx, n.id, q.ID)
		interopMust(t, err)
		if stored.Status != "READY" || string(stored.Response) != `{"agreement":"current"}` {
			t.Fatalf("stale worker changed agreement: status=%s response=%s", stored.Status, stored.Response)
		}
		changed := *q
		changed.Amount++
		_, err = f.runtime.CreateInteropQuote(f.ctx, changed)
		interopError(t, err, ErrInteropConflict)
	})

	t.Run("concurrent retries arm and capture exactly once", func(t *testing.T) {
		n := f.tenant(t, "capture", false)
		q := n.outgoingQuote(t, 1000)
		n.ready(t, q)
		tr := n.request(t, q)
		token := n.claim(t, tr)
		armed := make([]*InteropTransfer, 12)
		for _, err := range interopConcurrent(12, func(i int) error {
			var err error
			armed[i], err = f.worker.ArmInteropTransfer(f.ctx, n.id, tr.ID, token)
			return err
		}) {
			interopMust(t, err)
		}
		for _, a := range armed {
			if !a.HoldID.Valid || a.HoldID != armed[0].HoldID {
				t.Fatal("repeated arm created distinct holds")
			}
		}
		n.balance(t, n.user.ID, 10000, 9000)
		n.count(t, `SELECT count(*) FROM balance_holds WHERE tenant_id=$1`, 1)
		n.usage(t, q, "reserved", 1000, 0)
		prepare := RawJSON(" {\n  \"transferId\": \"" + tr.ID.String() + "\", \"sequence\": 1\n}\n")
		interopMust(t, f.worker.MarkInteropSubmitted(f.ctx, n.id, tr.ID, token, prepare))
		stored := n.transfer(t, tr.ID)
		if !bytes.Equal(stored.OriginalPrepare, prepare) {
			t.Fatal("submission did not preserve the original prepare bytes")
		}
		setWalletStatus(t, f.ctx, f.migrate, n.id, n.user.ID, WalletStatusFrozen)
		event := n.event(tr, "COMMITTED")
		ids := make([]int64, 12)
		for _, err := range interopConcurrent(12, func(i int) error {
			var err error
			ids[i], err = f.worker.RecordInteropEvent(f.ctx, event)
			return err
		}) {
			interopMust(t, err)
		}
		for _, id := range ids {
			if id != ids[0] {
				t.Fatal("duplicate callback created distinct receipts")
			}
		}
		for _, err := range interopConcurrent(12, func(int) error { return f.worker.ApplyInteropEvent(f.ctx, n.id, ids[0]) }) {
			interopMust(t, err)
		}
		n.settled(t, tr.ID, "SUCCEEDED", "COMMITTED", 9000, 9000)
		n.balance(t, n.clearing.ID, 1000, 1000)
		n.usage(t, q, "consumed", 0, 1000)
		n.hold(t, armed[0].HoldID.Int64, HoldStatusCaptured, 0)
		n.conserved(t)
	})

	t.Run("competing payments cannot overreserve balance or daily limit", func(t *testing.T) {
		for _, limited := range []bool{false, true} {
			name, amount := "balance-race", int64(7000)
			if limited {
				name, amount = "limit-race", 700
			}
			t.Run(name, func(t *testing.T) {
				n := f.tenant(t, name, false)
				if limited {
					_, err := f.migrate.ExecContext(f.ctx, `UPDATE transaction_limits SET per_transaction_limit=1000,daily_limit=1000,monthly_limit=1000 WHERE tenant_id=$1`, n.id)
					interopMust(t, err)
				}
				transfers := make([]*InteropTransfer, 2)
				tokens := make([]uuid.UUID, 2)
				for i := range transfers {
					q := n.outgoingQuote(t, amount)
					n.ready(t, q)
					transfers[i] = n.request(t, q)
					tokens[i] = n.claim(t, transfers[i])
				}
				errs := interopConcurrent(2, func(i int) error {
					_, err := f.worker.ArmInteropTransfer(f.ctx, n.id, transfers[i].ID, tokens[i])
					return err
				})
				ok := 0
				for _, err := range errs {
					if err == nil {
						ok++
					} else if limited {
						var exceeded TransactionLimitExceededError
						if !errors.As(err, &exceeded) {
							t.Fatalf("competing limit reservation: %v", err)
						}
					} else {
						interopError(t, err, ErrInsufficientFunds)
					}
				}
				if ok != 1 {
					t.Fatalf("successful competing reservations = %d, want 1", ok)
				}
				n.balance(t, n.user.ID, 10000, 10000-amount)
				n.count(t, `SELECT count(*) FROM balance_holds WHERE tenant_id=$1`, 1)
				n.count(t, `SELECT count(*) FROM transaction_limit_reservations WHERE tenant_id=$1`, 1)
				n.conserved(t)
			})
		}
	})

	t.Run("durable commitment fences abort while posting fails", func(t *testing.T) {
		n := f.tenant(t, "finality", false)
		q, tr, token := n.armed(t, 1000)
		interopMust(t, f.worker.MarkInteropSubmitted(f.ctx, n.id, tr.ID, token, RawJSON(`{"prepare":"fixed"}`)))
		setWalletStatus(t, f.ctx, f.migrate, n.id, n.clearing.ID, WalletStatusFrozen)
		event := n.event(tr, "COMMITTED")
		id, err := f.worker.RecordInteropEvent(f.ctx, event)
		interopMust(t, err)
		interopError(t, f.worker.ApplyInteropEvent(f.ctx, n.id, id), ErrWalletInactive)
		pending := n.transfer(t, tr.ID)
		if pending.HubState != "COMMITTED" || pending.LedgerTransactionID.Valid {
			t.Fatalf("commitment lost after posting error: %+v", pending)
		}
		n.balance(t, n.user.ID, 10000, 9000)
		n.hold(t, tr.HoldID.Int64, HoldStatusCommitted, 1000)
		n.usage(t, q, "reserved", 1000, 0)
		abortID, err := f.worker.RecordInteropEvent(f.ctx, n.event(tr, "ABORTED"))
		interopError(t, err, ErrInteropConflict)
		if abortID <= 0 {
			t.Fatal("conflicting terminal event was not retained")
		}
		var quarantined bool
		interopMust(t, f.worker.DB.GetContext(f.ctx, &quarantined, `SELECT quarantined FROM interop_inbox WHERE id=$1`, abortID))
		if !quarantined {
			t.Fatal("conflicting abort remains eligible for application")
		}
		if err := f.worker.ApplyInteropEvent(f.ctx, n.id, abortID); err == nil {
			t.Fatal("quarantined abort was accepted")
		}
		ids, err := f.worker.PendingInteropEvents(f.ctx, n.id)
		interopMust(t, err)
		if !slices.Equal(ids, []int64{id}) {
			t.Fatalf("pending receipts = %v, want only committed receipt %d", ids, id)
		}
		interopMust(t, f.worker.DeferInteropEvent(f.ctx, n.id, id))
		ids, err = f.worker.PendingInteropEvents(f.ctx, n.id)
		interopMust(t, err)
		if len(ids) != 0 {
			t.Fatalf("deferred or quarantined receipt still at head of queue: %v", ids)
		}
		var recorded struct {
			Payload RawJSON `db:"payload"`
			Digest  string  `db:"payload_sha256"`
		}
		interopMust(t, f.worker.DB.GetContext(f.ctx, &recorded, `SELECT payload,payload_sha256 FROM interop_inbox WHERE id=$1`, id))
		digest := sha256.Sum256(event.Payload)
		if !bytes.Equal(recorded.Payload, event.Payload) || recorded.Digest != hex.EncodeToString(digest[:]) {
			t.Fatal("receipt bytes no longer match their audit digest")
		}
		setWalletStatus(t, f.ctx, f.migrate, n.id, n.clearing.ID, WalletStatusActive)
		restarted := f.openRole(t, "wallet_ledger_worker")
		interopMust(t, restarted.ApplyInteropEvent(f.ctx, n.id, id))
		n.settled(t, tr.ID, "SUCCEEDED", "COMMITTED", 9000, 9000)
		n.usage(t, q, "consumed", 0, 1000)
		n.conserved(t)
	})

	t.Run("incoming reservation stays unspendable and replays after restart", func(t *testing.T) {
		n := f.tenant(t, "incoming", false)
		q := n.incomingQuote(t, 700)
		prepare, original := n.incomingPayloads(q)
		response, err := f.worker.ReserveInteropIncoming(f.ctx, n.id, q.ID, prepare, original, q.ExpiresAt.Time)
		interopMust(t, err)
		if !bytes.Equal(response, original) {
			t.Fatal("initial reservation changed the original response")
		}
		n.balance(t, n.user.ID, 10000, 10000)
		n.usage(t, q, "reserved", 700, 0)
		n.count(t, `SELECT count(*) FROM ledger_transactions WHERE tenant_id=$1 AND reference_type='mojaloop'`, 0)
		_, err = f.worker.ReserveInteropIncoming(f.ctx, n.id, q.ID, RawJSON(`{"changed":"prepare"}`), original, q.ExpiresAt.Time)
		interopError(t, err, ErrInteropConflict)
		// Expired agreement and a disabled admission switch cannot invalidate an
		// already acknowledged reservation or alter its response on replay.
		_, err = f.migrate.ExecContext(f.ctx, `UPDATE interop_quotes SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, q.ID)
		interopMust(t, err)
		_, err = f.migrate.ExecContext(f.ctx, `UPDATE interop_bindings SET enabled=false WHERE tenant_id=$1`, n.id)
		interopMust(t, err)
		restarted := f.openRole(t, "wallet_ledger_worker")
		changedResponse := RawJSON(`{"transferState":"COMMITTED","completedTimestamp":"2099-01-01T00:00:00Z"}`)
		response, err = restarted.ReserveInteropIncoming(f.ctx, n.id, q.ID, prepare, changedResponse, q.ExpiresAt.Time)
		interopMust(t, err)
		if !bytes.Equal(response, original) {
			t.Fatal("lost acknowledgement replay changed the RESERVED response")
		}
		tr := n.transfer(t, q.TransferID)
		id, err := restarted.RecordInteropEvent(f.ctx, n.event(tr, "COMMITTED"))
		interopMust(t, err)
		interopMust(t, restarted.ApplyInteropEvent(f.ctx, n.id, id))
		response, err = restarted.ReserveInteropIncoming(f.ctx, n.id, q.ID, prepare, changedResponse, q.ExpiresAt.Time)
		interopMust(t, err)
		if !bytes.Equal(response, original) {
			t.Fatal("post-commit replay changed original response bytes/timestamp/state")
		}
		interopMust(t, restarted.ApplyInteropEvent(f.ctx, n.id, id))
		n.settled(t, q.TransferID, "SUCCEEDED", "COMMITTED", 10700, 10700)
		n.balance(t, n.clearing.ID, -700, -700)
		n.usage(t, q, "consumed", 0, 700)
		n.conserved(t)
	})

	t.Run("incoming committed after freeze posts only to suspense", func(t *testing.T) {
		n := f.tenant(t, "suspense", false)
		q := n.incomingQuote(t, 700)
		prepare, original := n.incomingPayloads(q)
		_, err := f.worker.ReserveInteropIncoming(f.ctx, n.id, q.ID, prepare, original, q.ExpiresAt.Time)
		interopMust(t, err)
		setWalletStatus(t, f.ctx, f.migrate, n.id, n.user.ID, WalletStatusFrozen)
		tr := n.transfer(t, q.TransferID)
		id, err := f.worker.RecordInteropEvent(f.ctx, n.event(tr, "COMMITTED"))
		interopMust(t, err)
		interopMust(t, f.worker.ApplyInteropEvent(f.ctx, n.id, id))
		interopMust(t, f.worker.ApplyInteropEvent(f.ctx, n.id, id))
		n.settled(t, tr.ID, "SUSPENSE", "COMMITTED", 10000, 10000)
		n.balance(t, n.suspense.ID, 700, 700)
		n.balance(t, n.clearing.ID, -700, -700)
		n.usage(t, q, "consumed", 0, 700)
		n.conserved(t)
	})

	t.Run("incoming prepare deadline rejects without retaining usage", func(t *testing.T) {
		n := f.tenant(t, "prepare-deadline", false)
		q := n.incomingQuote(t, 100)
		prepare, response := n.incomingPayloads(q)
		for _, expiry := range []time.Time{time.Now().Add(-time.Second), q.ExpiresAt.Time.Add(time.Second)} {
			_, err := f.worker.ReserveInteropIncoming(f.ctx, n.id, q.ID, prepare, response, expiry)
			interopError(t, err, ErrInteropQuoteExpired)
			n.count(t, `SELECT count(*) FROM interop_transfers WHERE tenant_id=$1`, 0)
			n.count(t, `SELECT count(*) FROM transaction_limit_reservations WHERE tenant_id=$1`, 0)
			n.count(t, `SELECT count(*) FROM transaction_limit_period_usage WHERE tenant_id=$1`, 0)
		}
		_, err := f.worker.ReserveInteropIncoming(f.ctx, n.id, q.ID, prepare, response, q.ExpiresAt.Time)
		interopMust(t, err)
		n.usage(t, q, "reserved", 100, 0)
		n.balance(t, n.user.ID, 10000, 10000)
	})

	t.Run("authoritative abort releases reservations once", func(t *testing.T) {
		n := f.tenant(t, "abort", false)
		q, tr, token := n.armed(t, 1000)
		interopMust(t, f.worker.MarkInteropSubmitted(f.ctx, n.id, tr.ID, token, RawJSON(`{"prepare":"fixed"}`)))
		interopError(t, f.worker.RejectInteropBeforeSubmit(f.ctx, n.id, tr.ID, token, "timeout"), ErrInteropState)
		event := n.event(tr, "ABORTED")
		id, err := f.worker.RecordInteropEvent(f.ctx, event)
		interopMust(t, err)
		for _, err := range interopConcurrent(8, func(int) error { return f.worker.ApplyInteropEvent(f.ctx, n.id, id) }) {
			interopMust(t, err)
		}
		n.balance(t, n.user.ID, 10000, 10000)
		n.hold(t, tr.HoldID.Int64, HoldStatusReleased, 0)
		n.usage(t, q, "released", 0, 0)
		failed := n.transfer(t, tr.ID)
		if failed.Status != "FAILED" || failed.HubState != "ABORTED" || failed.LedgerTransactionID.Valid {
			t.Fatalf("abort outcome = %+v", failed)
		}
		n.count(t, `SELECT count(*) FROM ledger_transactions WHERE tenant_id=$1 AND reference_type='mojaloop'`, 0)
		n.conserved(t)
	})

	t.Run("committed hold survives expired quote and sweeper restart", func(t *testing.T) {
		n := f.tenant(t, "expiry", false)
		q, tr, token := n.armed(t, 1000)
		interopMust(t, f.worker.MarkInteropSubmitted(f.ctx, n.id, tr.ID, token, RawJSON(`{"prepare":"fixed"}`)))
		_, err := f.worker.CreateHold(f.ctx, HoldParams{TenantID: n.id, WalletID: n.user.ID, Amount: 200, Reason: "test active hold", ReferenceType: "test", ReferenceID: "active", IdempotencyKey: "active", ExpiresAt: time.Now().Add(time.Hour)})
		interopMust(t, err)
		_, err = f.migrate.ExecContext(f.ctx, `UPDATE balance_holds SET created_at=clock_timestamp()-interval '2 hours',expires_at=clock_timestamp()-interval '1 hour' WHERE tenant_id=$1`, n.id)
		interopMust(t, err)
		_, err = f.migrate.ExecContext(f.ctx, `UPDATE interop_quotes SET expires_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, q.ID)
		interopMust(t, err)
		restarted := f.openRole(t, "wallet_ledger_worker")
		expired, err := restarted.ExpireHolds(f.ctx, n.id, 100)
		interopMust(t, err)
		if expired != 1 {
			t.Fatalf("sweeper expired %d holds, want only the ordinary active hold", expired)
		}
		interopMust(t, restarted.DeferInteropTransfer(f.ctx, n.id, tr.ID, token, "lost_callback"))
		n.balance(t, n.user.ID, 10000, 9000)
		n.hold(t, tr.HoldID.Int64, HoldStatusCommitted, 1000)
		n.usage(t, q, "reserved", 1000, 0)
		if current := n.transfer(t, tr.ID); current.Status != "IN_DOUBT" {
			t.Fatalf("lost callback state = %s, want IN_DOUBT", current.Status)
		}
		n.conserved(t)
	})

	t.Run("tenant money and transfer bindings are enforced in SQL", func(t *testing.T) {
		a, b := f.tenant(t, "tenant-one", false), f.tenant(t, "tenant-two", false)
		qa, ta, _ := a.armed(t, 100)
		_, tb, _ := b.armed(t, 100)
		_, err := f.runtime.GetInteropQuote(f.ctx, b.id, qa.ID)
		interopError(t, err, ErrInteropNotFound)
		_, err = f.runtime.GetInteropTransfer(f.ctx, b.id, a.user.OwnerID, ta.ID, "")
		interopError(t, err, ErrInteropNotFound)
		_, err = f.runtime.GetInteropTransfer(f.ctx, a.id, "another-customer", ta.ID, "")
		interopError(t, err, ErrInteropNotFound)
		_, err = f.worker.DB.ExecContext(f.ctx, `UPDATE interop_transfers SET hold_id=$1 WHERE id=$2`, tb.HoldID.Int64, ta.ID)
		interopSQLState(t, err, "23503")
		_, err = f.worker.DB.ExecContext(f.ctx, `UPDATE interop_transfers SET ledger_transaction_id=$1 WHERE id=$2`, b.seedID, ta.ID)
		interopSQLState(t, err, "23503")
		_, err = f.runtime.DB.ExecContext(f.ctx, `INSERT INTO interop_quotes(id,transfer_id,tenant_id,owner_id,wallet_id,idempotency_key,amount,currency,currency_unit_version_id,request) VALUES($1,$2,$3,$4,$5,$6,100,'SDG',$7,'{}')`, uuid.New(), uuid.New(), b.id, a.user.OwnerID, a.user.ID, "foreign-wallet", a.unit)
		interopSQLState(t, err, "23503")
		aedUnit := testCurrencyUnitID(t, f.ctx, f.worker, "AED")
		_, err = f.runtime.DB.ExecContext(f.ctx, `INSERT INTO interop_quotes(id,transfer_id,tenant_id,owner_id,wallet_id,idempotency_key,amount,currency,currency_unit_version_id,request) VALUES($1,$2,$3,$4,$5,$6,100,'AED',$7,'{}')`, uuid.New(), uuid.New(), a.id, a.user.OwnerID, a.user.ID, "wrong-unit", aedUnit)
		interopSQLState(t, err, "23503")
		unconsumed := a.outgoingQuote(t, 100)
		a.ready(t, unconsumed)
		_, err = f.runtime.DB.ExecContext(f.ctx, `INSERT INTO interop_transfers(id,tenant_id,quote_id,owner_id,idempotency_key) VALUES($1,$2,$3,$4,$5)`, uuid.New(), a.id, unconsumed.ID, a.user.OwnerID, "wrong-wire-id")
		interopSQLState(t, err, "23503")
		_, err = f.migrate.ExecContext(f.ctx, `UPDATE interop_bindings SET clearing_wallet_id=suspense_wallet_id,suspense_wallet_id=clearing_wallet_id WHERE tenant_id=$1`, a.id)
		if err == nil {
			t.Fatal("existing obligations' settlement wallet identities were mutable")
		}
		a.balance(t, a.user.ID, 10000, 9900)
		b.balance(t, b.user.ID, 10000, 9900)
	})

	t.Run("invalid system wallet binding fails before accepting obligation", func(t *testing.T) {
		n := f.tenant(t, "invalid-binding", true)
		q := n.outgoingQuote(t, 100)
		n.ready(t, q)
		tr := n.request(t, q)
		token := n.claim(t, tr)
		_, err := f.worker.ArmInteropTransfer(f.ctx, n.id, tr.ID, token)
		interopError(t, err, ErrInteropState)
		incoming := n.incomingQuote(t, 100)
		prepare, response := n.incomingPayloads(incoming)
		_, err = f.worker.ReserveInteropIncoming(f.ctx, n.id, incoming.ID, prepare, response, incoming.ExpiresAt.Time)
		interopError(t, err, ErrInteropState)
		n.balance(t, n.user.ID, 10000, 10000)
		n.count(t, `SELECT count(*) FROM balance_holds WHERE tenant_id=$1`, 0)
		n.count(t, `SELECT count(*) FROM transaction_limit_reservations WHERE tenant_id=$1`, 0)
	})
}

type interopFixture struct {
	ctx     context.Context
	pg      *testdb.PostgresContainer
	migrate *basestore.DB
	runtime *Store
	worker  *Store
	tenants []tenantcatalog.Tenant
}

func newInteropFixture(t *testing.T) *interopFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	pg, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatalf("start disposable PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	dsn, err := pg.CreateDatabaseForRole(ctx, "wallet_ledger", "wallet_ledger_migrate")
	interopMust(t, err)
	migrate, err := basestore.OpenFromConfig(dsn, basestore.DriverPostgres)
	interopMust(t, err)
	t.Cleanup(func() {
		_ = migrate.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = pg.DropDatabase(cleanup, "wallet_ledger")
	})
	interopMust(t, basestore.MigrateScope(ctx, migrate, basestore.MigrationScopeWalletLedger))
	f := &interopFixture{ctx: ctx, pg: pg, migrate: migrate}
	f.runtime, f.worker = f.openRole(t, "wallet_ledger_runtime"), f.openRole(t, "wallet_ledger_worker")
	return f
}

func (f *interopFixture) openRole(t *testing.T, role string) *Store {
	t.Helper()
	dsn, err := f.pg.DatabaseURLForRole("wallet_ledger", role)
	interopMust(t, err)
	db, err := basestore.OpenFromConfig(dsn, basestore.DriverPostgres)
	interopMust(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return New(db)
}

type interopTenant struct {
	f                        *interopFixture
	id                       string
	unit                     int64
	user, clearing, suspense *Wallet
	seedID                   int64
}

func (f *interopFixture) tenant(t *testing.T, name string, wrongClearing bool) *interopTenant {
	t.Helper()
	n := &interopTenant{f: f, id: "interop-" + name, unit: testCurrencyUnitID(t, f.ctx, f.worker, "SDG")}
	f.tenants = append(f.tenants, tenantcatalog.Tenant{ID: tenantcatalog.ID(n.id), Name: "Synthetic interop regression"})
	slices.SortFunc(f.tenants, func(a, b tenantcatalog.Tenant) int { return cmp.Compare(a.ID, b.ID) })
	catalog, err := tenantcatalog.New(f.tenants)
	interopMust(t, err)
	interopMust(t, basestore.New(f.migrate).ProvisionTenantCatalog(f.ctx, catalog))
	ensure := func(ownerType, ownerID string, userID int64) *Wallet {
		w, err := f.runtime.EnsureWallet(f.ctx, EnsureWalletParams{TenantID: n.id, OwnerType: ownerType, OwnerID: ownerID, UserID: userID, Currency: "SDG", CurrencyUnitID: n.unit, KYCTier: KYCTierUnverified})
		interopMust(t, err)
		return w
	}
	n.user = ensure(OwnerTypeUser, "customer", 1)
	n.clearing = ensure(OwnerTypeSystem, SystemMojaloopClearing, 0)
	n.suspense = ensure(OwnerTypeSystem, SystemMojaloopSuspense, 0)
	treasury := ensure(OwnerTypeSystem, SystemTreasury, 0)
	seed, err := f.worker.PostSystemDebitDoubleEntry(f.ctx, DoubleEntryParams{TenantID: n.id, IdempotencyKey: "synthetic-fixture-seed", DebitWalletID: treasury.ID, CreditWalletID: n.user.ID, Amount: 10000, Currency: "SDG", ReferenceType: "interop-test-seed", ReferenceID: n.id, Description: "Disposable synthetic regression funds"})
	interopMust(t, err)
	n.seedID = seed.TransactionID
	clearingID := n.clearing.ID
	if wrongClearing {
		clearingID = n.user.ID
	}
	_, err = f.migrate.ExecContext(f.ctx, `INSERT INTO interop_bindings(tenant_id,fsp_id,currency,currency_unit_version_id,clearing_wallet_id,suspense_wallet_id,enabled) VALUES($1,$2,'SDG',$3,$4,$5,true)`, n.id, "test"+uuid.New().String()[:8], n.unit, clearingID, n.suspense.ID)
	interopMust(t, err)
	for _, direction := range []string{"interop_out", "interop_in"} {
		_, err = f.migrate.ExecContext(f.ctx, `INSERT INTO transaction_limits(tenant_id,kyc_tier,transaction_type,currency,currency_unit_version_id,daily_limit,monthly_limit,per_transaction_limit) VALUES($1,$2,$3,'SDG',$4,100000,100000,100000)`, n.id, KYCTierUnverified, direction, n.unit)
		interopMust(t, err)
	}
	return n
}

func (n *interopTenant) quote(direction string, amount int64) InteropQuote {
	q := InteropQuote{ID: uuid.New(), TransferID: uuid.New(), TenantID: n.id, OwnerID: n.user.OwnerID, WalletID: n.user.ID, Direction: direction, IdempotencyKey: uuid.NewString(), Amount: amount, Currency: "SDG", CurrencyUnitID: n.unit, Request: RawJSON(`{"partyIdType":"MSISDN","partyIdentifier":"249900000001"}`), Status: "REQUESTED"}
	if direction == "IN" {
		q.Status, q.Response, q.ExpiresAt = "READY", RawJSON(`{"quote":"fixed"}`), sql.NullTime{Time: time.Now().Add(time.Hour), Valid: true}
	}
	return q
}

func (n *interopTenant) outgoingQuote(t *testing.T, amount int64) *InteropQuote {
	t.Helper()
	q, err := n.f.runtime.CreateInteropQuote(n.f.ctx, n.quote("OUT", amount))
	interopMust(t, err)
	return q
}

func (n *interopTenant) incomingQuote(t *testing.T, amount int64) *InteropQuote {
	t.Helper()
	q, err := n.f.worker.CreateInteropQuote(n.f.ctx, n.quote("IN", amount))
	interopMust(t, err)
	return q
}

func (n *interopTenant) ready(t *testing.T, q *InteropQuote) {
	t.Helper()
	token := uuid.New()
	claimed, err := n.f.worker.ClaimInteropQuote(n.f.ctx, n.id, token)
	interopMust(t, err)
	if claimed.ID != q.ID {
		t.Fatalf("claimed quote %s, want %s", claimed.ID, q.ID)
	}
	interopMust(t, n.f.worker.CompleteInteropQuote(n.f.ctx, n.id, q.ID, token, RawJSON(`{"quote":"fixed"}`), RawJSON(`{"currentState":"WAITING_FOR_QUOTE_ACCEPTANCE"}`), time.Now().Add(time.Hour), ""))
}

func (n *interopTenant) request(t *testing.T, q *InteropQuote) *InteropTransfer {
	t.Helper()
	tr, err := n.f.runtime.RequestInteropTransfer(n.f.ctx, n.id, n.user.OwnerID, q.ID, q.IdempotencyKey)
	interopMust(t, err)
	return tr
}

func (n *interopTenant) claim(t *testing.T, tr *InteropTransfer) uuid.UUID {
	t.Helper()
	token := uuid.New()
	claimed, err := n.f.worker.ClaimInteropTransfer(n.f.ctx, n.id, token)
	interopMust(t, err)
	if claimed.ID != tr.ID {
		t.Fatalf("claimed transfer %s, want %s", claimed.ID, tr.ID)
	}
	return token
}

func (n *interopTenant) armed(t *testing.T, amount int64) (*InteropQuote, *InteropTransfer, uuid.UUID) {
	t.Helper()
	q := n.outgoingQuote(t, amount)
	n.ready(t, q)
	tr := n.request(t, q)
	token := n.claim(t, tr)
	tr, err := n.f.worker.ArmInteropTransfer(n.f.ctx, n.id, tr.ID, token)
	interopMust(t, err)
	return q, tr, token
}

func (n *interopTenant) event(tr *InteropTransfer, state string) InteropEvent {
	return InteropEvent{TenantID: n.id, TransferID: tr.ID, Kind: state, Authority: "sdk-hub-query", Payload: RawJSON(fmt.Sprintf(" {\n \"transferId\":\"%s\", \"transferState\":\"%s\"\n}\n", tr.ID, state))}
}

func (n *interopTenant) incomingPayloads(q *InteropQuote) (RawJSON, RawJSON) {
	return RawJSON(fmt.Sprintf(`{"transferId":"%s","quoteId":"%s"}`, q.TransferID, q.ID)), RawJSON(" {\n \"completedTimestamp\" : \"2026-09-10T00:00:00.123Z\",\n \"transferState\" : \"RESERVED\"\n}\n")
}

func (n *interopTenant) transfer(t *testing.T, id uuid.UUID) *InteropTransfer {
	t.Helper()
	tr, err := n.f.runtime.GetInteropTransfer(n.f.ctx, n.id, n.user.OwnerID, id, "")
	interopMust(t, err)
	return tr
}

func (n *interopTenant) balance(t *testing.T, id uuid.UUID, balance, available int64) {
	t.Helper()
	assertWalletBalances(t, n.f.ctx, n.f.runtime, n.id, id, balance, available)
}

func (n *interopTenant) count(t *testing.T, query string, want int64) {
	t.Helper()
	var count int64
	interopMust(t, n.f.migrate.GetContext(n.f.ctx, &count, query, n.id))
	if count != want {
		t.Fatalf("%s: got %d, want %d", query, count, want)
	}
}

func (n *interopTenant) hold(t *testing.T, id int64, status string, amount int64) {
	t.Helper()
	var hold BalanceHold
	interopMust(t, n.f.worker.DB.GetContext(n.f.ctx, &hold, `SELECT * FROM balance_holds WHERE tenant_id=$1 AND id=$2`, n.id, id))
	if hold.Status != status || hold.AmountRemaining != amount {
		t.Fatalf("hold state = %s/%d, want %s/%d", hold.Status, hold.AmountRemaining, status, amount)
	}
}

func (n *interopTenant) usage(t *testing.T, q *InteropQuote, status string, reserved, consumed int64) {
	t.Helper()
	var actual string
	interopMust(t, n.f.worker.DB.GetContext(n.f.ctx, &actual, `SELECT status FROM transaction_limit_reservations WHERE tenant_id=$1 AND command_id=$2`, n.id, "mojaloop:"+q.TransferID.String()))
	if actual != status {
		t.Fatalf("usage reservation = %s, want %s", actual, status)
	}
	var periods []struct {
		Reserved int64 `db:"reserved_amount"`
		Consumed int64 `db:"consumed_amount"`
	}
	interopMust(t, n.f.worker.DB.SelectContext(n.f.ctx, &periods, `SELECT reserved_amount,consumed_amount FROM transaction_limit_period_usage WHERE tenant_id=$1 AND wallet_id=$2 AND transaction_type=$3`, n.id, n.user.ID, interopUsage(q).TransactionType))
	if len(periods) != 2 {
		t.Fatalf("limit periods = %d, want daily and monthly", len(periods))
	}
	for _, p := range periods {
		if p.Reserved != reserved || p.Consumed != consumed {
			t.Fatalf("usage = reserved:%d consumed:%d, want %d/%d", p.Reserved, p.Consumed, reserved, consumed)
		}
	}
}

func (n *interopTenant) settled(t *testing.T, id uuid.UUID, status, hub string, balance, available int64) {
	t.Helper()
	tr := n.transfer(t, id)
	if tr.Status != status || tr.HubState != hub || !tr.LedgerTransactionID.Valid {
		t.Fatalf("settled transfer = status:%s hub:%s journal:%v", tr.Status, tr.HubState, tr.LedgerTransactionID)
	}
	n.balance(t, n.user.ID, balance, available)
	n.count(t, `SELECT count(*) FROM ledger_transactions WHERE tenant_id=$1 AND reference_type='mojaloop'`, 1)
	n.count(t, `SELECT count(*) FROM ledger_entries e JOIN ledger_transactions l ON l.tenant_id=e.tenant_id AND l.id=e.transaction_id WHERE e.tenant_id=$1 AND l.reference_type='mojaloop'`, 2)
}

func (n *interopTenant) conserved(t *testing.T) {
	t.Helper()
	n.count(t, `SELECT sum(balance) FROM wallets WHERE tenant_id=$1`, 0)
	n.count(t, `SELECT sum(CASE WHEN entry_type='debit' THEN -amount ELSE amount END) FROM ledger_entries WHERE tenant_id=$1`, 0)
}

func interopConcurrent(count int, fn func(int) error) []error {
	start := make(chan struct{})
	errs := make([]error, count)
	var group sync.WaitGroup
	for i := range count {
		group.Go(func() { <-start; errs[i] = fn(i) })
	}
	close(start)
	group.Wait()
	return errs
}

func interopMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func interopError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func interopSQLState(t *testing.T, err error, code string) {
	t.Helper()
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != code {
		t.Fatalf("error = %v, want PostgreSQL SQLSTATE %s", err, code)
	}
}

func TestInteropValidationRequiresExplicitIdentifiers(t *testing.T) {
	s := &Store{}
	q := InteropQuote{ID: uuid.New(), TransferID: uuid.New(), TenantID: "interop-validation", OwnerID: "customer", WalletID: uuid.New(), Direction: "OUT", IdempotencyKey: "key", Amount: 100, Currency: "SDG", CurrencyUnitID: 1, Request: RawJSON(`{}`), Status: "REQUESTED"}
	for _, test := range []struct {
		name   string
		mutate func(*InteropQuote)
		want   error
	}{
		{"tenant", func(q *InteropQuote) { q.TenantID = "" }, ErrMissingTenantID},
		{"currency", func(q *InteropQuote) { q.Currency = "" }, ErrMissingCurrency},
		{"unit", func(q *InteropQuote) { q.CurrencyUnitID = 0 }, ErrMissingCurrencyUnitID},
		{"owner", func(q *InteropQuote) { q.OwnerID = "" }, ErrInteropInvalid},
		{"amount", func(q *InteropQuote) { q.Amount = 0 }, ErrInvalidAmount},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := q
			test.mutate(&invalid)
			_, err := s.CreateInteropQuote(t.Context(), invalid)
			interopError(t, err, test.want)
		})
	}
	_, err := s.ClaimInteropTransfer(t.Context(), q.TenantID, uuid.Nil)
	interopError(t, err, ErrInteropInvalid)
	_, err = s.ReserveInteropIncoming(t.Context(), q.TenantID, uuid.Nil, RawJSON(`{}`), RawJSON(`{}`), time.Now().Add(time.Hour))
	interopError(t, err, ErrInteropInvalid)
}
