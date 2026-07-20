package store

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type RawJSON json.RawMessage

func (r *RawJSON) Scan(src any) error {
	if src == nil {
		*r = nil
		return nil
	}
	switch value := src.(type) {
	case []byte:
		*r = append((*r)[:0], value...)
	case string:
		*r = append((*r)[:0], value...)
	default:
		return fmt.Errorf("scan raw json: unsupported type %T", src)
	}
	return nil
}

func (r RawJSON) Value() (driver.Value, error) {
	if len(r) == 0 {
		return nil, nil
	}
	if !json.Valid(r) {
		return nil, fmt.Errorf("invalid raw json")
	}
	return []byte(r), nil
}

func (r RawJSON) MarshalJSON() ([]byte, error) {
	if len(r) == 0 {
		return []byte("null"), nil
	}
	if !json.Valid(r) {
		return nil, fmt.Errorf("invalid raw json")
	}
	return []byte(r), nil
}

func (r *RawJSON) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*r = nil
		return nil
	}
	if !json.Valid(data) {
		return fmt.Errorf("invalid raw json")
	}
	*r = append((*r)[:0], data...)
	return nil
}

const (
	PSPStatusInitiated  = "initiated"
	PSPStatusProcessing = "processing"
	PSPStatusPending    = "pending"
	PSPStatusHeld       = "held"
	PSPStatusFailed     = "failed"
	PSPStatusCancelled  = "cancelled"
	PSPStatusSuccess    = "success"
)

func ValidatePSPTransactionStatus(status string) error {
	switch status {
	case "":
		return ErrMissingStatus
	case PSPStatusInitiated, PSPStatusProcessing, PSPStatusPending, PSPStatusHeld, PSPStatusFailed, PSPStatusCancelled, PSPStatusSuccess:
		return nil
	default:
		return ErrInvalidStatus
	}
}

func ValidatePSPStatusTransition(current, next string) error {
	if err := ValidatePSPTransactionStatus(current); err != nil {
		return err
	}
	if err := ValidatePSPTransactionStatus(next); err != nil {
		return err
	}
	if current == next {
		return nil
	}
	allowed := map[string]map[string]struct{}{
		PSPStatusInitiated: {
			PSPStatusProcessing: {}, PSPStatusPending: {}, PSPStatusHeld: {},
			PSPStatusFailed: {}, PSPStatusCancelled: {}, PSPStatusSuccess: {},
		},
		PSPStatusProcessing: {
			PSPStatusPending: {}, PSPStatusHeld: {}, PSPStatusFailed: {}, PSPStatusCancelled: {}, PSPStatusSuccess: {},
		},
		PSPStatusPending: {
			PSPStatusProcessing: {}, PSPStatusHeld: {}, PSPStatusFailed: {}, PSPStatusCancelled: {}, PSPStatusSuccess: {},
		},
		PSPStatusHeld: {
			PSPStatusProcessing: {}, PSPStatusPending: {}, PSPStatusFailed: {}, PSPStatusCancelled: {}, PSPStatusSuccess: {},
		},
	}
	if _, ok := allowed[current][next]; !ok {
		return ErrInvalidStatusTransition
	}
	return nil
}

func PSPTransactionStatusTerminal(status string) bool {
	switch status {
	case PSPStatusSuccess, PSPStatusFailed, PSPStatusCancelled:
		return true
	default:
		return false
	}
}

func ValidateWithdrawalApprovalTarget(txn *PSPTransaction) error {
	if txn == nil || txn.Direction != "outbound" {
		return ErrPSPTransactionNotFound
	}
	switch txn.Status {
	case PSPStatusInitiated, PSPStatusPending, PSPStatusHeld:
	default:
		return ErrInvalidStatusTransition
	}
	var request struct {
		ApprovalRequired *bool `json:"approval_required"`
	}
	if err := json.Unmarshal(txn.RawRequest, &request); err != nil {
		return err
	}
	if request.ApprovalRequired == nil {
		return ErrMissingApprovalPolicy
	}
	if !*request.ApprovalRequired {
		return ErrApprovalNotRequired
	}
	if !txn.DecisionDeadlineAt.Valid || txn.DecisionDeadlineAt.Time.IsZero() {
		return ErrMissingApprovalTimeout
	}
	return nil
}

type PSPTransaction struct {
	ID                        int64          `db:"id"`
	TenantID                  string         `db:"tenant_id"`
	PSPProvider               string         `db:"psp_provider"`
	PSPTransactionID          sql.NullString `db:"psp_transaction_id"`
	IdempotencyKey            string         `db:"idempotency_key"`
	ClientReference           string         `db:"client_reference"`
	Direction                 string         `db:"direction"`
	WalletID                  uuid.NullUUID  `db:"wallet_id"`
	OwnerType                 sql.NullString `db:"owner_type"`
	OwnerID                   sql.NullString `db:"owner_id"`
	WithdrawalDestinationID   sql.NullInt64  `db:"withdrawal_destination_id"`
	AllowReturnToSource       sql.NullBool   `db:"allow_return_to_source"`
	Amount                    int64          `db:"amount"`
	FeeAmount                 sql.NullInt64  `db:"fee_amount"`
	NetAmount                 sql.NullInt64  `db:"net_amount"`
	Currency                  string         `db:"currency"`
	Status                    string         `db:"status"`
	WorkflowID                sql.NullString `db:"workflow_id"`
	ResponseCode              sql.NullString `db:"response_code"`
	ResponseMessage           sql.NullString `db:"response_message"`
	RawRequest                RawJSON        `db:"raw_request"`
	RawResponse               RawJSON        `db:"raw_response"`
	ApprovalTimeoutSeconds    sql.NullInt64  `db:"approval_timeout_seconds"`
	DecisionDeadlineAt        sql.NullTime   `db:"decision_deadline_at"`
	DepositIntentID           sql.NullInt64  `db:"deposit_intent_id"`
	CreatedAt                 time.Time      `db:"created_at"`
	ConfirmedAt               sql.NullTime   `db:"confirmed_at"`
	LastPolledAt              sql.NullTime   `db:"last_polled_at"`
	NextPollAt                sql.NullTime   `db:"next_poll_at"`
	ReconciledAt              sql.NullTime   `db:"reconciled_at"`
	RetryCount                int            `db:"retry_count"`
	LockToken                 sql.NullString `db:"lock_token"`
	LockExpiresAt             sql.NullTime   `db:"lock_expires_at"`
	LastErrorType             sql.NullString `db:"last_error_type"`
	LastErrorAt               sql.NullTime   `db:"last_error_at"`
	WorkflowSignalPayload     RawJSON        `db:"workflow_signal_payload"`
	WorkflowSignalDeliveredAt sql.NullTime   `db:"workflow_signal_delivered_at"`
}

type PSPWorkflowSignal struct {
	ProviderTxID string  `json:"provider_transaction_id,omitempty"`
	Amount       int64   `json:"amount,omitempty"`
	Currency     string  `json:"currency,omitempty"`
	Status       string  `json:"status"`
	RawResponse  RawJSON `json:"raw_response,omitempty"`
}
