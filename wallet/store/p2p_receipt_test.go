package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func TestP2PExpectations(t *testing.T) {
	zero, one, unit, wrongUnit := int64(0), int64(1), int64(14), int64(15)
	for _, tt := range []struct {
		name      string
		fee, unit *int64
		want      error
	}{
		{"legacy", nil, nil, nil}, {"reviewed zero fee", &zero, &unit, nil},
		{"fee changed", &one, &unit, ErrP2PFeeChanged}, {"unit changed", &zero, &wrongUnit, ErrCurrencyMismatch},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateP2PExpectation(tt.fee, tt.unit, 0, 14); !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestP2PReceiptsAndFailureFence(t *testing.T) {
	f := newInteropFixture(t)
	n := f.tenant(t, "p2p-receipts", false)
	ensure := func(owner string, user int64) *Wallet {
		w, e := f.runtime.EnsureWallet(f.ctx, EnsureWalletParams{TenantID: n.id, OwnerType: OwnerTypeUser, OwnerID: owner, UserID: user, Currency: "SDG", CurrencyUnitID: n.unit, KYCTier: KYCTierUnverified})
		interopMust(t, e)
		return w
	}
	from, to := ensure("51", 51), ensure("52", 52)
	_, err := f.worker.PostDoubleEntry(f.ctx, DoubleEntryParams{TenantID: n.id, IdempotencyKey: "p2p-fixture-funds", DebitWalletID: n.user.ID, CreditWalletID: from.ID, Amount: 5000, Currency: "SDG", ReferenceType: "fixture", ReferenceID: "p2p"})
	interopMust(t, err)
	_, err = f.migrate.ExecContext(f.ctx, `INSERT INTO transaction_limits(tenant_id,kyc_tier,transaction_type,currency,currency_unit_version_id,daily_limit,monthly_limit,per_transaction_limit) VALUES($1,$2,'p2p','SDG',$3,100000,100000,100000)`, n.id, KYCTierUnverified, n.unit)
	interopMust(t, err)
	newCommand := func(key string) MultiLegSettlementParams {
		fee := int64(0)
		payload := P2PCommandPayload{Currency: "SDG", FromWalletID: from.ID.String(), ToWalletID: to.ID.String(), Amount: 100, ReferenceID: key, FromOwnerType: OwnerTypeUser, FromOwnerID: "51", ToOwnerType: OwnerTypeUser, ToOwnerID: "52", ExpectedFeeAmount: &fee, ExpectedCurrencyUnitVersion: &n.unit}
		doc, e := json.Marshal(payload)
		interopMust(t, e)
		_, e = f.runtime.ReserveP2PCommand(f.ctx, P2PCommandReservation{TenantID: n.id, IdempotencyKey: key, WorkflowID: "workflow-" + key, FromWalletID: from.ID, ToWalletID: to.ID, FromOwnerType: OwnerTypeUser, FromOwnerID: "51", ToOwnerType: OwnerTypeUser, ToOwnerID: "52", Command: doc})
		interopMust(t, e)
		return MultiLegSettlementParams{P2PCommandID: key, TenantID: n.id, IdempotencyKey: key, Currency: "SDG", ReferenceType: "p2p", ReferenceID: key, Transfers: []SettlementTransfer{{DebitWalletID: from.ID, CreditWalletID: to.ID, Amount: 100}}, LimitUsage: LimitUsageParams{TenantID: n.id, CommandID: "p2p:" + key, WalletID: from.ID, TransactionType: "p2p", Currency: "SDG", Amount: 100}}
	}
	receipt := func(key, want string) *P2PReceipt {
		r, e := f.runtime.GetP2PReceipt(f.ctx, n.id, "51", key)
		interopMust(t, e)
		if r.Status != want || r.Payload.Amount != 100 || r.Fee != 0 || r.CurrencyUnitID != n.unit {
			t.Fatalf("receipt %+v want %s", r, want)
		}
		return r
	}
	t.Run("recipient lookup is exact and canonical", func(t *testing.T) {
		r, e := f.runtime.GetP2PRecipient(f.ctx, n.id, to.ID)
		interopMust(t, e)
		if r.OwnerID != "52" || r.WalletID != to.ID {
			t.Fatal(r)
		}
		for _, id := range []uuid.UUID{uuid.New(), n.user.ID, n.clearing.ID} {
			_, e = f.runtime.GetP2PRecipient(f.ctx, n.id, id)
			if !errors.Is(e, ErrWalletNotFound) {
				t.Fatalf("lookup should reject %v", e)
			}
		}
		_, e = f.runtime.GetP2PRecipient(f.ctx, "another-tenant", to.ID)
		if !errors.Is(e, ErrWalletNotFound) {
			t.Fatal(e)
		}
	})
	t.Run("reserved and stalled runs never imply complete", func(t *testing.T) {
		p := newCommand("stalled")
		receipt(p.IdempotencyKey, "reserved")
		_, e := f.runtime.RecordP2PCommandRun(f.ctx, n.id, p.IdempotencyKey, "workflow-stalled", "run-1")
		interopMust(t, e)
		receipt(p.IdempotencyKey, "running")
		for _, scope := range []struct{ tenant, owner string }{{n.id, "52"}, {"another-tenant", "51"}} {
			_, e = f.runtime.GetP2PReceipt(f.ctx, scope.tenant, scope.owner, p.IdempotencyKey)
			if !errors.Is(e, ErrP2PCommandNotFound) {
				t.Fatal(e)
			}
		}
	})
	t.Run("failure is durable and blocks late debit", func(t *testing.T) {
		p := newCommand("failed")
		interopMust(t, f.runtime.RecordP2PFailure(f.ctx, n.id, p.IdempotencyKey, "p2p_fee_changed"))
		r := receipt(p.IdempotencyKey, "failed")
		if r.ErrorCode != "p2p_fee_changed" {
			t.Fatal(r)
		}
		_, e := f.worker.PostMultiLegSettlement(f.ctx, p)
		if !errors.Is(e, ErrP2PCommandFailed) {
			t.Fatalf("late post %v", e)
		}
	})
	t.Run("ledger survives workflow failure and replay", func(t *testing.T) {
		p := newCommand("settled")
		posted, e := f.worker.PostMultiLegSettlement(f.ctx, p)
		interopMust(t, e)
		interopMust(t, f.worker.RecordP2PFailure(f.ctx, n.id, p.IdempotencyKey, "payment_not_completed"))
		r := receipt(p.IdempotencyKey, "completed")
		if r.TransactionID != posted.TransactionID || r.TransactionID <= 0 {
			t.Fatal(r)
		}
		again, e := f.worker.PostMultiLegSettlement(f.ctx, p)
		interopMust(t, e)
		if !again.Existing || again.TransactionID != posted.TransactionID {
			t.Fatal(again)
		}
	})
	t.Run("immutable reviewed amount cannot change in late activity", func(t *testing.T) {
		p := newCommand("forged")
		p.Transfers[0].Amount++
		_, e := f.worker.PostMultiLegSettlement(f.ctx, p)
		if !errors.Is(e, ErrInvalidP2PCommand) {
			t.Fatal(e)
		}
	})

	t.Run("unreviewed fee cannot be charged by a late activity", func(t *testing.T) {
		fees, e := f.runtime.EnsureWallet(f.ctx, EnsureWalletParams{TenantID: n.id, OwnerType: OwnerTypeSystem, OwnerID: SystemFees, Currency: "SDG", CurrencyUnitID: n.unit, KYCTier: KYCTierUnverified})
		interopMust(t, e)
		p := newCommand("wrong-fee")
		p.Transfers = append(p.Transfers, SettlementTransfer{DebitWalletID: from.ID, CreditWalletID: fees.ID, Amount: 1})
		_, e = f.worker.PostMultiLegSettlement(f.ctx, p)
		if !errors.Is(e, ErrP2PFeeChanged) {
			t.Fatalf("unreviewed fee: %v", e)
		}
		receipt(p.IdempotencyKey, "reserved")
	})
	t.Run("failure and settlement serialize under concurrent completion", func(t *testing.T) {
		for i := 0; i < 8; i++ {
			p := newCommand(fmt.Sprintf("race-%d", i))
			start := make(chan struct{})
			posted := make(chan error, 1)
			failed := make(chan error, 1)
			go func() { <-start; _, e := f.worker.PostMultiLegSettlement(f.ctx, p); posted <- e }()
			go func() {
				<-start
				failed <- f.runtime.RecordP2PFailure(f.ctx, n.id, p.IdempotencyKey, "payment_not_completed")
			}()
			close(start)
			postErr, failErr := <-posted, <-failed
			interopMust(t, failErr)
			r, e := f.runtime.GetP2PReceipt(f.ctx, n.id, "51", p.IdempotencyKey)
			interopMust(t, e)
			if postErr == nil {
				if r.Status != "completed" || r.TransactionID <= 0 {
					t.Fatal(r)
				}
			} else if !errors.Is(postErr, ErrP2PCommandFailed) || r.Status != "failed" || r.TransactionID != 0 {
				t.Fatalf("post=%v receipt=%+v", postErr, r)
			}
		}
	})

	t.Run("unrelated ledger key is not proof this payment completed", func(t *testing.T) {
		p := newCommand("p2p-fixture-funds")
		interopMust(t, f.worker.RecordP2PFailure(f.ctx, n.id, p.IdempotencyKey, "payment_not_completed"))
		receipt(p.IdempotencyKey, "failed")
	})
	t.Run("failure records cannot be edited or deleted by runtime", func(t *testing.T) {
		_, e := f.runtime.DB.ExecContext(f.ctx, `DELETE FROM p2p_command_failures WHERE tenant_id=$1`, n.id)
		interopSQLState(t, e, "42501")
		_, e = f.runtime.DB.ExecContext(f.ctx, `UPDATE p2p_command_failures SET error_code='payment_not_completed' WHERE tenant_id=$1`, n.id)
		interopSQLState(t, e, "42501")
	})
}
