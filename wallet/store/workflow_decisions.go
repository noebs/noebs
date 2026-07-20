package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

type WorkflowDecisionKind string

const (
	WorkflowDecisionManualTransfer      WorkflowDecisionKind = "manual_transfer"
	WorkflowDecisionWithdrawal          WorkflowDecisionKind = "withdrawal"
	WorkflowDecisionMaxWorkflowIDLength                      = 255
	WorkflowDecisionMaxEvidenceLength                        = 4096
)

type WorkflowDecision struct {
	TenantID            string               `db:"tenant_id"`
	WorkflowID          string               `db:"workflow_id"`
	Kind                WorkflowDecisionKind `db:"decision_kind"`
	SubjectID           int64                `db:"subject_id"`
	Approved            bool                 `db:"approved"`
	DecidedByOperatorID int64                `db:"decided_by_operator_id"`
	Reason              sql.NullString       `db:"reason"`
	ProofOfPayment      sql.NullString       `db:"proof_of_payment"`
	DecidedAt           time.Time            `db:"decided_at"`
}

type WorkflowDecisionKey struct {
	TenantID   string
	WorkflowID string
	Kind       WorkflowDecisionKind
	SubjectID  int64
}

type WorkflowDecisionLookup struct {
	Found    bool
	Decision WorkflowDecision
}

type WorkflowDecisionWindowClose struct {
	Key    WorkflowDecisionKey
	Reason string
}

func ValidateWorkflowDecisionKind(kind WorkflowDecisionKind) error {
	switch kind {
	case "":
		return ErrMissingDecisionKind
	case WorkflowDecisionManualTransfer, WorkflowDecisionWithdrawal:
		return nil
	default:
		return ErrInvalidDecisionKind
	}
}

func ValidateWorkflowDecision(decision WorkflowDecision) error {
	if _, err := ValidateTenantID(decision.TenantID); err != nil {
		return err
	}
	if strings.TrimSpace(decision.WorkflowID) == "" {
		return ErrMissingWorkflowID
	}
	if decision.WorkflowID != strings.TrimSpace(decision.WorkflowID) || len(decision.WorkflowID) > WorkflowDecisionMaxWorkflowIDLength {
		return ErrInvalidWorkflowID
	}
	if err := ValidateWorkflowDecisionKind(decision.Kind); err != nil {
		return err
	}
	if decision.SubjectID <= 0 {
		return ErrMissingDecisionSubject
	}
	if decision.DecidedByOperatorID <= 0 {
		return ErrMissingApproverID
	}
	if decision.Approved {
		if !validWorkflowDecisionText(decision.ProofOfPayment) {
			return ErrMissingProofOfPayment
		}
		if decision.Reason.Valid && !validWorkflowDecisionText(decision.Reason) {
			return ErrMissingReason
		}
		return nil
	}
	if !validWorkflowDecisionText(decision.Reason) {
		return ErrMissingApprovalReason
	}
	if decision.ProofOfPayment.Valid {
		return ErrInvalidDecision
	}
	return nil
}

func ValidateWorkflowDecisionKey(key WorkflowDecisionKey) error {
	if _, err := ValidateTenantID(key.TenantID); err != nil {
		return err
	}
	if strings.TrimSpace(key.WorkflowID) == "" {
		return ErrMissingWorkflowID
	}
	if key.WorkflowID != strings.TrimSpace(key.WorkflowID) || len(key.WorkflowID) > WorkflowDecisionMaxWorkflowIDLength {
		return ErrInvalidWorkflowID
	}
	if err := ValidateWorkflowDecisionKind(key.Kind); err != nil {
		return err
	}
	if key.SubjectID <= 0 {
		return ErrMissingDecisionSubject
	}
	return nil
}

func (s *Store) ReserveWorkflowDecision(ctx context.Context, decision WorkflowDecision) (*WorkflowDecision, error) {
	tenantID, err := ValidateTenantID(decision.TenantID)
	if err != nil {
		return nil, err
	}
	decision.TenantID = tenantID
	if err := ValidateWorkflowDecision(decision); err != nil {
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
	existing, decidedAt, err := prepareWorkflowDecisionReservationTx(ctx, tx, decision)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return existing, nil
	}
	stmt := tx.Rebind(`INSERT INTO workflow_decisions(
		tenant_id, workflow_id, decision_kind, subject_id, approved,
		decided_by_operator_id, reason, proof_of_payment, decided_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (tenant_id, workflow_id) DO NOTHING
	RETURNING *`)
	var stored WorkflowDecision
	if err := tx.GetContext(ctx, &stored, stmt,
		decision.TenantID,
		decision.WorkflowID,
		decision.Kind,
		decision.SubjectID,
		decision.Approved,
		decision.DecidedByOperatorID,
		decision.Reason,
		decision.ProofOfPayment,
		decidedAt,
	); err == nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &stored, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	existing, err = getWorkflowDecisionTx(ctx, tx, decision.TenantID, decision.WorkflowID)
	if err != nil {
		return nil, err
	}
	if !workflowDecisionEqual(*existing, decision) {
		return nil, ErrWorkflowDecisionConflict
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return existing, nil
}

func prepareWorkflowDecisionReservationTx(ctx context.Context, tx *sqlx.Tx, decision WorkflowDecision) (*WorkflowDecision, time.Time, error) {
	key := WorkflowDecisionKey{
		TenantID: decision.TenantID, WorkflowID: decision.WorkflowID, Kind: decision.Kind, SubjectID: decision.SubjectID,
	}
	if err := lockWorkflowDecisionSubjectTx(ctx, tx, key); err != nil {
		return nil, time.Time{}, err
	}
	var transfer ManualTransfer
	var transaction PSPTransaction
	switch decision.Kind {
	case WorkflowDecisionManualTransfer:
		stmt := tx.Rebind(`SELECT * FROM manual_transfers
			WHERE tenant_id = ? AND id = ? AND workflow_id = ?`)
		if err := tx.GetContext(ctx, &transfer, stmt, decision.TenantID, decision.SubjectID, decision.WorkflowID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, time.Time{}, ErrManualTransferNotFound
			}
			return nil, time.Time{}, err
		}
	case WorkflowDecisionWithdrawal:
		stmt := tx.Rebind(`SELECT * FROM psp_transactions
			WHERE tenant_id = ? AND id = ? AND workflow_id = ?`)
		if err := tx.GetContext(ctx, &transaction, stmt, decision.TenantID, decision.SubjectID, decision.WorkflowID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, time.Time{}, ErrPSPTransactionNotFound
			}
			return nil, time.Time{}, err
		}
	default:
		return nil, time.Time{}, ErrInvalidDecisionKind
	}

	existing, err := getWorkflowDecisionTx(ctx, tx, decision.TenantID, decision.WorkflowID)
	if err == nil {
		if !workflowDecisionEqual(*existing, decision) {
			return nil, time.Time{}, ErrWorkflowDecisionConflict
		}
		return existing, time.Time{}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, err
	}

	switch decision.Kind {
	case WorkflowDecisionManualTransfer:
		if transfer.Status != ManualTransferStatusPending {
			return nil, time.Time{}, ErrInvalidStatusTransition
		}
		if transfer.RequestedByOperatorID == decision.DecidedByOperatorID {
			return nil, time.Time{}, ErrApproverIsRequester
		}
	case WorkflowDecisionWithdrawal:
		if err := ValidateWithdrawalApprovalTarget(&transaction); err != nil {
			return nil, time.Time{}, err
		}
	}

	var decidedAt time.Time
	if err := tx.GetContext(ctx, &decidedAt, "SELECT clock_timestamp()"); err != nil {
		return nil, time.Time{}, err
	}
	deadline := transfer.DecisionDeadlineAt
	if decision.Kind == WorkflowDecisionWithdrawal {
		if !transaction.DecisionDeadlineAt.Valid || transaction.DecisionDeadlineAt.Time.IsZero() {
			return nil, time.Time{}, ErrMissingApprovalTimeout
		}
		deadline = transaction.DecisionDeadlineAt.Time
	}
	if !decidedAt.Before(deadline) {
		return nil, time.Time{}, ErrWorkflowDecisionWindowClosed
	}
	return nil, decidedAt, nil
}

func (s *Store) LookupWorkflowDecision(ctx context.Context, key WorkflowDecisionKey) (WorkflowDecisionLookup, error) {
	tenantID, err := ValidateTenantID(key.TenantID)
	if err != nil {
		return WorkflowDecisionLookup{}, err
	}
	key.TenantID = tenantID
	if err := ValidateWorkflowDecisionKey(key); err != nil {
		return WorkflowDecisionLookup{}, err
	}
	decision, err := s.getWorkflowDecision(ctx, key.TenantID, key.WorkflowID)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkflowDecisionLookup{}, nil
	}
	if err != nil {
		return WorkflowDecisionLookup{}, err
	}
	if decision.Kind != key.Kind || decision.SubjectID != key.SubjectID {
		return WorkflowDecisionLookup{}, ErrWorkflowDecisionConflict
	}
	return WorkflowDecisionLookup{Found: true, Decision: *decision}, nil
}

func (s *Store) CloseWorkflowDecisionWindow(ctx context.Context, close WorkflowDecisionWindowClose) (WorkflowDecisionLookup, error) {
	if err := ValidateWorkflowDecisionKey(close.Key); err != nil {
		return WorkflowDecisionLookup{}, err
	}
	if close.Reason == "" || close.Reason != strings.TrimSpace(close.Reason) || len(close.Reason) > WorkflowDecisionMaxEvidenceLength {
		return WorkflowDecisionLookup{}, ErrMissingReason
	}
	db, err := s.ensureDB()
	if err != nil {
		return WorkflowDecisionLookup{}, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return WorkflowDecisionLookup{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockWorkflowDecisionSubjectTx(ctx, tx, close.Key); err != nil {
		return WorkflowDecisionLookup{}, err
	}

	target, err := lockWorkflowDecisionTargetTx(ctx, tx, close.Key)
	if err != nil {
		return WorkflowDecisionLookup{}, err
	}
	existing, err := getWorkflowDecisionTx(ctx, tx, close.Key.TenantID, close.Key.WorkflowID)
	if err == nil {
		if existing.Kind != close.Key.Kind || existing.SubjectID != close.Key.SubjectID {
			return WorkflowDecisionLookup{}, ErrWorkflowDecisionConflict
		}
		if err := tx.Commit(); err != nil {
			return WorkflowDecisionLookup{}, err
		}
		return WorkflowDecisionLookup{Found: true, Decision: *existing}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return WorkflowDecisionLookup{}, err
	}
	if target.closedWithReason(close.Reason) {
		if err := tx.Commit(); err != nil {
			return WorkflowDecisionLookup{}, err
		}
		return WorkflowDecisionLookup{}, nil
	}
	if !target.deadline.Valid || target.deadline.Time.IsZero() {
		return WorkflowDecisionLookup{}, ErrMissingApprovalTimeout
	}
	var closedAt time.Time
	if err := tx.GetContext(ctx, &closedAt, "SELECT clock_timestamp()"); err != nil {
		return WorkflowDecisionLookup{}, err
	}
	if closedAt.Before(target.deadline.Time) {
		return WorkflowDecisionLookup{}, ErrWorkflowDecisionWindowOpen
	}

	switch close.Key.Kind {
	case WorkflowDecisionManualTransfer:
		if target.status != ManualTransferStatusPending {
			return WorkflowDecisionLookup{}, ErrInvalidStatusTransition
		}
		stmt := tx.Rebind(`UPDATE manual_transfers
			SET status = 'rejected', rejection_reason = ?
			WHERE tenant_id = ? AND id = ? AND workflow_id = ? AND status = 'pending'`)
		if _, err := tx.ExecContext(ctx, stmt, close.Reason, close.Key.TenantID, close.Key.SubjectID, close.Key.WorkflowID); err != nil {
			return WorkflowDecisionLookup{}, err
		}
	case WorkflowDecisionWithdrawal:
		switch target.status {
		case PSPStatusInitiated, PSPStatusPending, PSPStatusHeld:
		default:
			return WorkflowDecisionLookup{}, ErrInvalidStatusTransition
		}
		stmt := tx.Rebind(`UPDATE psp_transactions
			SET status = 'cancelled', response_message = ?, last_error_type = 'approval_timeout', last_error_at = clock_timestamp()
			WHERE tenant_id = ? AND id = ? AND workflow_id = ?
			AND status IN ('initiated', 'pending', 'held')`)
		if _, err := tx.ExecContext(ctx, stmt, close.Reason, close.Key.TenantID, close.Key.SubjectID, close.Key.WorkflowID); err != nil {
			return WorkflowDecisionLookup{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return WorkflowDecisionLookup{}, err
	}
	return WorkflowDecisionLookup{}, nil
}

type lockedWorkflowDecisionTarget struct {
	status   string
	reason   sql.NullString
	deadline sql.NullTime
}

func lockWorkflowDecisionSubjectTx(ctx context.Context, tx *sqlx.Tx, key WorkflowDecisionKey) error {
	stmt := tx.Rebind("SELECT pg_advisory_xact_lock(?)")
	_, err := tx.ExecContext(ctx, stmt, workflowDecisionSubjectLockKey(key))
	return err
}

func workflowDecisionSubjectLockKey(key WorkflowDecisionKey) int64 {
	identity := make([]byte, 0, len(key.TenantID)+len(key.Kind)+20)
	identity = binary.BigEndian.AppendUint32(identity, uint32(len(key.TenantID)))
	identity = append(identity, key.TenantID...)
	identity = binary.BigEndian.AppendUint32(identity, uint32(len(key.Kind)))
	identity = append(identity, key.Kind...)
	identity = binary.BigEndian.AppendUint64(identity, uint64(key.SubjectID))
	digest := sha256.Sum256(identity)
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

func (target lockedWorkflowDecisionTarget) closedWithReason(reason string) bool {
	return target.reason.Valid && target.reason.String == reason &&
		(target.status == ManualTransferStatusRejected || target.status == PSPStatusCancelled)
}

func lockWorkflowDecisionTargetTx(ctx context.Context, tx *sqlx.Tx, key WorkflowDecisionKey) (lockedWorkflowDecisionTarget, error) {
	var target lockedWorkflowDecisionTarget
	switch key.Kind {
	case WorkflowDecisionManualTransfer:
		stmt := tx.Rebind(`SELECT status, rejection_reason, decision_deadline_at FROM manual_transfers
			WHERE tenant_id = ? AND id = ? AND workflow_id = ? FOR UPDATE`)
		if err := tx.QueryRowxContext(ctx, stmt, key.TenantID, key.SubjectID, key.WorkflowID).Scan(&target.status, &target.reason, &target.deadline); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return target, ErrManualTransferNotFound
			}
			return target, err
		}
	case WorkflowDecisionWithdrawal:
		stmt := tx.Rebind(`SELECT status, response_message, decision_deadline_at FROM psp_transactions
			WHERE tenant_id = ? AND id = ? AND workflow_id = ? FOR UPDATE`)
		if err := tx.QueryRowxContext(ctx, stmt, key.TenantID, key.SubjectID, key.WorkflowID).Scan(&target.status, &target.reason, &target.deadline); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return target, ErrPSPTransactionNotFound
			}
			return target, err
		}
	default:
		return target, ErrInvalidDecisionKind
	}
	return target, nil
}

func (s *Store) getWorkflowDecision(ctx context.Context, tenantID, workflowID string) (*WorkflowDecision, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT * FROM workflow_decisions
		WHERE tenant_id = ? AND workflow_id = ?`)
	var decision WorkflowDecision
	if err := db.GetContext(ctx, &decision, stmt, tenantID, workflowID); err != nil {
		return nil, err
	}
	return &decision, nil
}

func getWorkflowDecisionTx(ctx context.Context, tx *sqlx.Tx, tenantID, workflowID string) (*WorkflowDecision, error) {
	stmt := tx.Rebind(`SELECT * FROM workflow_decisions
		WHERE tenant_id = ? AND workflow_id = ?`)
	var decision WorkflowDecision
	if err := tx.GetContext(ctx, &decision, stmt, tenantID, workflowID); err != nil {
		return nil, err
	}
	return &decision, nil
}

func workflowDecisionEqual(existing, requested WorkflowDecision) bool {
	return existing.TenantID == requested.TenantID &&
		existing.WorkflowID == requested.WorkflowID &&
		existing.Kind == requested.Kind &&
		existing.SubjectID == requested.SubjectID &&
		existing.Approved == requested.Approved &&
		existing.DecidedByOperatorID == requested.DecidedByOperatorID &&
		nullStringEqual(existing.Reason, requested.Reason) &&
		nullStringEqual(existing.ProofOfPayment, requested.ProofOfPayment)
}

func validWorkflowDecisionText(value sql.NullString) bool {
	return value.Valid &&
		value.String == strings.TrimSpace(value.String) &&
		value.String != "" &&
		len(value.String) <= WorkflowDecisionMaxEvidenceLength
}
