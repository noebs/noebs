package store

import (
	"errors"
	"sync"
	"testing"
)

func TestSharedOutboundLimitsUseExistingUsageAndSerializeRails(t *testing.T) {
	f := newInteropFixture(t)
	newTenant := func(t *testing.T, name string) *interopTenant {
		t.Helper()
		n := f.tenant(t, name, false)
		_, err := f.migrate.ExecContext(f.ctx, `INSERT INTO transaction_limits
			(tenant_id,kyc_tier,transaction_type,currency,currency_unit_version_id,daily_limit,monthly_limit,per_transaction_limit)
			VALUES($1,'unverified','p2p','SDG',$2,100000,100000,100000)`, n.id, n.unit)
		interopMust(t, err)
		return n
	}
	policy := func(t *testing.T, n *interopTenant, daily, monthly int64) {
		t.Helper()
		_, err := f.migrate.ExecContext(f.ctx, `INSERT INTO shared_outbound_limits
			(tenant_id,kyc_tier,currency,currency_unit_version_id,daily_limit,monthly_limit,enabled)
			VALUES($1,'unverified','SDG',$2,$3,$4,true)`, n.id, n.unit, daily, monthly)
		interopMust(t, err)
	}
	params := func(n *interopTenant, kind, key string, amount int64) LimitUsageParams {
		return LimitUsageParams{TenantID: n.id, WalletID: n.user.ID, TransactionType: kind, CommandID: key, Currency: "SDG", Amount: amount}
	}
	reserve := func(t *testing.T, p LimitUsageParams) *LimitUsageReservation {
		t.Helper()
		r, err := f.worker.ReserveLimitUsage(f.ctx, p)
		interopMust(t, err)
		return r
	}
	exceeded := func(t *testing.T, p LimitUsageParams, reason string) {
		t.Helper()
		_, err := f.worker.ReserveLimitUsage(f.ctx, p)
		var limitError TransactionLimitExceededError
		if !errors.As(err, &limitError) || limitError.Reason != reason {
			t.Fatalf("reservation error = %v, want %s", err, reason)
		}
	}

	t.Run("preactivation reserved and consumed usage count without backfill", func(t *testing.T) {
		n := newTenant(t, "shared-retroactive")
		spent := params(n, "interop_out", "old-outbound", 60)
		reserve(t, spent)
		interopMust(t, f.worker.ConsumeLimitUsage(f.ctx, ConsumeLimitUsageParams{Reservation: spent, LedgerTransactionID: n.seedID}))
		pending := params(n, "p2p", "old-pending", 20)
		reserve(t, pending)
		policy(t, n, 100, 200)
		exceeded(t, params(n, "interop_out", "over-budget", 21), LimitExceededDaily)
		exact := params(n, "p2p", "exact-remainder", 20)
		first := reserve(t, exact)
		if replay := reserve(t, exact); first.ID != replay.ID {
			t.Fatal("same command reserved twice")
		}
		exceeded(t, params(n, "interop_out", "last-unit", 1), LimitExceededDaily)
		interopMust(t, f.worker.ReleaseLimitUsage(f.ctx, pending))
		reserve(t, params(n, "interop_out", "released-room", 20))
		exceeded(t, params(n, "p2p", "still-full", 1), LimitExceededDaily)
		var daily int64
		interopMust(t, f.migrate.GetContext(f.ctx, &daily, `SELECT sum(reserved_amount+consumed_amount)
			FROM transaction_limit_period_usage WHERE tenant_id=$1 AND wallet_id=$2
			AND transaction_type IN ('p2p','interop_out') AND period_kind='daily'
			AND period_start=(clock_timestamp() AT TIME ZONE 'UTC')::date`, n.id, n.user.ID))
		if daily != 100 {
			t.Fatalf("shared usage = %d, want100", daily)
		}
	})

	t.Run("concurrent cross rail reservations share one remaining budget", func(t *testing.T) {
		n := newTenant(t, "shared-race")
		policy(t, n, 100, 200)
		start := make(chan struct{})
		results := make(chan error, 2)
		var workers sync.WaitGroup
		for _, kind := range []string{"p2p", "interop_out"} {
			workers.Add(1)
			go func(kind string) {
				defer workers.Done()
				<-start
				_, err := f.worker.ReserveLimitUsage(f.ctx, params(n, kind, "competing-"+kind, 60))
				results <- err
			}(kind)
		}
		close(start)
		workers.Wait()
		close(results)
		successes, denied := 0, 0
		for err := range results {
			if err == nil {
				successes++
				continue
			}
			var limitError TransactionLimitExceededError
			if !errors.As(err, &limitError) || limitError.Reason != LimitExceededDaily {
				t.Fatal(err)
			}
			denied++
		}
		if successes != 1 || denied != 1 {
			t.Fatalf("race succeeded%d denied%d", successes, denied)
		}
	})

	t.Run("monthly and UTC period scope", func(t *testing.T) {
		n := newTenant(t, "shared-month")
		old := params(n, "interop_out", "earlier-day", 90)
		reserve(t, old)
		interopMust(t, f.worker.ConsumeLimitUsage(f.ctx, ConsumeLimitUsageParams{Reservation: old, LedgerTransactionID: n.seedID}))
		// Isolated historical fixture: only the current monthly counter remains
		// relevant to the next admission; the prior daily counter is outside today.
		_, err := f.migrate.ExecContext(f.ctx, `UPDATE transaction_limit_period_usage SET period_start=period_start-1
			WHERE tenant_id=$1 AND transaction_type='interop_out' AND period_kind='daily'`, n.id)
		interopMust(t, err)
		policy(t, n, 100, 100)
		exceeded(t, params(n, "p2p", "monthly-over", 11), LimitExceededMonthly)
		reserve(t, params(n, "p2p", "monthly-exact", 10))
	})

	t.Run("unconfigured disabled wrong tier and other directions unchanged", func(t *testing.T) {
		for _, mode := range []string{"absent", "disabled", "other-tier"} {
			n := newTenant(t, "shared-"+mode)
			if mode != "absent" {
				policy(t, n, 10, 10)
				query := `UPDATE shared_outbound_limits SET enabled=false WHERE tenant_id=$1`
				if mode == "other-tier" {
					query = `UPDATE shared_outbound_limits SET kyc_tier='verified' WHERE tenant_id=$1`
				}
				_, err := f.migrate.ExecContext(f.ctx, query, n.id)
				interopMust(t, err)
			}
			reserve(t, params(n, "p2p", mode+"-local", 60))
			reserve(t, params(n, "interop_out", mode+"-external", 60))
		}
		n := newTenant(t, "shared-incoming")
		policy(t, n, 10, 10)
		reserve(t, params(n, "interop_in", "incoming-not-outbound", 100))
		reserve(t, params(n, "p2p", "outbound-limit", 10))
	})

	t.Run("runtime and worker read policy but cannot change it", func(t *testing.T) {
		n := newTenant(t, "shared-authority")
		policy(t, n, 100, 200)
		for _, role := range []*Store{f.runtime, f.worker} {
			var count int
			interopMust(t, role.DB.GetContext(f.ctx, &count, `SELECT count(*) FROM shared_outbound_limits WHERE tenant_id=$1`, n.id))
			if count != 1 {
				t.Fatal("policy read failed")
			}
			for _, query := range []string{
				`UPDATE shared_outbound_limits SET enabled=false WHERE tenant_id=$1`,
				`DELETE FROM shared_outbound_limits WHERE tenant_id=$1`,
				`INSERT INTO shared_outbound_limits(tenant_id,kyc_tier,currency,currency_unit_version_id,daily_limit,monthly_limit,enabled) SELECT tenant_id,'forged',currency,currency_unit_version_id,daily_limit,monthly_limit,enabled FROM shared_outbound_limits WHERE tenant_id=$1`,
			} {
				_, err := role.DB.ExecContext(f.ctx, query, n.id)
				interopSQLState(t, err, "42501")
			}
			_, err := role.DB.ExecContext(f.ctx, `TRUNCATE shared_outbound_limits`)
			interopSQLState(t, err, "42501")
		}
	})

	t.Run("actual interop arm obeys earlier local reservation", func(t *testing.T) {
		n := newTenant(t, "shared-native-admission")
		policy(t, n, 100, 200)
		reserve(t, params(n, "p2p", "local-already-reserved", 60))
		q := n.outgoingQuote(t, 50)
		n.ready(t, q)
		tr := n.request(t, q)
		claim := n.claim(t, tr)
		_, err := f.worker.ArmInteropTransfer(f.ctx, n.id, tr.ID, claim)
		var limitError TransactionLimitExceededError
		if !errors.As(err, &limitError) || limitError.Reason != LimitExceededDaily {
			t.Fatalf("native arm crossed shared budget: %v", err)
		}
		n.balance(t, n.user.ID, 10000, 10000)
		n.count(t, `SELECT count(*) FROM balance_holds WHERE tenant_id=$1`, 0)
	})

	t.Run("overflow safe sum fails closed", func(t *testing.T) {
		n := newTenant(t, "shared-large-counters")
		reserve(t, params(n, "interop_out", "large-existing", 1))
		_, err := f.migrate.ExecContext(f.ctx, `UPDATE transaction_limit_period_usage SET reserved_amount=9223372036854775807,consumed_amount=9223372036854775807 WHERE tenant_id=$1`, n.id)
		interopMust(t, err)
		policy(t, n, 100, 200)
		exceeded(t, params(n, "p2p", "small-after-large", 1), LimitExceededDaily)
	})
}
