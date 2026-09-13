package tenantaccess

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/adonese/noebs/internal/accountenrollment"
	"github.com/adonese/noebs/internal/pgsession"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/jackc/pgx/v5/pgconn"
)

// PostgresJournal shares the enrollment database. Callers hold the enrollment
// identity lock across journal commits and authority calls; commits deliberately
// precede remote writes so a process crash leaves an explicit recovery record.
type PostgresJournal struct{ db *sql.DB }

func NewPostgresJournal(db *sql.DB) (*PostgresJournal, error) {
	if db == nil {
		return nil, ErrInvalidRequest
	}
	return &PostgresJournal{db: db}, nil
}
func validIdentity(id accountenrollment.Identity) bool {
	return id.Issuer != "" && canonicalID(id.Subject) && id.TenantID != ""
}
func (s *PostgresJournal) Operation(ctx context.Context, id accountenrollment.Identity, operationID string) (*Operation, error) {
	if !validIdentity(id) || !canonicalID(operationID) {
		return nil, ErrInvalidRequest
	}
	// An operation ID is unique in the selected issuer/tenant, including across
	// targets. The service hashes the target and rejects conflicting reuse.
	row := pgsession.Use(ctx, s.db).QueryRowContext(ctx, `SELECT issuer,payload_hash,audit,status,completed_at,completed_roles FROM tenant_access_operations WHERE issuer=$1 AND tenant_id=$2 AND operation_id=$3`, id.Issuer, id.TenantID, operationID)
	op, err := scanOperation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = s.recovery(ctx, op); err != nil {
		return nil, err
	}
	return op, nil
}

type rowScanner interface{ Scan(...any) error }

func scanOperation(row rowScanner) (*Operation, error) {
	var op Operation
	var payload []byte
	var status string
	var completedRoles []byte
	var completed sql.NullTime
	if err := row.Scan(&op.Issuer, &op.PayloadHash, &payload, &status, &completed, &completedRoles); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&op.Audit); err != nil {
		return nil, err
	}
	if completedRoles != nil {
		if err := json.Unmarshal(completedRoles, &op.CompletedRoles); err != nil {
			return nil, err
		}
	}
	op.Status = status
	if completed.Valid {
		op.CompletedAt = &completed.Time
	}
	op.RecoveryAttempts = []RecoveryAttempt{}
	return &op, nil
}
func (s *PostgresJournal) recovery(ctx context.Context, op *Operation) error {
	rows, err := pgsession.Use(ctx, s.db).QueryContext(ctx, `SELECT attempt FROM tenant_access_recovery_attempts WHERE issuer=$1 AND tenant_id=$2 AND operation_id=$3 ORDER BY created_at,request_id`, op.Issuer, op.TenantID, op.OperationID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var payload []byte
		var attempt RecoveryAttempt
		if err = rows.Scan(&payload); err != nil {
			return err
		}
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&attempt); err != nil {
			return err
		}
		op.RecoveryAttempts = append(op.RecoveryAttempts, attempt)
	}
	return rows.Err()
}
func (s *PostgresJournal) History(ctx context.Context, id accountenrollment.Identity) ([]Operation, error) {
	if !validIdentity(id) {
		return nil, ErrInvalidRequest
	}
	rows, err := pgsession.Use(ctx, s.db).QueryContext(ctx, `SELECT issuer,payload_hash,audit,status,completed_at,completed_roles FROM tenant_access_operations WHERE issuer=$1 AND subject=$2 AND tenant_id=$3 ORDER BY created_at,operation_id`, id.Issuer, id.Subject, id.TenantID)
	if err != nil {
		return nil, err
	}
	result := []Operation{}
	for rows.Next() {
		op, e := scanOperation(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		result = append(result, *op)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range result {
		if err = s.recovery(ctx, &result[i]); err != nil {
			return nil, err
		}
	}
	return result, nil
}
func (s *PostgresJournal) Create(ctx context.Context, op Operation) error {
	request := ChangeRequest{OperationID: op.OperationID, TargetSubject: op.Subject, ExpectedRevision: op.ExpectedRevision, GrantRoles: op.GrantRoles, RevokeRoles: op.RevokeRoles, Reason: op.Reason}
	if validateChange(request) != nil || op.Issuer == "" || op.TenantID == "" || op.Status != "pending" || len(op.PayloadHash) != 64 || op.CreatedAt.IsZero() || op.CompletedAt != nil || len(op.RecoveryAttempts) != 0 {
		return ErrInvalidRequest
	}
	payload, err := json.Marshal(op.Audit)
	if err != nil {
		return err
	}
	tx, err := pgsession.Use(ctx, s.db).BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO tenant_access_operations(issuer,subject,tenant_id,operation_id,payload_hash,audit,status,created_at) VALUES($1,$2,$3,$4,$5,$6::jsonb,'pending',$7)`, op.Issuer, op.Subject, op.TenantID, op.OperationID, op.PayloadHash, payload, op.CreatedAt)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			if pg.ConstraintName == "tenant_access_one_pending" {
				return ErrPendingOperation
			}
			return ErrOperationConflict
		}
		return err
	}
	if slices.Contains(op.RevokeRoles, tenantauth.RoleUser) {
		_, err = tx.ExecContext(ctx, `INSERT INTO account_enrollments(issuer,subject,tenant_id,progress) VALUES($1,$2,$3,'{"complete":true}'::jsonb) ON CONFLICT(issuer,subject,tenant_id) DO UPDATE SET progress=EXCLUDED.progress,updated_at=clock_timestamp()`, op.Issuer, op.Subject, op.TenantID)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *PostgresJournal) RecordRecovery(ctx context.Context, id accountenrollment.Identity, operationID string, attempt RecoveryAttempt) error {
	if !validIdentity(id) || !canonicalID(operationID) || !canonicalID(attempt.ActorSubject) || attempt.RequestID == "" || attempt.AttemptedAt.IsZero() {
		return ErrInvalidRequest
	}
	payload, err := json.Marshal(attempt)
	if err != nil {
		return err
	}
	result, err := pgsession.Use(ctx, s.db).ExecContext(ctx, `INSERT INTO tenant_access_recovery_attempts(issuer,tenant_id,operation_id,request_id,attempt,created_at) SELECT issuer,tenant_id,operation_id,$5,$6::jsonb,$7 FROM tenant_access_operations WHERE issuer=$1 AND subject=$2 AND tenant_id=$3 AND operation_id=$4 AND status='pending' ON CONFLICT DO NOTHING`, id.Issuer, id.Subject, id.TenantID, operationID, attempt.RequestID, payload, attempt.AttemptedAt)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 1 {
		return err
	}
	var stored []byte
	if err = pgsession.Use(ctx, s.db).QueryRowContext(ctx, `SELECT attempt FROM tenant_access_recovery_attempts WHERE issuer=$1 AND tenant_id=$2 AND operation_id=$3 AND request_id=$4`, id.Issuer, id.TenantID, operationID, attempt.RequestID).Scan(&stored); err != nil {
		return err
	}
	var previous RecoveryAttempt
	if err = json.Unmarshal(stored, &previous); err != nil {
		return err
	}
	if previous.ActorSubject != attempt.ActorSubject || previous.SourceIP != attempt.SourceIP || !slices.Equal(previous.ActorRoles, attempt.ActorRoles) {
		return ErrOperationConflict
	}
	return nil
}
func (s *PostgresJournal) Complete(ctx context.Context, id accountenrollment.Identity, operationID string, completed time.Time, roles []tenantauth.Role) error {
	if !validIdentity(id) || !canonicalID(operationID) || completed.IsZero() || !validRoles(roles) {
		return ErrInvalidRequest
	}
	payload, err := json.Marshal(append([]tenantauth.Role{}, roles...))
	if err != nil {
		return err
	}
	result, err := pgsession.Use(ctx, s.db).ExecContext(ctx, `UPDATE tenant_access_operations SET status='complete',completed_at=$5,completed_roles=$6::jsonb WHERE issuer=$1 AND subject=$2 AND tenant_id=$3 AND operation_id=$4 AND status='pending'`, id.Issuer, id.Subject, id.TenantID, operationID, completed, payload)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrOperationConflict
	}
	return nil
}

// Tenant mutations take this lock before the per-identity enrollment lock. It
// serializes last-administrator checks and bootstrap across distinct subjects.
func (s *PostgresJournal) WithTenant(ctx context.Context, issuer, tenant string, work func(context.Context) error) error {
	if issuer == "" || tenant == "" || work == nil {
		return ErrInvalidRequest
	}
	return pgsession.With(ctx, s.db, func(ctx context.Context, conn *sql.Conn) error {
		key := issuer + "\x1f" + tenant
		if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1,49288))`, key); err != nil {
			return err
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, err := conn.ExecContext(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1,49288))`, key); err != nil {
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
		}()
		return work(ctx)
	})
}
