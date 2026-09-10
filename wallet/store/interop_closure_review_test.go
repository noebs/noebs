package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Closing a quote is a durable admission decision, not a financial outcome.
// These tests use the actual runtime login against disposable PostgreSQL and
// race it against the same public admission method used after step-up auth.
func TestInteropQuoteClosureRegression(t *testing.T) {
	f := newInteropFixture(t)

	t.Run("runtime closure is durable and cannot be changed or deleted", func(t *testing.T) {
		n := f.tenant(t, "closure-authority", false)
		q := n.outgoingQuote(t, 500)
		var login string
		interopMust(t, f.runtime.DB.GetContext(f.ctx, &login, "SELECT current_user"))
		if login != "wallet_ledger_runtime" {
			t.Fatalf("actual closure login = %q", login)
		}
		for range 3 {
			closed, transferID, err := f.runtime.CloseInteropQuote(f.ctx, n.id, n.user.OwnerID, q.ID)
			interopMust(t, err)
			if !closed || transferID != uuid.Nil {
				t.Fatalf("unused quote closure = %v/%s, want true/nil", closed, transferID)
			}
		}
		n.count(t, "SELECT count(*) FROM interop_quote_closures WHERE tenant_id=$1", 1)
		n.count(t, "SELECT count(*) FROM interop_transfers WHERE tenant_id=$1", 0)
		stored, err := f.runtime.GetInteropQuote(f.ctx, n.id, q.ID)
		interopMust(t, err)
		if stored.Status != q.Status || stored.Amount != q.Amount || string(stored.Request) != string(q.Request) {
			t.Fatal("closing a quote changed its protocol agreement")
		}
		_, err = f.runtime.DB.ExecContext(f.ctx, "UPDATE interop_quote_closures SET tenant_id=tenant_id WHERE tenant_id=$1", n.id)
		interopSQLState(t, err, "42501")
		_, err = f.runtime.DB.ExecContext(f.ctx, "DELETE FROM interop_quote_closures WHERE tenant_id=$1", n.id)
		interopSQLState(t, err, "42501")
		n.balance(t, n.user.ID, 10000, 10000)
		n.count(t, "SELECT count(*) FROM balance_holds WHERE tenant_id=$1", 0)
	})

	t.Run("closure fences every later admission key and quote replay", func(t *testing.T) {
		n := f.tenant(t, "closure-first", false)
		q := n.outgoingQuote(t, 500)
		n.ready(t, q)
		closed, transferID, err := f.runtime.CloseInteropQuote(f.ctx, n.id, n.user.OwnerID, q.ID)
		interopMust(t, err)
		if !closed || transferID != uuid.Nil {
			t.Fatalf("close-before-admit = %v/%s", closed, transferID)
		}
		for _, key := range []string{q.IdempotencyKey, "different-transfer-key"} {
			_, err = f.runtime.RequestInteropTransfer(f.ctx, n.id, n.user.OwnerID, q.ID, key)
			interopError(t, err, ErrInteropState)
		}
		replayed, err := f.runtime.CreateInteropQuote(f.ctx, *q)
		interopMust(t, err)
		if replayed.ID != q.ID {
			t.Fatal("quote retry escaped the closed quote identity")
		}
		_, err = f.runtime.RequestInteropTransfer(f.ctx, n.id, n.user.OwnerID, replayed.ID, "after-quote-replay")
		interopError(t, err, ErrInteropState)
		n.count(t, "SELECT count(*) FROM interop_transfers WHERE tenant_id=$1", 0)
		n.count(t, "SELECT count(*) FROM balance_holds WHERE tenant_id=$1", 0)
	})

	t.Run("expired quote can be closed while admission is disabled", func(t *testing.T) {
		n := f.tenant(t, "closure-expired", false)
		q := n.outgoingQuote(t, 500)
		n.ready(t, q)
		_, err := f.migrate.ExecContext(f.ctx, "UPDATE interop_quotes SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", q.ID)
		interopMust(t, err)
		_, err = f.migrate.ExecContext(f.ctx, "UPDATE interop_bindings SET enabled=false WHERE tenant_id=$1", n.id)
		interopMust(t, err)
		closed, transferID, err := f.runtime.CloseInteropQuote(f.ctx, n.id, n.user.OwnerID, q.ID)
		interopMust(t, err)
		if !closed || transferID != uuid.Nil {
			t.Fatalf("expired disabled quote closure = %v/%s", closed, transferID)
		}
		n.count(t, "SELECT count(*) FROM interop_quote_closures WHERE tenant_id=$1", 1)
		n.count(t, "SELECT count(*) FROM interop_transfers WHERE tenant_id=$1", 0)
	})

	t.Run("foreign owner tenant and incoming direction cannot close or reveal transfers", func(t *testing.T) {
		a, b := f.tenant(t, "closure-owner-a", false), f.tenant(t, "closure-owner-b", false)
		q := a.outgoingQuote(t, 500)
		nobody := uuid.New()
		for _, admitted := range []bool{false, true} {
			if admitted {
				a.ready(t, q)
				a.request(t, q)
			}
			for _, tc := range []struct {
				tenant string
				owner  string
				quote  uuid.UUID
			}{{a.id, "another-customer", q.ID}, {b.id, a.user.OwnerID, q.ID}, {a.id, a.user.OwnerID, nobody}} {
				closed, transferID, err := f.runtime.CloseInteropQuote(f.ctx, tc.tenant, tc.owner, tc.quote)
				interopError(t, err, ErrInteropNotFound)
				if closed || transferID != uuid.Nil {
					t.Fatal("unauthorized closure returned another owner's admission result")
				}
			}
		}
		incoming := b.incomingQuote(t, 100)
		closed, transferID, err := f.runtime.CloseInteropQuote(f.ctx, b.id, b.user.OwnerID, incoming.ID)
		interopError(t, err, ErrInteropNotFound)
		if closed || transferID != uuid.Nil {
			t.Fatal("outgoing cancellation changed an incoming quote")
		}
		a.count(t, "SELECT count(*) FROM interop_quote_closures WHERE tenant_id=$1", 0)
		b.count(t, "SELECT count(*) FROM interop_quote_closures WHERE tenant_id=$1", 0)
	})

	t.Run("simultaneous close and admission have one durable winner", func(t *testing.T) {
		for round := range 8 {
			n := f.tenant(t, fmt.Sprintf("closure-race-%d", round), false)
			q := n.outgoingQuote(t, 500)
			n.ready(t, q)
			type result struct {
				closed bool
				id     uuid.UUID
				err    error
			}
			results := make([]result, 16)
			for _, err := range interopConcurrent(len(results), func(i int) error {
				if (i+round)%2 == 0 {
					results[i].closed, results[i].id, results[i].err = f.runtime.CloseInteropQuote(f.ctx, n.id, n.user.OwnerID, q.ID)
				} else {
					transfer, err := f.runtime.RequestInteropTransfer(f.ctx, n.id, n.user.OwnerID, q.ID, q.IdempotencyKey)
					results[i].err = err
					if err == nil {
						results[i].id = transfer.ID
					}
				}
				return nil
			}) {
				interopMust(t, err)
			}
			var transfers, closures int
			interopMust(t, f.runtime.DB.GetContext(f.ctx, &transfers, "SELECT count(*) FROM interop_transfers WHERE tenant_id=$1", n.id))
			interopMust(t, f.runtime.DB.GetContext(f.ctx, &closures, "SELECT count(*) FROM interop_quote_closures WHERE tenant_id=$1", n.id))
			if transfers+closures != 1 {
				t.Fatalf("race %d admitted=%d closed=%d; exactly one durable outcome required", round, transfers, closures)
			}
			for i, r := range results {
				if (i+round)%2 == 0 {
					interopMust(t, r.err)
					if r.closed != (closures == 1) || (r.closed && r.id != uuid.Nil) || (!r.closed && r.id != q.TransferID) {
						t.Fatalf("race %d close %d disagreed with durable outcome: %+v", round, i, r)
					}
				} else if closures == 1 {
					interopError(t, r.err, ErrInteropState)
				} else {
					interopMust(t, r.err)
					if r.id != q.TransferID {
						t.Fatal("concurrent admission retry changed the transfer identity")
					}
				}
			}
			n.balance(t, n.user.ID, 10000, 10000)
			n.count(t, "SELECT count(*) FROM balance_holds WHERE tenant_id=$1", 0)
		}
	})

	t.Run("admitted transfer is recovered without altering holds or finality", func(t *testing.T) {
		n := f.tenant(t, "closure-recovery", false)
		q, transfer, token := n.armed(t, 1000)
		interopMust(t, f.worker.MarkInteropSubmitted(f.ctx, n.id, transfer.ID, token, RawJSON("{\"original\":\"prepare\"}")))
		_, err := f.migrate.ExecContext(f.ctx, "UPDATE interop_bindings SET enabled=false WHERE tenant_id=$1", n.id)
		interopMust(t, err)
		_, err = f.migrate.ExecContext(f.ctx, "UPDATE interop_quotes SET expires_at=$1 WHERE id=$2", time.Now().Add(-time.Hour), q.ID)
		interopMust(t, err)
		for range 3 {
			closed, recoveredID, err := f.runtime.CloseInteropQuote(f.ctx, n.id, n.user.OwnerID, q.ID)
			interopMust(t, err)
			if closed || recoveredID != transfer.ID {
				t.Fatalf("already admitted closure returned %v/%s", closed, recoveredID)
			}
			recovered, err := f.runtime.GetInteropTransfer(f.ctx, n.id, n.user.OwnerID, recoveredID, "")
			interopMust(t, err)
			if recovered.Status != "PENDING" || recovered.HubState != "UNKNOWN" || recovered.HoldID != transfer.HoldID {
				t.Fatalf("closing admitted quote changed its obligation: %+v", recovered)
			}
		}
		retried, err := f.runtime.RequestInteropTransfer(f.ctx, n.id, n.user.OwnerID, q.ID, q.IdempotencyKey)
		interopMust(t, err)
		if retried.ID != transfer.ID {
			t.Fatal("disabled expired command retry did not recover the original transfer")
		}
		n.balance(t, n.user.ID, 10000, 9000)
		n.hold(t, transfer.HoldID.Int64, HoldStatusCommitted, 1000)
		n.usage(t, q, "reserved", 1000, 0)
		eventID, err := f.worker.RecordInteropEvent(f.ctx, n.event(transfer, "COMMITTED"))
		interopMust(t, err)
		interopMust(t, f.worker.ApplyInteropEvent(f.ctx, n.id, eventID))
		closed, recoveredID, err := f.runtime.CloseInteropQuote(f.ctx, n.id, n.user.OwnerID, q.ID)
		interopMust(t, err)
		if closed || recoveredID != transfer.ID {
			t.Fatal("closure concealed a committed transfer")
		}
		n.settled(t, transfer.ID, "SUCCEEDED", "COMMITTED", 9000, 9000)
		n.hold(t, transfer.HoldID.Int64, HoldStatusCaptured, 0)
		n.usage(t, q, "consumed", 0, 1000)
		n.count(t, "SELECT count(*) FROM interop_quote_closures WHERE tenant_id=$1", 0)
		n.conserved(t)
	})
}

func TestInteropQuoteClosureValidation(t *testing.T) {
	store := &Store{}
	for _, tc := range []struct {
		name, tenant, owner string
		quote               uuid.UUID
		want                error
	}{
		{"missing tenant", "", "customer", uuid.New(), ErrMissingTenantID},
		{"missing owner", "interop-closure", "", uuid.New(), ErrInteropInvalid},
		{"missing quote", "interop-closure", "customer", uuid.Nil, ErrInteropInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			closed, transferID, err := store.CloseInteropQuote(t.Context(), tc.tenant, tc.owner, tc.quote)
			interopError(t, err, tc.want)
			if closed || transferID != uuid.Nil {
				t.Fatal("invalid closure input reported a durable outcome")
			}
		})
	}
}
