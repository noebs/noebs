package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	FulfillmentAutomated = "automated"
	FulfillmentManual    = "manual"
	FulfillmentOffline   = "offline"
)

var (
	ErrInvalidClientReference     = errors.New("invalid client reference")
	ErrMissingStatusVersion       = errors.New("missing transaction status version")
	ErrStatusVersionConflict      = errors.New("transaction status changed; reload before resolving")
	ErrInvalidFulfillmentMethod   = errors.New("invalid fulfillment method")
	ErrMissingEvidenceReference   = errors.New("missing evidence reference")
	ErrMissingSettlementReference = errors.New("missing settlement reference")
	ErrManualResolutionConflict   = errors.New("manual resolution conflicts with an existing command")
	ErrManualResolutionNotAllowed = errors.New("only dispatched, unresolved transactions can be resolved manually")
)

type PSPManualResolution struct {
	TenantID            string `db:"tenant_id"`
	ClientReference     string `db:"client_reference"`
	IdempotencyKey      string `db:"idempotency_key"`
	OperatorID          int64  `db:"operator_id"`
	ExpectedVersion     int64  `db:"expected_version"`
	Status              string `db:"status"`
	FulfillmentMethod   string `db:"fulfillment_method"`
	Reason              string `db:"reason"`
	EvidenceReference   string `db:"evidence_reference"`
	SettlementReference string `db:"settlement_reference"`
}

type TransactionStatusEvent struct {
	ID                int64          `db:"id"`
	EventID           string         `db:"event_id"`
	TenantID          string         `db:"tenant_id"`
	AggregateID       string         `db:"aggregate_id"`
	Version           int64          `db:"version"`
	Status            string         `db:"status"`
	Substatus         string         `db:"substatus"`
	UserID            sql.NullInt64  `db:"user_id"`
	OperatorID        sql.NullInt64  `db:"operator_id"`
	Reason            sql.NullString `db:"reason"`
	EvidenceReference sql.NullString `db:"evidence_reference"`
	CreatedAt         time.Time      `db:"created_at"`
}

func ValidatePSPManualResolution(command PSPManualResolution) error {
	if _, err := ValidateTenantID(command.TenantID); err != nil {
		return err
	}
	if err := validateBoundedIdentifier(command.ClientReference, 255, ErrMissingClientReference, ErrInvalidClientReference); err != nil {
		return err
	}
	if err := validateBoundedIdentifier(command.IdempotencyKey, 255, ErrMissingIdempotencyKey, ErrInvalidIdempotencyKey); err != nil {
		return err
	}
	if command.OperatorID <= 0 {
		return ErrMissingOperatorID
	}
	if command.ExpectedVersion <= 0 {
		return ErrMissingStatusVersion
	}
	if command.Status != PSPStatusSuccess && command.Status != PSPStatusFailed {
		return ErrInvalidStatus
	}
	if command.FulfillmentMethod != FulfillmentManual && command.FulfillmentMethod != FulfillmentOffline {
		return ErrInvalidFulfillmentMethod
	}
	if err := validateBoundedIdentifier(command.Reason, 4096, ErrMissingReason, ErrMissingReason); err != nil {
		return err
	}
	if err := validateBoundedIdentifier(command.EvidenceReference, 4096, ErrMissingEvidenceReference, ErrMissingEvidenceReference); err != nil {
		return err
	}
	if command.Status == PSPStatusSuccess {
		if err := validateBoundedIdentifier(command.SettlementReference, 255, ErrMissingSettlementReference, ErrMissingSettlementReference); err != nil {
			return err
		}
	} else if command.SettlementReference != "" {
		return ErrInvalidStatus
	}
	return nil
}

// CanResolvePSPTransaction excludes the approval and pre-dispatch states: an
// operator records an external outcome, never bypasses withdrawal approval.
func CanResolvePSPTransaction(transaction *PSPTransaction) bool {
	return transaction != nil && transaction.WorkflowID.Valid && transaction.WorkflowID.String != "" &&
		(transaction.Status == PSPStatusProcessing || transaction.Status == PSPStatusPending)
}

func (s *Store) ResolvePSPTransaction(ctx context.Context, command PSPManualResolution) (*PSPTransaction, error) {
	if err := ValidatePSPManualResolution(command); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	transaction := &PSPTransaction{}
	err = tx.GetContext(ctx, transaction, tx.Rebind(`SELECT * FROM psp_transactions WHERE tenant_id = ? AND client_reference = ?`), command.TenantID, command.ClientReference)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPSPTransactionNotFound
	}
	if err != nil {
		return nil, err
	}
	var existing PSPManualResolution
	err = tx.GetContext(ctx, &existing, tx.Rebind(`SELECT tenant_id,client_reference,idempotency_key,operator_id,expected_version,status,fulfillment_method,reason,evidence_reference,settlement_reference
		FROM psp_manual_resolutions WHERE tenant_id = ? AND (client_reference = ? OR idempotency_key = ?)`), command.TenantID, command.ClientReference, command.IdempotencyKey)
	if err == nil {
		if existing != command {
			return nil, ErrManualResolutionConflict
		}
		if err := tx.GetContext(ctx, transaction, tx.Rebind(`SELECT * FROM psp_transactions WHERE tenant_id = ? AND client_reference = ?`), command.TenantID, command.ClientReference); err != nil {
			return nil, err
		}
		return transaction, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if transaction.StatusVersion != command.ExpectedVersion {
		return nil, ErrStatusVersionConflict
	}
	if !CanResolvePSPTransaction(transaction) {
		return nil, ErrManualResolutionNotAllowed
	}
	var operatorExists bool
	if err := tx.GetContext(ctx, &operatorExists, tx.Rebind(`SELECT EXISTS(SELECT 1 FROM operator_identities WHERE id = ?)`), command.OperatorID); err != nil {
		return nil, err
	}
	if !operatorExists {
		return nil, ErrOperatorIdentityNotFound
	}
	result, err := tx.ExecContext(ctx, tx.Rebind(`INSERT INTO psp_manual_resolutions(
		tenant_id,client_reference,idempotency_key,operator_id,expected_version,status,fulfillment_method,reason,evidence_reference,settlement_reference
	) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`), command.TenantID, command.ClientReference, command.IdempotencyKey, command.OperatorID, command.ExpectedVersion, command.Status, command.FulfillmentMethod, command.Reason, command.EvidenceReference, command.SettlementReference)
	if err != nil {
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) {
			switch pgError.ConstraintName {
			case "psp_manual_resolution_version":
				return nil, ErrStatusVersionConflict
			case "psp_manual_resolution_target":
				return nil, ErrManualResolutionNotAllowed
			}
		}
		return nil, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if inserted == 0 {
		if err := tx.GetContext(ctx, &existing, tx.Rebind(`SELECT tenant_id,client_reference,idempotency_key,operator_id,expected_version,status,fulfillment_method,reason,evidence_reference,settlement_reference
		 FROM psp_manual_resolutions WHERE tenant_id = ? AND (client_reference = ? OR idempotency_key = ?)`), command.TenantID, command.ClientReference, command.IdempotencyKey); err != nil {
			return nil, err
		}
		if existing != command {
			return nil, ErrManualResolutionConflict
		}
	}
	var stored PSPTransaction
	if err := tx.GetContext(ctx, &stored, tx.Rebind(`SELECT * FROM psp_transactions WHERE tenant_id = ? AND client_reference = ?`), command.TenantID, command.ClientReference); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &stored, nil
}

func (s *Store) ListPSPTransactionStatusEvents(ctx context.Context, tenantID, clientReference string, limit int) ([]TransactionStatusEvent, error) {
	if _, err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(clientReference) == "" {
		return nil, ErrMissingClientReference
	}
	if limit <= 0 {
		return nil, ErrInvalidLimit
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var events []TransactionStatusEvent
	err = db.SelectContext(ctx, &events, db.Rebind(`SELECT id,event_id,tenant_id,aggregate_id,version,status,substatus,user_id,operator_id,reason,evidence_reference,created_at
		FROM transaction_status_events WHERE tenant_id = ? AND aggregate_id = ? ORDER BY version DESC LIMIT ?`), tenantID, "psp:"+clientReference, limit)
	return events, err
}
