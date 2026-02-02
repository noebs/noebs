package workflow

import (
	"testing"

	walletstore "github.com/adonese/noebs/wallet/store"
)

func TestValidateManualTransferDecision(t *testing.T) {
	decision := ManualTransferDecision{ApproverID: 10}
	if err := validateManualTransferDecision(10, decision); err != walletstore.ErrApproverIsRequester {
		t.Fatalf("expected maker-checker error, got %v", err)
	}
	if err := validateManualTransferDecision(11, decision); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if err := validateManualTransferDecision(0, decision); err != nil {
		t.Fatalf("expected no error when requester missing, got %v", err)
	}
}
