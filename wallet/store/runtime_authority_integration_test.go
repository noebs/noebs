package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/testdb"
	basestore "github.com/adonese/noebs/store"
)

func TestRuntimeAuthorityReservesImmutableCommands(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)

	container, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	const databaseName = "wallet_ledger"
	migrationURL, err := container.CreateDatabaseForRole(ctx, databaseName, "wallet_ledger_migrate")
	if err != nil {
		t.Fatal(err)
	}
	migrationDB, err := basestore.OpenFromConfig(migrationURL, basestore.DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = migrationDB.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer dropCancel()
		_ = container.DropDatabase(dropCtx, databaseName)
	})
	if err := basestore.MigrateScope(ctx, migrationDB, basestore.MigrationScopeWalletLedger); err != nil {
		t.Fatal(err)
	}
	const tenantID = "tenant-runtime-authority"
	provisionWalletStoreTestTenant(t, ctx, migrationDB, tenantID, "Runtime Authority Tenant")
	if _, err := migrationDB.ExecContext(ctx, `INSERT INTO psp_configs(
		tenant_id, provider_code, provider_name, api_base_url,
		idempotency_header_name, deposit_response_mapping
	) VALUES($1, 'test', 'Test PSP', 'https://psp.invalid', 'Idempotency-Key', '{}')`, tenantID); err != nil {
		t.Fatal(err)
	}

	runtimeURL, err := container.DatabaseURLForRole(databaseName, "wallet_ledger_runtime")
	if err != nil {
		t.Fatal(err)
	}
	runtimeDB, err := basestore.OpenFromConfig(runtimeURL, basestore.DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeDB.Close() })
	runtimeStore := New(runtimeDB)
	usdUnitID := testCurrencyUnitID(t, ctx, runtimeStore, "USD")

	fromWallet, err := runtimeStore.EnsureWallet(ctx, EnsureWalletParams{
		TenantID: tenantID, OwnerType: OwnerTypeUser, OwnerID: "101", UserID: 101,
		Currency: "USD", CurrencyUnitID: usdUnitID, KYCTier: KYCTierUnverified,
	})
	if err != nil {
		t.Fatal(err)
	}
	toWallet, err := runtimeStore.EnsureWallet(ctx, EnsureWalletParams{
		TenantID: tenantID, OwnerType: OwnerTypeUser, OwnerID: "202", UserID: 202,
		Currency: "USD", CurrencyUnitID: usdUnitID, KYCTier: KYCTierUnverified,
	})
	if err != nil {
		t.Fatal(err)
	}
	requester, err := runtimeStore.ResolveOperatorIdentity(ctx, "https://issuer.example", "requester")
	if err != nil {
		t.Fatal(err)
	}
	approver, err := runtimeStore.ResolveOperatorIdentity(ctx, "https://issuer.example", "approver")
	if err != nil {
		t.Fatal(err)
	}
	transfer, err := runtimeStore.CreateManualTransfer(ctx, ManualTransfer{
		TenantID: tenantID, WorkflowID: "manual-runtime-authority", IdempotencyKey: "manual-runtime-authority",
		TransferType: ManualTransferTypeCredit, WalletID: sql.NullString{String: fromWallet.ID.String(), Valid: true},
		Amount: 25, Currency: "USD", CurrencyUnitID: fromWallet.CurrencyUnitID,
		Reason: "authority test", Status: ManualTransferStatusPending,
		RequestedByOperatorID: requester.ID, ApprovalTimeoutSeconds: 300,
	})
	if err != nil {
		t.Fatalf("create manual transfer: %v", err)
	}
	decision := WorkflowDecision{
		TenantID: tenantID, WorkflowID: transfer.WorkflowID, Kind: WorkflowDecisionManualTransfer,
		SubjectID: transfer.ID, Approved: true, DecidedByOperatorID: approver.ID,
		ProofOfPayment: sql.NullString{String: "proof-runtime-authority", Valid: true},
	}
	firstDecision, err := runtimeStore.ReserveWorkflowDecision(ctx, decision)
	if err != nil {
		t.Fatalf("reserve workflow decision: %v", err)
	}
	replayedDecision, err := runtimeStore.ReserveWorkflowDecision(ctx, decision)
	if err != nil {
		t.Fatalf("replay workflow decision: %v", err)
	}
	if replayedDecision.DecidedAt != firstDecision.DecidedAt {
		t.Fatal("workflow decision replay did not return the durable decision")
	}

	p2p := P2PCommandReservation{
		TenantID: tenantID, IdempotencyKey: "p2p-runtime-authority", WorkflowID: "p2p-runtime-authority",
		FromWalletID: fromWallet.ID, ToWalletID: toWallet.ID,
		FromOwnerType: fromWallet.OwnerType, FromOwnerID: fromWallet.OwnerID,
		ToOwnerType: toWallet.OwnerType, ToOwnerID: toWallet.OwnerID,
		Command: RawJSON(`{"amount":100,"currency":"USD"}`),
	}
	firstP2P, err := runtimeStore.ReserveP2PCommand(ctx, p2p)
	if err != nil {
		t.Fatalf("reserve P2P command: %v", err)
	}
	replayedP2P, err := runtimeStore.ReserveP2PCommand(ctx, p2p)
	if err != nil {
		t.Fatalf("replay P2P command: %v", err)
	}
	if replayedP2P.CreatedAt != firstP2P.CreatedAt {
		t.Fatal("P2P replay did not return the durable command")
	}
	conflictingP2P := p2p
	conflictingP2P.Command = RawJSON(`{"amount":101,"currency":"USD"}`)
	if _, err := runtimeStore.ReserveP2PCommand(ctx, conflictingP2P); !errors.Is(err, ErrDuplicateP2PCommand) {
		t.Fatalf("conflicting P2P replay error = %v, want %v", err, ErrDuplicateP2PCommand)
	}
	if _, err := runtimeStore.RecordP2PCommandRun(
		ctx, tenantID, p2p.IdempotencyKey, p2p.WorkflowID, "p2p-run-1",
	); err != nil {
		t.Fatalf("record P2P run: %v", err)
	}

	deposit := DepositIntent{
		TenantID: tenantID, IntentReference: "deposit-runtime-authority", ProviderCode: "test",
		WalletID: fromWallet.ID, OwnerType: fromWallet.OwnerType, OwnerID: fromWallet.OwnerID,
		Amount: 100, Currency: "USD", CurrencyUnitID: fromWallet.CurrencyUnitID,
		IdempotencyKey: "deposit-runtime-authority",
		WorkflowID:     "deposit-runtime-authority", Metadata: RawJSON(`{}`), Region: "",
		RawRequest: RawJSON(`{"amount":100,"currency":"USD"}`),
	}
	firstDeposit, err := runtimeStore.ReserveDepositIntent(ctx, deposit)
	if err != nil {
		t.Fatalf("reserve deposit intent: %v", err)
	}
	replayedDeposit, err := runtimeStore.ReserveDepositIntent(ctx, deposit)
	if err != nil {
		t.Fatalf("replay deposit intent: %v", err)
	}
	if replayedDeposit.ID != firstDeposit.ID || replayedDeposit.CreatedAt != firstDeposit.CreatedAt {
		t.Fatal("deposit replay did not return the durable intent")
	}
	conflictingDeposit := deposit
	conflictingDeposit.Amount++
	if _, err := runtimeStore.ReserveDepositIntent(ctx, conflictingDeposit); !errors.Is(err, ErrDuplicateDepositIntent) {
		t.Fatalf("conflicting deposit replay error = %v, want %v", err, ErrDuplicateDepositIntent)
	}
	if _, err := runtimeStore.RecordDepositIntentRun(
		ctx, tenantID, deposit.IntentReference, deposit.WorkflowID, "deposit-run-1",
	); err != nil {
		t.Fatalf("record deposit run: %v", err)
	}
	if _, err := migrationDB.ExecContext(ctx, `UPDATE psp_transactions SET status = 'pending' WHERE tenant_id = $1 AND client_reference = $2`, tenantID, deposit.IntentReference); err != nil {
		t.Fatal(err)
	}
	transaction, err := runtimeStore.GetPSPTransactionByReference(ctx, tenantID, deposit.IntentReference)
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := runtimeStore.ResolvePSPTransaction(ctx, lifecycleTestCommand(transaction, approver.ID))
	if err != nil {
		t.Fatalf("runtime resolve transaction through immutable command: %v", err)
	}
	if resolved.Substatus != "settlement_pending" || resolved.StatusVersion != transaction.StatusVersion+1 {
		t.Fatalf("resolved lifecycle = %+v", resolved)
	}
	if _, err := runtimeDB.ExecContext(ctx, `UPDATE psp_transactions SET status = 'failed'`); err == nil {
		t.Fatal("runtime must not update transaction outcomes directly")
	}
	if _, err := runtimeDB.ExecContext(ctx, `UPDATE psp_manual_resolutions SET evidence_reference = 'rewritten'`); err == nil {
		t.Fatal("runtime must not rewrite resolution evidence")
	}
	if _, err := runtimeDB.ExecContext(ctx, `INSERT INTO transaction_status_events(tenant_id,aggregate_id,version,status,substatus) VALUES('tenant-runtime-authority','forged',1,'completed','offline')`); err == nil {
		t.Fatal("runtime must not forge transition events")
	}
	workerURL, err := container.DatabaseURLForRole(databaseName, "wallet_ledger_worker")
	if err != nil {
		t.Fatal(err)
	}
	workerDB, err := basestore.OpenFromConfig(workerURL, basestore.DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workerDB.Close() })
	outbox, err := NewStatusEventOutbox(New(workerDB), "noebs.status.changed.v1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	events, err := outbox.ClaimPendingTransactionEvents(ctx, 20)
	if err != nil || len(events) == 0 {
		t.Fatalf("worker claim events = %+v, %v", events, err)
	}
	if err := outbox.MarkTransactionEventPublished(ctx, events[0].ID); err != nil {
		t.Fatalf("worker ack event: %v", err)
	}
}
