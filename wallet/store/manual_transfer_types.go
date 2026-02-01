package store

import (
	"database/sql"
	"time"
)

type ManualTransfer struct {
	ID              int64          `db:"id"`
	TenantID        string         `db:"tenant_id"`
	WorkflowID      string         `db:"workflow_id"`
	IdempotencyKey  string         `db:"idempotency_key"`
	TransferType    string         `db:"transfer_type"`
	WalletID        sql.NullString `db:"wallet_id"`
	Amount          int64          `db:"amount"`
	Currency        string         `db:"currency"`
	Reason          string         `db:"reason"`
	Status          string         `db:"status"`
	RequestedBy     sql.NullInt64  `db:"requested_by"`
	ApprovedBy      sql.NullInt64  `db:"approved_by"`
	ProofOfPayment  sql.NullString `db:"proof_of_payment"`
	PSPProvider     sql.NullString `db:"psp_provider"`
	PSPReference    sql.NullString `db:"psp_reference"`
	RejectionReason sql.NullString `db:"rejection_reason"`
	RequestedAt     time.Time      `db:"requested_at"`
	ApprovedAt      sql.NullTime   `db:"approved_at"`
	CompletedAt     sql.NullTime   `db:"completed_at"`
}

type ManualTransferApproval struct {
	ID               int64          `db:"id"`
	TenantID         string         `db:"tenant_id"`
	ManualTransferID int64          `db:"manual_transfer_id"`
	ApproverID       int64          `db:"approver_id"`
	Decision         string         `db:"decision"`
	Reason           sql.NullString `db:"reason"`
	DecidedAt        time.Time      `db:"decided_at"`
}
