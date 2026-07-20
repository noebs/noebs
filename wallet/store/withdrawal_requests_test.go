package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestBindWithdrawalRequestUsesOnlyPersistedAuthority(t *testing.T) {
	request := WithdrawalRequestSnapshot{
		TenantID:               "tenant",
		ClientReference:        "withdrawal-1",
		ProviderCode:           "bank",
		WalletID:               uuid.New(),
		Amount:                 2500,
		Currency:               "AED",
		OwnerType:              OwnerTypeUser,
		OwnerID:                "42",
		DestinationID:          9,
		ApprovalRequired:       true,
		HoldExpirySeconds:      3600,
		ApprovalTimeoutSeconds: 300,
		Region:                 "AE",
		Metadata:               map[string]any{"order": "42"},
	}
	raw, err := MarshalWithdrawalRequest(request)
	if err != nil {
		t.Fatalf("MarshalWithdrawalRequest() error = %v", err)
	}
	transaction := &PSPTransaction{
		TenantID:                request.TenantID,
		PSPProvider:             request.ProviderCode,
		ClientReference:         request.ClientReference,
		Direction:               "outbound",
		WalletID:                uuid.NullUUID{UUID: request.WalletID, Valid: true},
		OwnerType:               sql.NullString{String: request.OwnerType, Valid: true},
		OwnerID:                 sql.NullString{String: request.OwnerID, Valid: true},
		WithdrawalDestinationID: sql.NullInt64{Int64: request.DestinationID, Valid: true},
		AllowReturnToSource:     sql.NullBool{Bool: request.AllowReturnToSource, Valid: true},
		Amount:                  request.Amount,
		Currency:                request.Currency,
		WorkflowID:              sql.NullString{String: "withdrawal-workflow", Valid: true},
		RawRequest:              raw,
	}

	bound, err := BindWithdrawalRequest(transaction, request.TenantID, request.ClientReference, "withdrawal-workflow")
	if err != nil {
		t.Fatalf("BindWithdrawalRequest() error = %v", err)
	}
	if bound.WalletID != request.WalletID || bound.Amount != request.Amount || bound.DestinationID != request.DestinationID {
		t.Fatalf("bound request = %+v, want persisted request %+v", bound, request)
	}

	tests := []struct {
		name   string
		mutate func(*PSPTransaction)
	}{
		{name: "workflow", mutate: func(txn *PSPTransaction) { txn.WorkflowID.String = "attacker-workflow" }},
		{name: "direction", mutate: func(txn *PSPTransaction) { txn.Direction = "inbound" }},
		{name: "amount", mutate: func(txn *PSPTransaction) { txn.Amount++ }},
		{name: "provider", mutate: func(txn *PSPTransaction) { txn.PSPProvider = "attacker" }},
		{name: "owner", mutate: func(txn *PSPTransaction) { txn.OwnerID.String = "attacker" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := *transaction
			test.mutate(&changed)
			if _, err := BindWithdrawalRequest(&changed, request.TenantID, request.ClientReference, "withdrawal-workflow"); !errors.Is(err, ErrInvalidWithdrawalRequest) {
				t.Fatalf("BindWithdrawalRequest() error = %v, want %v", err, ErrInvalidWithdrawalRequest)
			}
		})
	}

	var forged map[string]any
	if err := json.Unmarshal(raw, &forged); err != nil {
		t.Fatal(err)
	}
	forged["amount"] = float64(request.Amount + 1)
	transaction.RawRequest, err = json.Marshal(forged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BindWithdrawalRequest(transaction, request.TenantID, request.ClientReference, "withdrawal-workflow"); !errors.Is(err, ErrInvalidWithdrawalRequest) {
		t.Fatalf("forged raw request error = %v, want %v", err, ErrInvalidWithdrawalRequest)
	}
}
