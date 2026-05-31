package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

func TestCreateOrResetUserTwoFADoesNotDisableEnabledSecret(t *testing.T) {
	ctx, store, tenantID := newWalletStoreIntegration(t)

	userID := int64(42)
	original, err := store.CreateOrResetUserTwoFA(ctx, tenantID, userID, "secret-1")
	if err != nil {
		t.Fatalf("create 2fa: %v", err)
	}
	if original.Enabled {
		t.Fatal("new 2fa record should start disabled")
	}
	enabledAt := time.Now().UTC()
	if err := store.SetUserTwoFAEnabled(ctx, tenantID, userID, true, enabledAt); err != nil {
		t.Fatalf("enable 2fa: %v", err)
	}
	if err := store.TouchUserTwoFALastUsed(ctx, tenantID, userID, enabledAt.Add(time.Second)); err != nil {
		t.Fatalf("touch 2fa last used: %v", err)
	}

	if _, err := store.CreateOrResetUserTwoFA(ctx, tenantID, userID, "secret-2"); !errors.Is(err, ErrUserTwoFAAlreadyEnabled) {
		t.Fatalf("reset enabled 2fa error = %v, want %v", err, ErrUserTwoFAAlreadyEnabled)
	}
	record, err := store.GetUserTwoFA(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("get 2fa after rejected reset: %v", err)
	}
	if !record.Enabled || record.Secret != "secret-1" {
		t.Fatalf("enabled 2fa mutated on rejected reset: enabled=%v secret=%q", record.Enabled, record.Secret)
	}
	if !record.LastUsedAt.Valid {
		t.Fatal("last_used_at should remain after rejected reset")
	}

	disabledAt := enabledAt.Add(2 * time.Second)
	if err := store.SetUserTwoFAEnabled(ctx, tenantID, userID, false, disabledAt); err != nil {
		t.Fatalf("disable 2fa: %v", err)
	}
	reset, err := store.CreateOrResetUserTwoFA(ctx, tenantID, userID, "secret-2")
	if err != nil {
		t.Fatalf("reset disabled 2fa: %v", err)
	}
	if reset.Enabled || reset.Secret != "secret-2" {
		t.Fatalf("disabled reset record = enabled:%v secret:%q", reset.Enabled, reset.Secret)
	}
	if reset.EnabledAt.Valid || reset.DisabledAt.Valid || reset.LastUsedAt.Valid {
		t.Fatalf("reset 2fa should clear enabled/disabled/last-used timestamps: %+v", reset)
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
	requesterID := insertWalletAdmin(t, ctx, store, tenantID, "requester@example.test")
	approverID := insertWalletAdmin(t, ctx, store, tenantID, "approver@example.test")

	transfer := ManualTransfer{
		TenantID:       tenantID,
		WorkflowID:     "wf-1",
		IdempotencyKey: "idem-1",
		TransferType:   ManualTransferTypeDebit,
		WalletID:       sql.NullString{String: wallet.ID.String(), Valid: true},
		Amount:         100,
		Currency:       "USD",
		Reason:         "manual adjustment",
		Status:         ManualTransferStatusPending,
		RequestedBy:    sqlNullInt64(requesterID),
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

	foreignRequesterID := insertWalletAdmin(t, ctx, store, "other-tenant", "foreign-requester@example.test")
	foreignRequesterTransfer := transfer
	foreignRequesterTransfer.WorkflowID = "wf-foreign-requester"
	foreignRequesterTransfer.IdempotencyKey = "idem-foreign-requester"
	foreignRequesterTransfer.RequestedBy = sqlNullInt64(foreignRequesterID)
	if _, err := store.CreateManualTransfer(ctx, foreignRequesterTransfer); !errors.Is(err, ErrAdminUserNotFound) {
		t.Fatalf("foreign requester manual transfer error = %v, want %v", err, ErrAdminUserNotFound)
	}

	approval := ManualTransferApproval{
		TenantID:         tenantID,
		ManualTransferID: created.ID,
		ApproverID:       approverID,
		Decision:         ManualTransferStatusApproved,
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
	selfApproval.ApproverID = requesterID
	if _, err := store.AddManualTransferApproval(ctx, selfApproval); !errors.Is(err, ErrApproverIsRequester) {
		t.Fatalf("self manual approval error = %v, want %v", err, ErrApproverIsRequester)
	}

	foreignApproverID := insertWalletAdmin(t, ctx, store, "other-tenant", "foreign-approver@example.test")
	foreignApproval := approval
	foreignApproval.ApproverID = foreignApproverID
	if _, err := store.AddManualTransferApproval(ctx, foreignApproval); !errors.Is(err, ErrAdminUserNotFound) {
		t.Fatalf("foreign approver error = %v, want %v", err, ErrAdminUserNotFound)
	}

	terminalApproverID := insertWalletAdmin(t, ctx, store, tenantID, "terminal-approver@example.test")
	approvedAt := time.Now().UTC()
	proofOfPayment := "receipt-1"
	if err := store.UpdateManualTransferStatus(ctx, tenantID, transfer.WorkflowID, ManualTransferStatusUpdate{
		Status:         ManualTransferStatusApproved,
		ApprovedBy:     sql.NullInt64{Int64: approverID, Valid: true},
		ApprovedAt:     sql.NullTime{Time: approvedAt, Valid: true},
		ProofOfPayment: sql.NullString{String: proofOfPayment, Valid: true},
	}); err != nil {
		t.Fatalf("approve manual transfer: %v", err)
	}
	terminalApproval := approval
	terminalApproval.ApproverID = terminalApproverID
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
	if !completed.ApprovedBy.Valid || completed.ApprovedBy.Int64 != approverID {
		t.Fatalf("completed transfer approved_by = %+v, want %d", completed.ApprovedBy, approverID)
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
	if err := basestore.MigrateScope(ctx, db, tenantID, basestore.MigrationScopeWalletLedger); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	return ctx, New(db), tenantID
}

func insertWalletAdmin(t *testing.T, ctx context.Context, store *Store, tenantID, email string) int64 {
	t.Helper()

	db, err := store.ensureDB()
	if err != nil {
		t.Fatalf("ensure db: %v", err)
	}
	var roleID int64
	roleStmt := db.Rebind(`INSERT INTO admin_roles(tenant_id, role_name, role_level, permissions)
		VALUES(?, ?, 1, '[]') RETURNING id`)
	if err := db.GetContext(ctx, &roleID, roleStmt, tenantID, email+"-role"); err != nil {
		t.Fatalf("insert admin role: %v", err)
	}
	var adminID int64
	adminStmt := db.Rebind(`INSERT INTO admin_users(tenant_id, email, password_hash, role_id)
		VALUES(?, ?, 'hash', ?) RETURNING id`)
	if err := db.GetContext(ctx, &adminID, adminStmt, tenantID, email, roleID); err != nil {
		t.Fatalf("insert admin user: %v", err)
	}
	return adminID
}

func sqlNullInt64(value int64) sql.NullInt64 {
	return sql.NullInt64{Int64: value, Valid: true}
}
