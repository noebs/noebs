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

	fromWallet, err := runtimeStore.EnsureWallet(ctx, EnsureWalletParams{
		TenantID: tenantID, OwnerType: OwnerTypeUser, OwnerID: "101", UserID: 101,
		Currency: "USD", KYCTier: KYCTierUnverified,
	})
	if err != nil {
		t.Fatal(err)
	}
	toWallet, err := runtimeStore.EnsureWallet(ctx, EnsureWalletParams{
		TenantID: tenantID, OwnerType: OwnerTypeUser, OwnerID: "202", UserID: 202,
		Currency: "USD", KYCTier: KYCTierUnverified,
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
		Amount: 25, Currency: "USD", Reason: "authority test", Status: ManualTransferStatusPending,
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
		Amount: 100, Currency: "USD", IdempotencyKey: "deposit-runtime-authority",
		WorkflowID: "deposit-runtime-authority", Metadata: RawJSON(`{}`), Region: "",
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
}
