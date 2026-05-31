package store

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
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
	if PSPTransactionStatusTerminal(current) && current != next {
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

type PSPTransaction struct {
	ID               int64          `db:"id"`
	TenantID         string         `db:"tenant_id"`
	PSPProvider      string         `db:"psp_provider"`
	PSPTransactionID sql.NullString `db:"psp_transaction_id"`
	IdempotencyKey   string         `db:"idempotency_key"`
	ClientReference  string         `db:"client_reference"`
	Direction        string         `db:"direction"`
	Amount           int64          `db:"amount"`
	FeeAmount        sql.NullInt64  `db:"fee_amount"`
	NetAmount        sql.NullInt64  `db:"net_amount"`
	Currency         string         `db:"currency"`
	Status           string         `db:"status"`
	WorkflowID       sql.NullString `db:"workflow_id"`
	ResponseCode     sql.NullString `db:"response_code"`
	ResponseMessage  sql.NullString `db:"response_message"`
	RawRequest       RawJSON        `db:"raw_request"`
	RawResponse      RawJSON        `db:"raw_response"`
	CreatedAt        time.Time      `db:"created_at"`
	ConfirmedAt      sql.NullTime   `db:"confirmed_at"`
	LastPolledAt     sql.NullTime   `db:"last_polled_at"`
	NextPollAt       sql.NullTime   `db:"next_poll_at"`
	ReconciledAt     sql.NullTime   `db:"reconciled_at"`
	RetryCount       int            `db:"retry_count"`
	LockToken        sql.NullString `db:"lock_token"`
	LockExpiresAt    sql.NullTime   `db:"lock_expires_at"`
	LastErrorType    sql.NullString `db:"last_error_type"`
	LastErrorAt      sql.NullTime   `db:"last_error_at"`
}
