package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/testdb"
	basestore "github.com/adonese/noebs/store"
)

func TestEnsureWalletRejectsMismatchedReplay(t *testing.T) {
	ctx, store, tenantID := newWalletStoreIntegration(t)

	params := EnsureWalletParams{
		TenantID:  tenantID,
		OwnerType: OwnerTypeUser,
		OwnerID:   "user-42",
		UserID:    42,
		Currency:  "USD",
		KYCTier:   KYCTierUnverified,
	}
	created, err := store.EnsureWallet(ctx, params)
	if err != nil {
		t.Fatalf("ensure wallet: %v", err)
	}
	replayed, err := store.EnsureWallet(ctx, params)
	if err != nil {
		t.Fatalf("replay wallet: %v", err)
	}
	if replayed.ID != created.ID {
		t.Fatalf("replayed wallet id = %s, want %s", replayed.ID, created.ID)
	}

	userMismatch := params
	userMismatch.UserID = 99
	if _, err := store.EnsureWallet(ctx, userMismatch); !errors.Is(err, ErrDuplicateWallet) {
		t.Fatalf("user mismatch replay error = %v, want %v", err, ErrDuplicateWallet)
	}

	ownerMismatch := params
	ownerMismatch.OwnerID = "user-99"
	if _, err := store.EnsureWallet(ctx, ownerMismatch); !errors.Is(err, ErrDuplicateWallet) {
		t.Fatalf("owner mismatch replay error = %v, want %v", err, ErrDuplicateWallet)
	}

	kycMismatch := params
	kycMismatch.KYCTier = "verified"
	if _, err := store.EnsureWallet(ctx, kycMismatch); !errors.Is(err, ErrDuplicateWallet) {
		t.Fatalf("kyc mismatch replay error = %v, want %v", err, ErrDuplicateWallet)
	}
}

func TestManualTransferAndApprovalReplaysAreExact(t *testing.T) {
	ctx, store, tenantID := newWalletStoreIntegration(t)
	wallet, err := store.EnsureWallet(ctx, EnsureWalletParams{
		TenantID:  tenantID,
		OwnerType: OwnerTypeUser,
		OwnerID:   "manual-user",
		UserID:    101,
		Currency:  "USD",
		KYCTier:   KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure manual transfer wallet: %v", err)
	}
	requesterID := insertWalletOperator(t, ctx, store, "requester")
	approverID := insertWalletOperator(t, ctx, store, "approver")

	transfer := ManualTransfer{
		TenantID:              tenantID,
		WorkflowID:            "wf-1",
		IdempotencyKey:        "idem-1",
		TransferType:          ManualTransferTypeDebit,
		WalletID:              sql.NullString{String: wallet.ID.String(), Valid: true},
		Amount:                100,
		Currency:              "USD",
		Reason:                "manual adjustment",
		Status:                ManualTransferStatusPending,
		RequestedByOperatorID: requesterID,
	}
	created, err := store.CreateManualTransfer(ctx, transfer)
	if err != nil {
		t.Fatalf("create manual transfer: %v", err)
	}
	replayed, err := store.CreateManualTransfer(ctx, transfer)
	if err != nil {
		t.Fatalf("replay manual transfer: %v", err)
	}
	if replayed.ID != created.ID {
		t.Fatalf("replayed transfer id = %d, want %d", replayed.ID, created.ID)
	}

	amountMismatch := transfer
	amountMismatch.Amount++
	if _, err := store.CreateManualTransfer(ctx, amountMismatch); !errors.Is(err, ErrDuplicateManualTransfer) {
		t.Fatalf("manual transfer amount mismatch error = %v, want %v", err, ErrDuplicateManualTransfer)
	}
	workflowMismatch := transfer
	workflowMismatch.WorkflowID = "wf-2"
	if _, err := store.CreateManualTransfer(ctx, workflowMismatch); !errors.Is(err, ErrDuplicateManualTransfer) {
		t.Fatalf("manual transfer workflow mismatch error = %v, want %v", err, ErrDuplicateManualTransfer)
	}

	foreignWallet, err := store.EnsureWallet(ctx, EnsureWalletParams{
		TenantID:  "other-tenant",
		OwnerType: OwnerTypeUser,
		OwnerID:   "foreign-manual-user",
		UserID:    201,
		Currency:  "USD",
		KYCTier:   KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure foreign manual transfer wallet: %v", err)
	}
	foreignWalletTransfer := transfer
	foreignWalletTransfer.WorkflowID = "wf-foreign-wallet"
	foreignWalletTransfer.IdempotencyKey = "idem-foreign-wallet"
	foreignWalletTransfer.WalletID = sql.NullString{String: foreignWallet.ID.String(), Valid: true}
	if _, err := store.CreateManualTransfer(ctx, foreignWalletTransfer); !errors.Is(err, ErrWalletNotFound) {
		t.Fatalf("foreign wallet manual transfer error = %v, want %v", err, ErrWalletNotFound)
	}

	approval := ManualTransferApproval{
		TenantID:            tenantID,
		ManualTransferID:    created.ID,
		DecidedByOperatorID: approverID,
		Decision:            ManualTransferStatusApproved,
	}
	storedApproval, err := store.AddManualTransferApproval(ctx, approval)
	if err != nil {
		t.Fatalf("add manual transfer approval: %v", err)
	}
	replayedApproval, err := store.AddManualTransferApproval(ctx, approval)
	if err != nil {
		t.Fatalf("replay manual transfer approval: %v", err)
	}
	if replayedApproval.ID != storedApproval.ID {
		t.Fatalf("replayed approval id = %d, want %d", replayedApproval.ID, storedApproval.ID)
	}
	decisionMismatch := approval
	decisionMismatch.Decision = ManualTransferStatusRejected
	if _, err := store.AddManualTransferApproval(ctx, decisionMismatch); !errors.Is(err, ErrDuplicateManualApproval) {
		t.Fatalf("manual approval decision mismatch error = %v, want %v", err, ErrDuplicateManualApproval)
	}

	selfApproval := approval
	selfApproval.DecidedByOperatorID = requesterID
	if _, err := store.AddManualTransferApproval(ctx, selfApproval); !errors.Is(err, ErrApproverIsRequester) {
		t.Fatalf("self manual approval error = %v, want %v", err, ErrApproverIsRequester)
	}

	terminalDecidedByOperatorID := insertWalletOperator(t, ctx, store, "terminal-approver")
	approvedAt := time.Now().UTC()
	proofOfPayment := "receipt-1"
	if err := store.UpdateManualTransferStatus(ctx, tenantID, transfer.WorkflowID, ManualTransferStatusUpdate{
		Status:               ManualTransferStatusApproved,
		ApprovedByOperatorID: sql.NullInt64{Int64: approverID, Valid: true},
		ApprovedAt:           sql.NullTime{Time: approvedAt, Valid: true},
		ProofOfPayment:       sql.NullString{String: proofOfPayment, Valid: true},
	}); err != nil {
		t.Fatalf("approve manual transfer: %v", err)
	}
	terminalApproval := approval
	terminalApproval.DecidedByOperatorID = terminalDecidedByOperatorID
	if _, err := store.AddManualTransferApproval(ctx, terminalApproval); !errors.Is(err, ErrInvalidStatusTransition) {
		t.Fatalf("terminal transfer approval error = %v, want %v", err, ErrInvalidStatusTransition)
	}

	completedAt := approvedAt.Add(time.Hour)
	if err := store.UpdateManualTransferStatus(ctx, tenantID, transfer.WorkflowID, ManualTransferStatusUpdate{
		Status:      ManualTransferStatusCompleted,
		CompletedAt: sql.NullTime{Time: completedAt, Valid: true},
	}); err != nil {
		t.Fatalf("complete manual transfer: %v", err)
	}
	completed, err := store.GetManualTransferByWorkflow(ctx, tenantID, transfer.WorkflowID)
	if err != nil {
		t.Fatalf("get completed manual transfer: %v", err)
	}
	if completed.Status != ManualTransferStatusCompleted {
		t.Fatalf("completed transfer status = %s, want %s", completed.Status, ManualTransferStatusCompleted)
	}
	if !completed.ApprovedByOperatorID.Valid || completed.ApprovedByOperatorID.Int64 != approverID {
		t.Fatalf("completed transfer approved_by = %+v, want %d", completed.ApprovedByOperatorID, approverID)
	}
	if !sameManualTransferNullTime(completed.ApprovedAt, sql.NullTime{Time: approvedAt, Valid: true}) {
		t.Fatalf("completed transfer approved_at = %+v, want %s", completed.ApprovedAt, approvedAt)
	}
	if !completed.ProofOfPayment.Valid || completed.ProofOfPayment.String != proofOfPayment {
		t.Fatalf("completed transfer proof_of_payment = %+v, want %q", completed.ProofOfPayment, proofOfPayment)
	}
	if !sameManualTransferNullTime(completed.CompletedAt, sql.NullTime{Time: completedAt, Valid: true}) {
		t.Fatalf("completed transfer completed_at = %+v, want %s", completed.CompletedAt, completedAt)
	}
}

func TestResolveOperatorIdentityIsImmutableAndConcurrent(t *testing.T) {
	ctx, store, _ := newWalletStoreIntegration(t)
	const callers = 32
	ids := make(chan int64, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			operator, err := store.ResolveOperatorIdentity(ctx, "https://identity.example/realms/noebs", "operator-1")
			if err != nil {
				errs <- err
				return
			}
			ids <- operator.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatalf("resolve concurrent operator: %v", err)
	}
	var operatorID int64
	for id := range ids {
		if operatorID == 0 {
			operatorID = id
		}
		if id != operatorID {
			t.Fatalf("operator id = %d, want stable id %d", id, operatorID)
		}
	}
	otherIssuer, err := store.ResolveOperatorIdentity(ctx, "https://other-identity.example/realms/noebs", "operator-1")
	if err != nil {
		t.Fatalf("resolve other issuer: %v", err)
	}
	otherSubject, err := store.ResolveOperatorIdentity(ctx, "https://identity.example/realms/noebs", "operator-2")
	if err != nil {
		t.Fatalf("resolve other subject: %v", err)
	}
	if otherIssuer.ID == operatorID || otherSubject.ID == operatorID || otherIssuer.ID == otherSubject.ID {
		t.Fatalf("distinct immutable identities collapsed: %d, %d, %d", operatorID, otherIssuer.ID, otherSubject.ID)
	}

	db, err := store.ensureDB()
	if err != nil {
		t.Fatalf("ensure db: %v", err)
	}
	var count int
	if err := db.GetContext(ctx, &count, `SELECT COUNT(*) FROM operator_identities
		WHERE issuer = $1 AND subject = $2`, "https://identity.example/realms/noebs", "operator-1"); err != nil {
		t.Fatalf("count operator projection: %v", err)
	}
	if count != 1 {
		t.Fatalf("operator projection rows = %d, want 1", count)
	}
	for _, table := range []string{"admin_users", "admin_roles"} {
		var exists bool
		if err := db.GetContext(ctx, &exists, `SELECT to_regclass($1) IS NOT NULL`, table); err != nil {
			t.Fatalf("inspect legacy table %s: %v", table, err)
		}
		if exists {
			t.Fatalf("legacy credential table %s exists", table)
		}
	}
}

func newWalletStoreIntegration(t *testing.T) (context.Context, *Store, string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)

	container, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	dbName := fmt.Sprintf("noebs_wallet_store_%d", time.Now().UnixNano())
	dbURL, err := container.CreateDatabase(ctx, dbName)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}

	db, err := basestore.OpenFromConfig(dbURL, basestore.DriverPostgres)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer dropCancel()
		_ = container.DropDatabase(dropCtx, dbName)
	})

	tenantID := "tenant"
	if err := basestore.MigrateScope(ctx, db, basestore.MigrationScopeWalletLedger); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	return ctx, New(db), tenantID
}

func insertWalletOperator(t *testing.T, ctx context.Context, store *Store, subject string) int64 {
	t.Helper()
	operator, err := store.ResolveOperatorIdentity(ctx, "https://identity.example/realms/noebs", subject)
	if err != nil {
		t.Fatalf("resolve operator identity: %v", err)
	}
	return operator.ID
}
