package store

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidateWorkflowDecision(t *testing.T) {
	valid := WorkflowDecision{
		TenantID:            "tenant",
		WorkflowID:          "workflow-1",
		Kind:                WorkflowDecisionWithdrawal,
		SubjectID:           41,
		Approved:            true,
		DecidedByOperatorID: 7,
		ProofOfPayment:      sql.NullString{String: "proof-1", Valid: true},
	}
	if err := ValidateWorkflowDecision(valid); err != nil {
		t.Fatalf("valid decision: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*WorkflowDecision)
		want   error
	}{
		{"tenant", func(d *WorkflowDecision) { d.TenantID = "" }, ErrMissingTenantID},
		{"workflow", func(d *WorkflowDecision) { d.WorkflowID = "" }, ErrMissingWorkflowID},
		{"workflow whitespace", func(d *WorkflowDecision) { d.WorkflowID = " workflow-1" }, ErrInvalidWorkflowID},
		{"workflow length", func(d *WorkflowDecision) { d.WorkflowID = string(make([]byte, WorkflowDecisionMaxWorkflowIDLength+1)) }, ErrInvalidWorkflowID},
		{"kind", func(d *WorkflowDecision) { d.Kind = "" }, ErrMissingDecisionKind},
		{"invalid kind", func(d *WorkflowDecision) { d.Kind = "refund" }, ErrInvalidDecisionKind},
		{"subject", func(d *WorkflowDecision) { d.SubjectID = 0 }, ErrMissingDecisionSubject},
		{"operator", func(d *WorkflowDecision) { d.DecidedByOperatorID = 0 }, ErrMissingApproverID},
		{"approval proof", func(d *WorkflowDecision) { d.ProofOfPayment = sql.NullString{} }, ErrMissingProofOfPayment},
		{"approval proof whitespace", func(d *WorkflowDecision) { d.ProofOfPayment = sql.NullString{String: " proof-1 ", Valid: true} }, ErrMissingProofOfPayment},
		{"approval reason whitespace", func(d *WorkflowDecision) { d.Reason = sql.NullString{String: " reason ", Valid: true} }, ErrMissingReason},
		{"rejection reason", func(d *WorkflowDecision) {
			d.Approved = false
			d.ProofOfPayment = sql.NullString{}
		}, ErrMissingApprovalReason},
		{"rejection proof", func(d *WorkflowDecision) {
			d.Approved = false
			d.Reason = sql.NullString{String: "rejected", Valid: true}
		}, ErrInvalidDecision},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := valid
			tt.mutate(&decision)
			if err := ValidateWorkflowDecision(decision); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestWorkflowDecisionSubjectLockKeyIsCanonical(t *testing.T) {
	base := WorkflowDecisionKey{TenantID: "tenant", Kind: WorkflowDecisionWithdrawal, SubjectID: 41}
	if got := workflowDecisionSubjectLockKey(base); got != workflowDecisionSubjectLockKey(base) {
		t.Fatalf("lock key is not deterministic: %d", got)
	}
	for _, distinct := range []WorkflowDecisionKey{
		{TenantID: "tenant-2", Kind: base.Kind, SubjectID: base.SubjectID},
		{TenantID: base.TenantID, Kind: WorkflowDecisionManualTransfer, SubjectID: base.SubjectID},
		{TenantID: base.TenantID, Kind: base.Kind, SubjectID: base.SubjectID + 1},
	} {
		if workflowDecisionSubjectLockKey(distinct) == workflowDecisionSubjectLockKey(base) {
			t.Fatalf("distinct subject shares lock key: %+v", distinct)
		}
	}
}

func TestWorkflowDecisionReservationIsImmutableAndCrashDurable(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	operatorID := insertWalletOperator(t, ctx, walletStore, "decision-operator")
	transaction := createApprovalPSPTransaction(t, ctx, walletStore, tenantID, "workflow-decision-replay")
	decision := WorkflowDecision{
		TenantID:            tenantID,
		WorkflowID:          "workflow-decision-replay",
		Kind:                WorkflowDecisionWithdrawal,
		SubjectID:           transaction.ID,
		Approved:            true,
		DecidedByOperatorID: operatorID,
		Reason:              sql.NullString{String: "reviewed", Valid: true},
		ProofOfPayment:      sql.NullString{String: "proof-19", Valid: true},
	}

	const callers = 12
	results := make(chan *WorkflowDecision, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stored, err := walletStore.ReserveWorkflowDecision(ctx, decision)
			results <- stored
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("identical concurrent replay: %v", err)
		}
	}
	var decidedAt string
	for stored := range results {
		if stored == nil {
			t.Fatal("identical concurrent replay returned nil")
		}
		if decidedAt == "" {
			decidedAt = stored.DecidedAt.String()
		} else if stored.DecidedAt.String() != decidedAt {
			t.Fatalf("replay timestamp = %s, want %s", stored.DecidedAt, decidedAt)
		}
	}

	restarted := New(walletStore.DB)
	lookup, err := restarted.LookupWorkflowDecision(ctx, WorkflowDecisionKey{
		TenantID: tenantID, WorkflowID: decision.WorkflowID, Kind: decision.Kind, SubjectID: decision.SubjectID,
	})
	if err != nil {
		t.Fatalf("lookup after store restart: %v", err)
	}
	if !lookup.Found || !workflowDecisionEqual(lookup.Decision, decision) {
		t.Fatalf("lookup after store restart = %+v", lookup)
	}
	if err := restarted.UpdatePSPTransactionStatus(ctx, tenantID, transaction.ClientReference, PSPStatusUpdate{Status: PSPStatusSuccess}); err != nil {
		t.Fatalf("complete withdrawal target: %v", err)
	}
	terminalReplay, err := restarted.ReserveWorkflowDecision(ctx, decision)
	if err != nil {
		t.Fatalf("exact replay after terminal withdrawal: %v", err)
	}
	if !workflowDecisionEqual(*terminalReplay, decision) {
		t.Fatalf("terminal replay = %+v, want %+v", terminalReplay, decision)
	}

	opposite := decision
	opposite.Approved = false
	opposite.Reason = sql.NullString{String: "rejected", Valid: true}
	opposite.ProofOfPayment = sql.NullString{}
	if _, err := restarted.ReserveWorkflowDecision(ctx, opposite); !errors.Is(err, ErrWorkflowDecisionConflict) {
		t.Fatalf("opposite decision error = %v, want %v", err, ErrWorkflowDecisionConflict)
	}
}

func TestWorkflowDecisionReservationWaitsForSubjectAdvisoryLock(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	operatorID := insertWalletOperator(t, ctx, walletStore, "advisory-lock-operator")
	transaction := createApprovalPSPTransaction(t, ctx, walletStore, tenantID, "advisory-lock-workflow")
	decision := WorkflowDecision{
		TenantID: tenantID, WorkflowID: transaction.WorkflowID.String, Kind: WorkflowDecisionWithdrawal,
		SubjectID: transaction.ID, Approved: true, DecidedByOperatorID: operatorID,
		ProofOfPayment: sql.NullString{String: "proof-advisory", Valid: true},
	}

	blocker, err := walletStore.DB.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := WorkflowDecisionKey{
		TenantID: tenantID, WorkflowID: decision.WorkflowID, Kind: decision.Kind, SubjectID: decision.SubjectID,
	}
	if err := lockWorkflowDecisionSubjectTx(ctx, blocker, key); err != nil {
		_ = blocker.Rollback()
		t.Fatal(err)
	}
	type result struct {
		decision *WorkflowDecision
		err      error
	}
	completed := make(chan result, 1)
	go func() {
		stored, reserveErr := walletStore.ReserveWorkflowDecision(ctx, decision)
		completed <- result{decision: stored, err: reserveErr}
	}()

	select {
	case got := <-completed:
		_ = blocker.Rollback()
		t.Fatalf("reservation bypassed subject lock: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-completed:
		if got.err != nil || got.decision == nil {
			t.Fatalf("reservation after lock release = %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reservation remained blocked after advisory lock release")
	}
}

func TestManualWorkflowDecisionExactReplaySurvivesTerminalTarget(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	requesterID := insertWalletOperator(t, ctx, walletStore, "manual-decision-requester")
	operatorID := insertWalletOperator(t, ctx, walletStore, "manual-decision-operator")
	wallet, err := walletStore.EnsureWallet(ctx, EnsureWalletParams{
		TenantID: tenantID, OwnerType: OwnerTypeUser, OwnerID: "manual-decision-user",
		UserID: 51, Currency: "USD", KYCTier: KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure wallet: %v", err)
	}
	transfer, err := walletStore.CreateManualTransfer(ctx, ManualTransfer{
		TenantID: tenantID, WorkflowID: "manual-decision-terminal", IdempotencyKey: "manual-decision-terminal",
		TransferType: ManualTransferTypeDebit, WalletID: sql.NullString{String: wallet.ID.String(), Valid: true},
		Amount: 100, Currency: "USD", Reason: "operator correction", Status: ManualTransferStatusPending,
		RequestedByOperatorID: requesterID, ApprovalTimeoutSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	decision := WorkflowDecision{
		TenantID: tenantID, WorkflowID: transfer.WorkflowID, Kind: WorkflowDecisionManualTransfer,
		SubjectID: transfer.ID, DecidedByOperatorID: operatorID,
		Reason: sql.NullString{String: "rejected by reviewer", Valid: true},
	}
	stored, err := walletStore.ReserveWorkflowDecision(ctx, decision)
	if err != nil {
		t.Fatalf("reserve decision: %v", err)
	}
	if err := walletStore.UpdateManualTransferStatus(ctx, tenantID, transfer.WorkflowID, ManualTransferStatusUpdate{
		Status: ManualTransferStatusRejected, RejectionReason: decision.Reason,
	}); err != nil {
		t.Fatalf("reject transfer: %v", err)
	}
	replayed, err := walletStore.ReserveWorkflowDecision(ctx, decision)
	if err != nil {
		t.Fatalf("exact replay after terminal manual transfer: %v", err)
	}
	if replayed.DecidedAt != stored.DecidedAt || !workflowDecisionEqual(*replayed, decision) {
		t.Fatalf("terminal replay = %+v, want original %+v", replayed, stored)
	}
	opposite := decision
	opposite.Approved = true
	opposite.Reason = sql.NullString{}
	opposite.ProofOfPayment = sql.NullString{String: "proof-55", Valid: true}
	if _, err := walletStore.ReserveWorkflowDecision(ctx, opposite); !errors.Is(err, ErrWorkflowDecisionConflict) {
		t.Fatalf("opposite terminal replay error = %v, want %v", err, ErrWorkflowDecisionConflict)
	}
}

func TestWorkflowDecisionReservationRejectsPersistedExpiredWindows(t *testing.T) {
	t.Run("manual transfer", func(t *testing.T) {
		ctx, walletStore, tenantID := newWalletStoreIntegration(t)
		requesterID := insertWalletOperator(t, ctx, walletStore, "expired-manual-requester")
		operatorID := insertWalletOperator(t, ctx, walletStore, "expired-manual-operator")
		wallet, err := walletStore.EnsureWallet(ctx, EnsureWalletParams{
			TenantID: tenantID, OwnerType: OwnerTypeUser, OwnerID: "expired-manual-user",
			UserID: 61, Currency: "USD", KYCTier: KYCTierUnverified,
		})
		if err != nil {
			t.Fatalf("ensure wallet: %v", err)
		}
		transfer, err := walletStore.CreateManualTransfer(ctx, ManualTransfer{
			TenantID: tenantID, WorkflowID: "expired-manual", IdempotencyKey: "expired-manual",
			TransferType: ManualTransferTypeDebit, WalletID: sql.NullString{String: wallet.ID.String(), Valid: true},
			Amount: 100, Currency: "USD", Reason: "review required", Status: ManualTransferStatusPending,
			RequestedByOperatorID: requesterID, ApprovalTimeoutSeconds: 3600,
		})
		if err != nil {
			t.Fatalf("create transfer: %v", err)
		}
		if _, err := walletStore.DB.ExecContext(ctx, `UPDATE manual_transfers
			SET decision_deadline_at = clock_timestamp() - interval '1 second'
			WHERE tenant_id = $1 AND id = $2`, tenantID, transfer.ID); err != nil {
			t.Fatalf("expire manual decision window: %v", err)
		}
		_, err = walletStore.ReserveWorkflowDecision(ctx, WorkflowDecision{
			TenantID: tenantID, WorkflowID: transfer.WorkflowID, Kind: WorkflowDecisionManualTransfer,
			SubjectID: transfer.ID, Approved: true, DecidedByOperatorID: operatorID,
			ProofOfPayment: sql.NullString{String: "late-proof", Valid: true},
		})
		if !errors.Is(err, ErrWorkflowDecisionWindowClosed) {
			t.Fatalf("late manual decision error = %v, want %v", err, ErrWorkflowDecisionWindowClosed)
		}
	})

	t.Run("withdrawal", func(t *testing.T) {
		ctx, walletStore, tenantID := newWalletStoreIntegration(t)
		operatorID := insertWalletOperator(t, ctx, walletStore, "expired-withdrawal-operator")
		transaction := createApprovalPSPTransaction(t, ctx, walletStore, tenantID, "expired-withdrawal")
		if _, err := walletStore.DB.ExecContext(ctx, `UPDATE psp_transactions
			SET decision_deadline_at = clock_timestamp() - interval '1 second'
			WHERE tenant_id = $1 AND id = $2`, tenantID, transaction.ID); err != nil {
			t.Fatalf("expire withdrawal decision window: %v", err)
		}
		_, err := walletStore.ReserveWorkflowDecision(ctx, WorkflowDecision{
			TenantID: tenantID, WorkflowID: transaction.WorkflowID.String, Kind: WorkflowDecisionWithdrawal,
			SubjectID: transaction.ID, Approved: true, DecidedByOperatorID: operatorID,
			ProofOfPayment: sql.NullString{String: "late-proof", Valid: true},
		})
		if !errors.Is(err, ErrWorkflowDecisionWindowClosed) {
			t.Fatalf("late withdrawal decision error = %v, want %v", err, ErrWorkflowDecisionWindowClosed)
		}
	})
}

func TestConcurrentOppositeWorkflowDecisionsChooseOneImmutableWinner(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	approveOperator := insertWalletOperator(t, ctx, walletStore, "approve-operator")
	rejectOperator := insertWalletOperator(t, ctx, walletStore, "reject-operator")
	transaction := createApprovalPSPTransaction(t, ctx, walletStore, tenantID, "workflow-decision-race")
	approved := WorkflowDecision{
		TenantID: tenantID, WorkflowID: "workflow-decision-race", Kind: WorkflowDecisionWithdrawal,
		SubjectID: transaction.ID, Approved: true, DecidedByOperatorID: approveOperator,
		ProofOfPayment: sql.NullString{String: "proof-23", Valid: true},
	}
	rejected := WorkflowDecision{
		TenantID: tenantID, WorkflowID: "workflow-decision-race", Kind: WorkflowDecisionWithdrawal,
		SubjectID: transaction.ID, DecidedByOperatorID: rejectOperator,
		Reason: sql.NullString{String: "rejected", Valid: true},
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, decision := range []WorkflowDecision{approved, rejected} {
		go func(decision WorkflowDecision) {
			<-start
			_, err := walletStore.ReserveWorkflowDecision(ctx, decision)
			errs <- err
		}(decision)
	}
	close(start)
	var succeeded, conflicted int
	for range 2 {
		switch err := <-errs; {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrWorkflowDecisionConflict):
			conflicted++
		default:
			t.Fatalf("race error = %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("race results: succeeded=%d conflicted=%d", succeeded, conflicted)
	}
}

func TestWorkflowDecisionReservationRejectsClosedApprovalWindow(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	operatorID := insertWalletOperator(t, ctx, walletStore, "late-operator")
	transaction := createApprovalPSPTransaction(t, ctx, walletStore, tenantID, "workflow-decision-timeout")
	key := WorkflowDecisionKey{
		TenantID: tenantID, WorkflowID: transaction.WorkflowID.String,
		Kind: WorkflowDecisionWithdrawal, SubjectID: transaction.ID,
	}
	if _, err := walletStore.CloseWorkflowDecisionWindow(ctx, WorkflowDecisionWindowClose{
		Key: key, Reason: "withdrawal approval timed out",
	}); !errors.Is(err, ErrWorkflowDecisionWindowOpen) {
		t.Fatalf("early close error = %v, want %v", err, ErrWorkflowDecisionWindowOpen)
	}
	if _, err := walletStore.DB.ExecContext(ctx, `UPDATE psp_transactions
		SET decision_deadline_at = clock_timestamp() - interval '1 second'
		WHERE tenant_id = $1 AND id = $2`, tenantID, transaction.ID); err != nil {
		t.Fatalf("expire approval deadline: %v", err)
	}
	var before time.Time
	if err := walletStore.DB.GetContext(ctx, &before, "SELECT clock_timestamp()"); err != nil {
		t.Fatalf("read DB clock before close: %v", err)
	}
	closed, err := walletStore.CloseWorkflowDecisionWindow(ctx, WorkflowDecisionWindowClose{
		Key: key, Reason: "withdrawal approval timed out",
	})
	if err != nil {
		t.Fatalf("close decision window: %v", err)
	}
	if closed.Found {
		t.Fatal("closed empty decision window returned a decision")
	}
	var (
		lastErrorAt sql.NullTime
		after       time.Time
	)
	if err := walletStore.DB.GetContext(ctx, &lastErrorAt, `SELECT last_error_at FROM psp_transactions
		WHERE tenant_id = $1 AND id = $2`, tenantID, transaction.ID); err != nil {
		t.Fatalf("read closure timestamp: %v", err)
	}
	if err := walletStore.DB.GetContext(ctx, &after, "SELECT clock_timestamp()"); err != nil {
		t.Fatalf("read DB clock after close: %v", err)
	}
	if !lastErrorAt.Valid || lastErrorAt.Time.Before(before) || lastErrorAt.Time.After(after) {
		t.Fatalf("closure timestamp = %+v, want DB time in [%s, %s]", lastErrorAt, before, after)
	}
	_, err = walletStore.ReserveWorkflowDecision(ctx, WorkflowDecision{
		TenantID: tenantID, WorkflowID: key.WorkflowID, Kind: key.Kind, SubjectID: key.SubjectID,
		Approved: true, DecidedByOperatorID: operatorID,
		ProofOfPayment: sql.NullString{String: "late-proof", Valid: true},
	})
	if !errors.Is(err, ErrInvalidStatusTransition) {
		t.Fatalf("late decision error = %v, want %v", err, ErrInvalidStatusTransition)
	}
	current, err := walletStore.GetPSPTransactionByWorkflow(ctx, tenantID, key.WorkflowID)
	if err != nil {
		t.Fatalf("get closed transaction: %v", err)
	}
	if current.Status != PSPStatusCancelled {
		t.Fatalf("closed transaction status = %q, want %q", current.Status, PSPStatusCancelled)
	}
}

func createApprovalPSPTransaction(t *testing.T, ctx context.Context, walletStore *Store, tenantID, workflowID string) *PSPTransaction {
	t.Helper()
	if _, err := walletStore.DB.ExecContext(ctx, `INSERT INTO psp_configs(
		tenant_id, provider_code, provider_name, api_base_url, idempotency_header_name, deposit_response_mapping
	) VALUES($1, 'test', 'Test PSP', 'https://psp.invalid', 'Idempotency-Key', '{}')
	ON CONFLICT (tenant_id, provider_code) DO NOTHING`, tenantID); err != nil {
		t.Fatalf("seed approval PSP config: %v", err)
	}
	wallet, err := walletStore.EnsureWallet(ctx, EnsureWalletParams{
		TenantID: tenantID, OwnerType: OwnerTypeSystem, OwnerID: workflowID,
		Currency: "AED", KYCTier: KYCTierUnverified,
	})
	if err != nil {
		t.Fatalf("ensure approval wallet: %v", err)
	}
	transaction, err := walletStore.CreatePSPTransaction(ctx, PSPTransaction{
		TenantID: tenantID, PSPProvider: "test", IdempotencyKey: workflowID,
		ClientReference: workflowID, Direction: "outbound", Amount: 100, Currency: "AED",
		WalletID:            uuid.NullUUID{UUID: wallet.ID, Valid: true},
		OwnerType:           sql.NullString{String: wallet.OwnerType, Valid: true},
		OwnerID:             sql.NullString{String: wallet.OwnerID, Valid: true},
		AllowReturnToSource: sql.NullBool{Bool: true, Valid: true},
		Status:              PSPStatusInitiated, WorkflowID: sql.NullString{String: workflowID, Valid: true},
		RawRequest:             RawJSON(`{"approval_required":true,"approval_timeout_seconds":3600,"hold_expiry_seconds":3600}`),
		ApprovalTimeoutSeconds: sql.NullInt64{Int64: 3600, Valid: true},
	})
	if err != nil {
		t.Fatalf("create approval transaction: %v", err)
	}
	return transaction
}
