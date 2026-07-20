package store

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestValidateWithdrawalApprovalTarget(t *testing.T) {
	valid := PSPTransaction{
		Direction: "outbound", Status: PSPStatusInitiated,
		RawRequest:         RawJSON(`{"approval_required":true}`),
		DecisionDeadlineAt: sql.NullTime{Time: time.Now().UTC().Add(time.Hour), Valid: true},
	}
	if err := ValidateWithdrawalApprovalTarget(&valid); err != nil {
		t.Fatalf("valid approval target: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*PSPTransaction)
		want   error
	}{
		{"direction", func(txn *PSPTransaction) { txn.Direction = "inbound" }, ErrPSPTransactionNotFound},
		{"terminal", func(txn *PSPTransaction) { txn.Status = PSPStatusSuccess }, ErrInvalidStatusTransition},
		{"missing policy", func(txn *PSPTransaction) { txn.RawRequest = RawJSON(`{}`) }, ErrMissingApprovalPolicy},
		{"approval disabled", func(txn *PSPTransaction) { txn.RawRequest = RawJSON(`{"approval_required":false}`) }, ErrApprovalNotRequired},
		{"deadline", func(txn *PSPTransaction) { txn.DecisionDeadlineAt = sql.NullTime{} }, ErrMissingApprovalTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txn := valid
			tt.mutate(&txn)
			if err := ValidateWithdrawalApprovalTarget(&txn); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestPSPApprovalWindowUsesPersistedDurationInsteadOfCallerDeadline(t *testing.T) {
	valid := PSPTransaction{
		Direction:              "outbound",
		RawRequest:             RawJSON(`{"approval_required":true,"approval_timeout_seconds":120,"hold_expiry_seconds":60}`),
		ApprovalTimeoutSeconds: sql.NullInt64{Int64: 60, Valid: true},
	}
	if err := validatePSPDecisionDeadline(valid); err != nil {
		t.Fatalf("valid persisted approval window: %v", err)
	}
	wrongDuration := valid
	wrongDuration.ApprovalTimeoutSeconds.Int64 = 120
	if err := validatePSPDecisionDeadline(wrongDuration); !errors.Is(err, ErrInvalidApprovalTimeout) {
		t.Fatalf("wrong effective duration error = %v, want %v", err, ErrInvalidApprovalTimeout)
	}
	callerDeadline := valid
	callerDeadline.DecisionDeadlineAt = sql.NullTime{Time: time.Now().UTC().Add(time.Hour), Valid: true}
	if err := validatePSPDecisionDeadline(callerDeadline); !errors.Is(err, ErrInvalidApprovalTimeout) {
		t.Fatalf("caller deadline error = %v, want %v", err, ErrInvalidApprovalTimeout)
	}
}
