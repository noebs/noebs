package activity_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/adonese/noebs/internal/testdb"
	basestore "github.com/adonese/noebs/store"
	walletactivity "github.com/adonese/noebs/wallet/activity"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletvalidation "github.com/adonese/noebs/wallet/validation"
	walletworkflow "github.com/adonese/noebs/wallet/workflow"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func TestP2PLateFreezeIsFinalizedWithoutRetryOrDebit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pg, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	dsn, err := pg.CreateDatabaseForRole(ctx, "wallet_ledger", "wallet_ledger_migrate")
	if err != nil {
		t.Fatal(err)
	}
	migrate, err := basestore.OpenFromConfig(dsn, basestore.DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = migrate.Close(); _ = pg.DropDatabase(context.Background(), "wallet_ledger") })
	if err = basestore.MigrateScope(ctx, migrate, basestore.MigrationScopeWalletLedger); err != nil {
		t.Fatal(err)
	}
	catalog, err := tenantcatalog.New([]tenantcatalog.Tenant{{ID: "late-freeze", Name: "Isolated late-freeze fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = basestore.New(migrate).ProvisionTenantCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	roleStore := func(role string) *walletstore.Store {
		dsn, e := pg.DatabaseURLForRole("wallet_ledger", role)
		if e != nil {
			t.Fatal(e)
		}
		db, e := basestore.OpenFromConfig(dsn, basestore.DriverPostgres)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = db.Close() })
		return walletstore.New(db)
	}
	runtime, worker := roleStore("wallet_ledger_runtime"), roleStore("wallet_ledger_worker")
	unit, err := worker.GetCurrencyUnit(ctx, "SDG", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	ensure := func(kind, owner string, user int64) *walletstore.Wallet {
		w, e := runtime.EnsureWallet(ctx, walletstore.EnsureWalletParams{TenantID: "late-freeze", OwnerType: kind, OwnerID: owner, UserID: user, Currency: "SDG", CurrencyUnitID: unit.ID, KYCTier: walletstore.KYCTierUnverified})
		if e != nil {
			t.Fatal(e)
		}
		return w
	}
	from, to, treasury := ensure("user", "1", 1), ensure("user", "2", 2), ensure("system", walletstore.SystemTreasury, 0)
	_, err = worker.PostSystemDebitDoubleEntry(ctx, walletstore.DoubleEntryParams{TenantID: "late-freeze", IdempotencyKey: "isolated-opening", DebitWalletID: treasury.ID, CreditWalletID: from.ID, Amount: 1000, Currency: "SDG", ReferenceType: "fixture", ReferenceID: "isolated-opening"})
	if err != nil {
		t.Fatal(err)
	}
	var operator int64
	if err = migrate.GetContext(ctx, &operator, `INSERT INTO operator_identities(issuer,subject) VALUES('https://fixture.invalid','late-freeze') RETURNING id`); err != nil {
		t.Fatal(err)
	}
	_, err = migrate.ExecContext(ctx, `INSERT INTO fee_configs(tenant_id,transaction_type,currency,currency_unit_version_id,tier_min,percentage_fee,flat_fee,min_fee,is_active,created_by_operator_id) VALUES('late-freeze','p2p','SDG',$1,0,0,0,0,true,$2)`, unit.ID, operator)
	if err != nil {
		t.Fatal(err)
	}
	fee := int64(0)
	payload := walletstore.P2PCommandPayload{Currency: "SDG", FromWalletID: from.ID.String(), ToWalletID: to.ID.String(), Amount: 100, ReferenceID: "late-freeze-command", FromOwnerType: "user", FromOwnerID: "1", ToOwnerType: "user", ToOwnerID: "2", ExpectedFeeAmount: &fee, ExpectedCurrencyUnitVersion: &unit.ID}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.ReserveP2PCommand(ctx, walletstore.P2PCommandReservation{TenantID: "late-freeze", IdempotencyKey: "late-freeze-command", WorkflowID: "default-test-workflow-id", FromWalletID: from.ID, ToWalletID: to.ID, FromOwnerType: "user", FromOwnerID: "1", ToOwnerType: "user", ToOwnerID: "2", Command: body})
	if err != nil {
		t.Fatal(err)
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(walletactivity.NewP2PActivities(worker))
	validator := walletvalidation.Service{Store: worker}
	env.RegisterActivityWithOptions(func(c context.Context, r walletvalidation.P2PValidationRequest) (*walletvalidation.P2PValidationResult, error) {
		result, e := validator.ValidateP2P(c, r)
		if e != nil {
			return nil, e
		}
		// Freeze only after validation has accepted both active wallets.
		_, e = migrate.ExecContext(c, `UPDATE wallets SET status='frozen' WHERE tenant_id='late-freeze' AND id=$1`, to.ID)
		return result, e
	}, activity.RegisterOptions{Name: walletactivity.ActivityValidateP2PTransfer})
	ledger := walletactivity.NewLedgerActivities(worker)
	env.RegisterActivityWithOptions(ledger.ValidateMultiLegSettlement, activity.RegisterOptions{Name: walletactivity.ActivityValidateMultiLegSettlement})
	var attempts atomic.Int32
	var terminal atomic.Bool
	env.RegisterActivityWithOptions(func(c context.Context, p walletstore.MultiLegSettlementParams) (*walletstore.MultiLegSettlementResult, error) {
		attempts.Add(1)
		result, e := ledger.ExecuteMultiLegSettlement(c, p)
		var appError *temporal.ApplicationError
		terminal.Store(errors.As(e, &appError) && appError.NonRetryable())
		return result, e
	}, activity.RegisterOptions{Name: walletactivity.ActivityExecuteMultiLegSettlement})
	env.ExecuteWorkflow(walletworkflow.P2P, walletworkflow.P2PParams{TenantID: "late-freeze", IdempotencyKey: "late-freeze-command"})
	if env.GetWorkflowError() == nil || attempts.Load() != 1 || !terminal.Load() {
		t.Fatalf("error=%v attempts=%d terminal=%v", env.GetWorkflowError(), attempts.Load(), terminal.Load())
	}
	receipt, err := runtime.GetP2PReceipt(ctx, "late-freeze", "1", "late-freeze-command")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "failed" || receipt.TransactionID != 0 {
		t.Fatalf("late freeze receipt=%+v", receipt)
	}
	for _, check := range []struct {
		w       *walletstore.Wallet
		balance int64
	}{{from, 1000}, {to, 0}} {
		w, e := runtime.GetWallet(ctx, "late-freeze", check.w.ID)
		if e != nil || w.Balance != check.balance || w.AvailableBalance != check.balance {
			t.Fatalf("balance after late freeze=%+v err=%v", w, e)
		}
	}
}
