package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

type PSPTransaction struct {
	ID               int64           `db:"id"`
	TenantID         string          `db:"tenant_id"`
	PSPProvider      string          `db:"psp_provider"`
	PSPTransactionID sql.NullString  `db:"psp_transaction_id"`
	IdempotencyKey   string          `db:"idempotency_key"`
	ClientReference  string          `db:"client_reference"`
	Direction        string          `db:"direction"`
	Amount           int64           `db:"amount"`
	FeeAmount        sql.NullInt64   `db:"fee_amount"`
	NetAmount        sql.NullInt64   `db:"net_amount"`
	Currency         string          `db:"currency"`
	Status           string          `db:"status"`
	WorkflowID       sql.NullString  `db:"workflow_id"`
	ResponseCode     sql.NullString  `db:"response_code"`
	ResponseMessage  sql.NullString  `db:"response_message"`
	RawRequest       json.RawMessage `db:"raw_request"`
	RawResponse      json.RawMessage `db:"raw_response"`
	CreatedAt        time.Time       `db:"created_at"`
	ConfirmedAt      sql.NullTime    `db:"confirmed_at"`
	LastPolledAt     sql.NullTime    `db:"last_polled_at"`
	NextPollAt       sql.NullTime    `db:"next_poll_at"`
	ReconciledAt     sql.NullTime    `db:"reconciled_at"`
	RetryCount       int             `db:"retry_count"`
	LockToken        sql.NullString  `db:"lock_token"`
	LockExpiresAt    sql.NullTime    `db:"lock_expires_at"`
	LastErrorType    sql.NullString  `db:"last_error_type"`
	LastErrorAt      sql.NullTime    `db:"last_error_at"`
}
