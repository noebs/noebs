package store

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/statusevent"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestP2PRecipientNotificationsFollowCommittedCredits(t *testing.T) {
	f := newInteropFixture(t)
	n := f.tenant(t, "p2p-recipient-events", false)
	from, err := f.runtime.EnsureWallet(f.ctx, EnsureWalletParams{TenantID: n.id, OwnerType: OwnerTypeUser, OwnerID: "51", UserID: 51, Currency: "SDG", CurrencyUnitID: n.unit, KYCTier: KYCTierUnverified})
	interopMust(t, err)
	_, err = f.worker.PostDoubleEntry(f.ctx, DoubleEntryParams{TenantID: n.id, IdempotencyKey: "recipient-event-funds", DebitWalletID: n.user.ID, CreditWalletID: from.ID, Amount: 5000, Currency: "SDG", ReferenceType: "fixture", ReferenceID: "p2p"})
	interopMust(t, err)
	to, err := f.runtime.EnsureWallet(f.ctx, EnsureWalletParams{TenantID: n.id, OwnerType: OwnerTypeUser, OwnerID: "52", UserID: 52, Currency: "SDG", CurrencyUnitID: n.unit, KYCTier: KYCTierUnverified})
	interopMust(t, err)
	_, err = f.migrate.ExecContext(f.ctx, `INSERT INTO transaction_limits(tenant_id,kyc_tier,transaction_type,currency,currency_unit_version_id,daily_limit,monthly_limit,per_transaction_limit) VALUES($1,$2,'p2p','SDG',$3,100000,100000,100000)`, n.id, KYCTierUnverified, n.unit)
	interopMust(t, err)
	reserve := func(key string, dest *Wallet) MultiLegSettlementParams {
		t.Helper()
		fee := int64(0)
		payload := P2PCommandPayload{Currency: "SDG", FromWalletID: from.ID.String(), ToWalletID: dest.ID.String(), Amount: 100, ReferenceID: key, FromOwnerType: from.OwnerType, FromOwnerID: from.OwnerID, ToOwnerType: dest.OwnerType, ToOwnerID: dest.OwnerID, ExpectedFeeAmount: &fee, ExpectedCurrencyUnitVersion: &n.unit}
		doc, e := json.Marshal(payload)
		interopMust(t, e)
		_, e = f.runtime.ReserveP2PCommand(f.ctx, P2PCommandReservation{TenantID: n.id, IdempotencyKey: key, WorkflowID: "workflow-" + key, FromWalletID: from.ID, ToWalletID: dest.ID, FromOwnerType: from.OwnerType, FromOwnerID: from.OwnerID, ToOwnerType: dest.OwnerType, ToOwnerID: dest.OwnerID, Command: doc})
		interopMust(t, e)
		return MultiLegSettlementParams{P2PCommandID: key, TenantID: n.id, IdempotencyKey: key, Currency: "SDG", ReferenceType: "p2p", ReferenceID: key, Transfers: []SettlementTransfer{{DebitWalletID: from.ID, CreditWalletID: dest.ID, Amount: 100}}, LimitUsage: LimitUsageParams{TenantID: n.id, CommandID: "p2p:" + key, WalletID: from.ID, TransactionType: "p2p", Currency: "SDG", Amount: 100}}
	}
	count := func(key string, want int) {
		t.Helper()
		var got int
		interopMust(t, f.runtime.DB.GetContext(f.ctx, &got, `SELECT count(*) FROM transaction_status_events WHERE tenant_id=$1 AND aggregate_id=$2`, n.id, "p2p-incoming:"+to.ID.String()+":"+key))
		if got != want {
			t.Fatalf("recipient events for %s = %d, want %d", key, got, want)
		}
	}
	t.Run("reservation execution and failure do not notify recipient", func(t *testing.T) {
		p := reserve("failed", to)
		count(p.IdempotencyKey, 0)
		_, err := f.runtime.RecordP2PCommandRun(f.ctx, n.id, p.IdempotencyKey, "workflow-failed", "run-failed")
		interopMust(t, err)
		count(p.IdempotencyKey, 0)
		interopMust(t, f.runtime.RecordP2PFailure(f.ctx, n.id, p.IdempotencyKey, "payment_not_completed"))
		_, err = f.worker.PostMultiLegSettlement(f.ctx, p)
		interopError(t, err, ErrP2PCommandFailed)
		count(p.IdempotencyKey, 0)
	})
	t.Run("completed recipient event survives retries without duplicating sender", func(t *testing.T) {
		p := reserve("completed", to)
		for _, err := range interopConcurrent(4, func(int) error {
			_, err := f.worker.PostMultiLegSettlement(f.ctx, p)
			return err
		}) {
			interopMust(t, err)
		}
		count(p.IdempotencyKey, 1)
		n.balance(t, to.ID, 100, 100)
		var firstID uuid.UUID
		aggregate := "p2p-incoming:" + to.ID.String() + ":" + p.IdempotencyKey
		interopMust(t, f.runtime.DB.GetContext(f.ctx, &firstID, `SELECT event_id FROM transaction_status_events WHERE tenant_id=$1 AND aggregate_id=$2`, n.id, aggregate))
		interopMust(t, f.worker.RecordP2PFailure(f.ctx, n.id, p.IdempotencyKey, "payment_not_completed"))
		_, err := f.runtime.RecordP2PCommandRun(f.ctx, n.id, p.IdempotencyKey, "workflow-completed", "late-run")
		interopMust(t, err)
		count(p.IdempotencyKey, 1)
		var senderEvents int
		interopMust(t, f.runtime.DB.GetContext(f.ctx, &senderEvents, `SELECT count(*) FROM transaction_status_events WHERE tenant_id=$1 AND aggregate_id=$2 AND user_id=$3`, n.id, "p2p:"+p.IdempotencyKey, from.UserID.Int64))
		if senderEvents != 2 {
			t.Fatalf("sender events = %d, want unchanged created and completed", senderEvents)
		}
		outbox, err := NewStatusEventOutbox(f.worker, "noebs.status.changed.v1", time.Minute)
		interopMust(t, err)
		events, err := outbox.ClaimPendingTransactionEvents(f.ctx, 100)
		interopMust(t, err)
		found := 0
		for _, event := range events {
			parsed, err := statusevent.Parse(event.Payload)
			interopMust(t, err)
			if parsed.AggregateID != aggregate {
				continue
			}
			found++
			if parsed.ID != firstID || parsed.TenantID != n.id || parsed.UserID == nil || *parsed.UserID != 52 || parsed.Version != 1 || parsed.Status != "completed" || parsed.Substatus != "automated" || parsed.OccurredAt.IsZero() {
				t.Fatalf("recipient event = %+v", parsed)
			}
			if event.EventKey != n.id+":"+aggregate {
				t.Fatalf("recipient partition key = %s", event.EventKey)
			}
		}
		if found != 1 {
			t.Fatalf("published recipient events = %d", found)
		}
		_, err = f.worker.DB.ExecContext(f.ctx, `INSERT INTO ledger_entries(
 tenant_id,transaction_id,wallet_id,entry_type,amount,currency,currency_unit_version_id,
 balance_after,wallet_sequence,status)
 SELECT e.tenant_id,e.transaction_id,e.wallet_id,e.entry_type,e.amount,e.currency,e.currency_unit_version_id,
 e.balance_after,e.wallet_sequence+1,e.status FROM ledger_entries e
 JOIN ledger_transactions lt ON lt.tenant_id=e.tenant_id AND lt.id=e.transaction_id
 WHERE e.tenant_id=$1 AND lt.idempotency_key=$2 AND e.wallet_id=$3 AND e.entry_type='credit'`, n.id, p.IdempotencyKey, to.ID)
		var conflict *pgconn.PgError
		if !errors.As(err, &conflict) || conflict.Code != "23505" || conflict.ConstraintName != "transaction_status_events_tenant_id_aggregate_id_version_key" {
			t.Fatalf("unexpected second credit must fail event uniqueness: %v", err)
		}
		count(p.IdempotencyKey, 1)
	})
	t.Run("rolled back credit cannot notify recipient", func(t *testing.T) {
		p := reserve("rollback", to)
		before, err := f.runtime.GetWallet(f.ctx, n.id, to.ID)
		interopMust(t, err)
		_, err = f.migrate.ExecContext(f.ctx, `CREATE FUNCTION test_reject_credit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.entry_type='credit' AND EXISTS(SELECT 1 FROM ledger_transactions WHERE id=NEW.transaction_id AND idempotency_key='rollback') THEN
  IF NOT EXISTS(SELECT 1 FROM transaction_status_events WHERE tenant_id=NEW.tenant_id AND aggregate_id='p2p-incoming:' || NEW.wallet_id || ':rollback') THEN
   RAISE EXCEPTION 'recipient trigger did not run before test failure';
  END IF;
  RAISE EXCEPTION 'test failure after recipient event' USING ERRCODE='P9001';
 END IF;
 RETURN NEW; END $$;
 CREATE TRIGGER z_test_reject_credit AFTER INSERT ON ledger_entries FOR EACH ROW EXECUTE FUNCTION test_reject_credit()`)
		interopMust(t, err)
		_, err = f.worker.PostMultiLegSettlement(f.ctx, p)
		interopSQLState(t, err, "P9001")
		count(p.IdempotencyKey, 0)
		n.balance(t, to.ID, before.Balance, before.AvailableBalance)
		var sourceCompleted int
		interopMust(t, f.runtime.DB.GetContext(f.ctx, &sourceCompleted, `SELECT count(*) FROM transaction_status_events WHERE tenant_id=$1 AND aggregate_id=$2 AND status='completed'`, n.id, "p2p:"+p.IdempotencyKey))
		if sourceCompleted != 0 {
			t.Fatal("rollback retained sender completion")
		}
	})
	t.Run("unrelated credit with command key does not notify recipient", func(t *testing.T) {
		p := reserve("unrelated", to)
		_, err := f.worker.PostDoubleEntry(f.ctx, DoubleEntryParams{TenantID: n.id, IdempotencyKey: p.IdempotencyKey, DebitWalletID: from.ID, CreditWalletID: to.ID, Amount: 100, Currency: "SDG", ReferenceType: "fixture", ReferenceID: p.ReferenceID})
		interopMust(t, err)
		count(p.IdempotencyKey, 0)
	})
	t.Run("self transfer is rejected without duplicate notifications", func(t *testing.T) {
		_, err := f.runtime.ReserveP2PCommand(f.ctx, P2PCommandReservation{TenantID: n.id, IdempotencyKey: "self", WorkflowID: "workflow-self", FromWalletID: to.ID, ToWalletID: to.ID, FromOwnerType: to.OwnerType, FromOwnerID: to.OwnerID, ToOwnerType: to.OwnerType, ToOwnerID: to.OwnerID, Command: RawJSON(`{}`)})
		if !errors.Is(err, ErrInvalidWalletPair) {
			t.Fatalf("self transfer: %v", err)
		}
		count("self", 0)
	})
}
