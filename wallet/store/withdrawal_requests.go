package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"strings"

	"github.com/google/uuid"
)

// WithdrawalRequestSnapshot is the immutable, server-bound withdrawal command
// persisted before a workflow can start.
type WithdrawalRequestSnapshot struct {
	TenantID               string         `json:"tenant_id"`
	ClientReference        string         `json:"client_reference"`
	ProviderCode           string         `json:"provider_code"`
	WalletID               uuid.UUID      `json:"wallet_id"`
	Amount                 int64          `json:"amount"`
	Currency               string         `json:"currency"`
	OwnerType              string         `json:"owner_type"`
	OwnerID                string         `json:"owner_id"`
	DestinationID          int64          `json:"destination_id"`
	AllowReturnToSource    bool           `json:"allow_return_to_source"`
	ApprovalRequired       bool           `json:"approval_required"`
	HoldExpirySeconds      int            `json:"hold_expiry_seconds"`
	ApprovalTimeoutSeconds int            `json:"approval_timeout_seconds"`
	Region                 string         `json:"region"`
	Metadata               map[string]any `json:"metadata"`
}

func MarshalWithdrawalRequest(request WithdrawalRequestSnapshot) (RawJSON, error) {
	if err := validateWithdrawalRequestSnapshot(request); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, ErrInvalidWithdrawalRequest
	}
	return RawJSON(encoded), nil
}

func BindWithdrawalRequest(
	transaction *PSPTransaction,
	tenantID string,
	clientReference string,
	workflowID string,
) (WithdrawalRequestSnapshot, error) {
	if transaction == nil ||
		transaction.TenantID != tenantID ||
		transaction.ClientReference != clientReference ||
		transaction.Direction != "outbound" ||
		!transaction.WorkflowID.Valid || transaction.WorkflowID.String != workflowID ||
		len(transaction.RawRequest) == 0 {
		return WithdrawalRequestSnapshot{}, ErrInvalidWithdrawalRequest
	}

	decoder := json.NewDecoder(bytes.NewReader(transaction.RawRequest))
	decoder.DisallowUnknownFields()
	var request WithdrawalRequestSnapshot
	if err := decoder.Decode(&request); err != nil {
		return WithdrawalRequestSnapshot{}, ErrInvalidWithdrawalRequest
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return WithdrawalRequestSnapshot{}, ErrInvalidWithdrawalRequest
	}
	if err := validateWithdrawalRequestSnapshot(request); err != nil {
		return WithdrawalRequestSnapshot{}, err
	}
	if request.TenantID != tenantID ||
		request.ClientReference != clientReference ||
		request.ProviderCode != transaction.PSPProvider ||
		!transaction.WalletID.Valid || request.WalletID != transaction.WalletID.UUID ||
		!transaction.OwnerType.Valid || request.OwnerType != transaction.OwnerType.String ||
		!transaction.OwnerID.Valid || request.OwnerID != transaction.OwnerID.String ||
		!transaction.AllowReturnToSource.Valid || request.AllowReturnToSource != transaction.AllowReturnToSource.Bool ||
		!withdrawalDestinationMatches(transaction.WithdrawalDestinationID, request.DestinationID) ||
		request.Amount != transaction.Amount ||
		request.Currency != transaction.Currency {
		return WithdrawalRequestSnapshot{}, ErrInvalidWithdrawalRequest
	}
	return request, nil
}

func withdrawalDestinationMatches(stored sql.NullInt64, requested int64) bool {
	if requested == 0 {
		return !stored.Valid
	}
	return stored.Valid && stored.Int64 == requested
}

func validateWithdrawalRequestSnapshot(request WithdrawalRequestSnapshot) error {
	if _, err := ValidateTenantID(request.TenantID); err != nil {
		return ErrInvalidWithdrawalRequest
	}
	if request.ClientReference == "" || strings.TrimSpace(request.ClientReference) != request.ClientReference ||
		request.ProviderCode == "" || strings.TrimSpace(request.ProviderCode) != request.ProviderCode ||
		request.WalletID == uuid.Nil || request.Amount <= 0 ||
		request.Currency == "" || strings.TrimSpace(request.Currency) != request.Currency ||
		!OwnerTypeValid(request.OwnerType) || request.OwnerID == "" || strings.TrimSpace(request.OwnerID) != request.OwnerID ||
		request.DestinationID < 0 || (!request.AllowReturnToSource && request.DestinationID == 0) ||
		request.HoldExpirySeconds <= 0 ||
		(request.ApprovalRequired && request.ApprovalTimeoutSeconds <= 0) ||
		(!request.ApprovalRequired && request.ApprovalTimeoutSeconds != 0) ||
		strings.TrimSpace(request.Region) != request.Region || len(request.Region) > 128 ||
		request.Metadata == nil {
		return ErrInvalidWithdrawalRequest
	}
	return nil
}
