package store

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSharedP2PPolicyActivationAndRollbackPreserveMoneyAndUsage(t *testing.T) {
	f := newInteropFixture(t)
	n := f.tenant(t, "shared-policy-operation", false)
	script, err := os.ReadFile("../../scripts/configure-shared-p2p-policy.sql")
	interopMust(t, err)
	p := map[string]any{"tenant_id": n.id, "kyc_tier": "unverified", "currency": "SDG", "currency_unit_version_id": n.unit,
		"daily_limit": 100000, "monthly_limit": 100000, "per_transaction_limit": 100000,
		"request_id": "isolated-shared-policy-v1", "operator_label": "isolated-regression"}
	run := func(action string, commit bool) error {
		encoded, err := json.Marshal(p)
		if err != nil {
			return err
		}
		tx, err := f.migrate.BeginTxx(f.ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(f.ctx, `SELECT set_config('noebs.p2p_policy',$1,true),set_config('noebs.p2p_action',$2,true)`, string(encoded), action); err != nil {
			return err
		}
		if _, err = tx.ExecContext(f.ctx, string(script)); err != nil {
			return err
		}
		if commit {
			return tx.Commit()
		}
		return tx.Rollback()
	}
	count := func(query string, want int) {
		t.Helper()
		var got int
		interopMust(t, f.migrate.GetContext(f.ctx, &got, query, n.id))
		if got != want {
			t.Fatalf("count=%d want%d for%s", got, want, query)
		}
	}
	existing := LimitUsageParams{TenantID: n.id, WalletID: n.user.ID, TransactionType: "interop_out", CommandID: "prior-outbound-reservation", Currency: "SDG", Amount: 50}
	_, err = f.worker.ReserveLimitUsage(f.ctx, existing)
	interopMust(t, err)
	interopMust(t, run("activate", false))
	count(`SELECT count(*) FROM shared_outbound_limits WHERE tenant_id=$1`, 0)
	count(`SELECT count(*) FROM fee_configs WHERE tenant_id=$1 AND transaction_type='p2p'`, 0)
	count(`SELECT count(*) FROM wallet_audit_log WHERE tenant_id=$1`, 0)
	interopMust(t, run("activate", true))
	interopMust(t, run("activate", true))
	count(`SELECT count(*) FROM shared_outbound_limits WHERE tenant_id=$1 AND enabled`, 1)
	count(`SELECT count(*) FROM fee_configs WHERE tenant_id=$1 AND transaction_type='p2p' AND is_active AND percentage_fee=0 AND flat_fee=0`, 1)
	count(`SELECT count(*) FROM transaction_limits WHERE tenant_id=$1 AND transaction_type='p2p' AND is_active`, 1)
	count(`SELECT count(*) FROM wallet_audit_log WHERE tenant_id=$1`, 1)
	p["operator_label"] = "different-operation"
	if err := run("rollback", true); err == nil {
		t.Fatal("request identity reused with changed audit policy")
	}
	p["operator_label"] = "isolated-regression"
	_, err = f.migrate.ExecContext(f.ctx, `INSERT INTO transaction_limits(tenant_id,kyc_tier,transaction_type,currency,currency_unit_version_id,daily_limit,monthly_limit,per_transaction_limit,is_active)
		VALUES($1,'verified','p2p','SDG',$2,100000,100000,100000,true)`, n.id, n.unit)
	interopMust(t, err)
	if err := run("rollback", true); err == nil {
		t.Fatal("rollback disabled a fee shared by another active tier")
	}
	count(`SELECT count(*) FROM fee_configs WHERE tenant_id=$1 AND transaction_type='p2p' AND is_active`, 1)
	_, err = f.migrate.ExecContext(f.ctx, `UPDATE transaction_limits SET is_active=false WHERE tenant_id=$1 AND transaction_type='p2p' AND kyc_tier='verified'`, n.id)
	interopMust(t, err)
	interopMust(t, run("rollback", false))
	count(`SELECT count(*) FROM fee_configs WHERE tenant_id=$1 AND transaction_type='p2p' AND is_active`, 1)
	interopMust(t, run("rollback", true))
	interopMust(t, run("rollback", true))
	count(`SELECT count(*) FROM shared_outbound_limits WHERE tenant_id=$1 AND enabled`, 1)
	count(`SELECT count(*) FROM fee_configs WHERE tenant_id=$1 AND transaction_type='p2p' AND is_active`, 0)
	count(`SELECT count(*) FROM transaction_limits WHERE tenant_id=$1 AND transaction_type='p2p' AND is_active`, 0)
	count(`SELECT count(*) FROM transaction_limits WHERE tenant_id=$1 AND transaction_type='interop_out' AND is_active AND daily_limit=100000 AND monthly_limit=100000`, 1)
	count(`SELECT count(*) FROM wallet_audit_log WHERE tenant_id=$1`, 2)
	count(`SELECT count(*) FROM transaction_limit_reservations WHERE tenant_id=$1 AND amount=50 AND status='reserved'`, 1)
	count(`SELECT count(*) FROM transaction_limit_period_usage WHERE tenant_id=$1 AND reserved_amount=50 AND consumed_amount=0`, 2)
	n.balance(t, n.user.ID, 10000, 10000)
	if err := run("activate", true); err == nil {
		t.Fatal("silent reactivation after rollback accepted")
	}
}

func TestSharedP2PPolicyCannotEnableAnotherTierThroughSharedFee(t *testing.T) {
	f := newInteropFixture(t)
	n := f.tenant(t, "shared-policy-other-tier", false)
	script, err := os.ReadFile("../../scripts/configure-shared-p2p-policy.sql")
	interopMust(t, err)
	_, err = f.migrate.ExecContext(f.ctx, `INSERT INTO transaction_limits(tenant_id,kyc_tier,transaction_type,currency,currency_unit_version_id,daily_limit,monthly_limit,per_transaction_limit,is_active)
		VALUES($1,'verified','p2p','SDG',$2,100000,100000,100000,true)`, n.id, n.unit)
	interopMust(t, err)
	p := map[string]any{"tenant_id": n.id, "kyc_tier": "unverified", "currency": "SDG", "currency_unit_version_id": n.unit,
		"daily_limit": 100000, "monthly_limit": 100000, "per_transaction_limit": 100000, "request_id": "other-tier-refused", "operator_label": "isolated-regression"}
	encoded, err := json.Marshal(p)
	interopMust(t, err)
	tx, err := f.migrate.BeginTxx(f.ctx, nil)
	interopMust(t, err)
	_, err = tx.ExecContext(f.ctx, `SELECT set_config('noebs.p2p_policy',$1,true),set_config('noebs.p2p_action','activate',true)`, string(encoded))
	interopMust(t, err)
	if _, err = tx.ExecContext(f.ctx, string(script)); err == nil {
		t.Fatal("new zero fee activated a different tier's preexisting policy")
	}
	interopMust(t, tx.Rollback())
	var count int
	interopMust(t, f.migrate.GetContext(f.ctx, &count, `SELECT count(*) FROM fee_configs WHERE tenant_id=$1`, n.id))
	if count != 0 {
		t.Fatal("rejected operation wrote a fee")
	}
}

func TestSharedP2PPolicyRejectsUnexpectedPriorPolicy(t *testing.T) {
	f := newInteropFixture(t)
	n := f.tenant(t, "shared-policy-cas", false)
	script, err := os.ReadFile("../../scripts/configure-shared-p2p-policy.sql")
	interopMust(t, err)
	p := map[string]any{"tenant_id": n.id, "kyc_tier": "unverified", "currency": "SDG", "currency_unit_version_id": n.unit,
		"daily_limit": 100000, "monthly_limit": 100000, "per_transaction_limit": 99999, "request_id": "cas-refused", "operator_label": "isolated-regression"}
	encoded, err := json.Marshal(p)
	interopMust(t, err)
	tx, err := f.migrate.BeginTxx(f.ctx, nil)
	interopMust(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(f.ctx, `SELECT set_config('noebs.p2p_policy',$1,true),set_config('noebs.p2p_action','activate',true)`, string(encoded))
	interopMust(t, err)
	if _, err = tx.ExecContext(f.ctx, string(script)); err == nil {
		t.Fatal("changed preexisting ceiling was accepted")
	}
}
