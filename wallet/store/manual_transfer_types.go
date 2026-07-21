package store

import (
	"database/sql"
	"time"
)

const (
	ManualTransferTypeCredit     = "manual_credit"
	ManualTransferTypeDebit      = "manual_debit"
	ManualTransferTypeWithdrawal = "manual_withdrawal"
)

const (
	ManualTransferStatusPending   = "pending"
	ManualTransferStatusApproved  = "approved"
	ManualTransferStatusRejected  = "rejected"
	ManualTransferStatusCompleted = "completed"
)

func ValidateManualTransferType(transferType string) error {
	switch transferType {
	case "":
		return ErrMissingTransferType
	case ManualTransferTypeCredit, ManualTransferTypeDebit, ManualTransferTypeWithdrawal:
		return nil
	default:
		return ErrInvalidTransferType
	}
}

func ValidateManualTransferStatus(status string) error {
	switch status {
	case "":
		return ErrMissingStatus
	case ManualTransferStatusPending, ManualTransferStatusApproved, ManualTransferStatusRejected, ManualTransferStatusCompleted:
		return nil
	default:
		return ErrInvalidStatus
	}
}

func ValidateManualTransferDecision(decision string) error {
	switch decision {
	case "":
		return ErrMissingDecision
	case ManualTransferStatusApproved, ManualTransferStatusRejected:
		return nil
	default:
		return ErrInvalidDecision
	}
}

func IsManualTransferDebit(transferType string) bool {
	switch transferType {
	case ManualTransferTypeDebit, ManualTransferTypeWithdrawal:
		return true
	default:
		return false
	}
}

type ManualTransfer struct {
	ID                     int64          `db:"id"`
	TenantID               string         `db:"tenant_id"`
	WorkflowID             string         `db:"workflow_id"`
	IdempotencyKey         string         `db:"idempotency_key"`
	TransferType           string         `db:"transfer_type"`
	WalletID               sql.NullString `db:"wallet_id"`
	Amount                 int64          `db:"amount"`
	Currency               string         `db:"currency"`
	CurrencyUnitID         int64          `db:"currency_unit_version_id"`
	Reason                 string         `db:"reason"`
	Status                 string         `db:"status"`
	RequestedByOperatorID  int64          `db:"requested_by_operator_id"`
	ApprovedByOperatorID   sql.NullInt64  `db:"approved_by_operator_id"`
	ProofOfPayment         sql.NullString `db:"proof_of_payment"`
	PSPProvider            sql.NullString `db:"psp_provider"`
	PSPReference           sql.NullString `db:"psp_reference"`
	RejectionReason        sql.NullString `db:"rejection_reason"`
	ApprovalTimeoutSeconds int            `db:"approval_timeout_seconds"`
	DecisionDeadlineAt     time.Time      `db:"decision_deadline_at"`
	RequestedAt            time.Time      `db:"requested_at"`
	ApprovedAt             sql.NullTime   `db:"approved_at"`
	CompletedAt            sql.NullTime   `db:"completed_at"`
}

type ManualTransferApproval struct {
	ID                  int64          `db:"id"`
	TenantID            string         `db:"tenant_id"`
	ManualTransferID    int64          `db:"manual_transfer_id"`
	DecidedByOperatorID int64          `db:"decided_by_operator_id"`
	Decision            string         `db:"decision"`
	Reason              sql.NullString `db:"reason"`
	DecidedAt           time.Time      `db:"decided_at"`
}

type ManualTransferFilter struct {
	TenantID            string
	Status              string
	TransferType        string
	WalletID            string
	RequesterOperatorID int64
	Start               time.Time
	End                 time.Time
	Limit               int
	Offset              int
}

type ManualTransferStatusUpdate struct {
	Status               string
	ApprovedByOperatorID sql.NullInt64
	ApprovedAt           sql.NullTime
	CompletedAt          sql.NullTime
	ProofOfPayment       sql.NullString
	RejectionReason      sql.NullString
}
