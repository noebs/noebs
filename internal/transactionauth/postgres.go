package transactionauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) (*PostgresStore, error) {
	if db == nil {
		return nil, ErrInvalidConfiguration
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) CreateIntent(ctx context.Context, intent IntentRecord) error {
	if s == nil || s.db == nil || ctx == nil || !validPendingIntent(intent) {
		return ErrInvalidInput
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO wallet_transaction_authorization_intents (
			intent_hash, browser_start_hash, tenant_id, issuer, subject, operation,
			request_digest, idempotency_key, created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, intent.IntentHash[:], intent.BrowserStartHash[:], intent.Binding.TenantID, intent.Binding.Issuer,
		intent.Binding.Subject, string(intent.Binding.Operation), intent.Binding.RequestDigest[:],
		intent.Binding.IdempotencyKey, intent.CreatedAt.UTC(), intent.ExpiresAt.UTC())
	if err != nil {
		if isUniqueViolation(err) {
			return ErrIntentConflict
		}
		return storeError(err)
	}
	return nil
}

func (s *PostgresStore) StartFlow(
	ctx context.Context,
	browserStartHash Digest,
	flow NewFlowRecord,
	now time.Time,
) (FlowRecord, error) {
	if s == nil || s.db == nil || ctx == nil || browserStartHash == (Digest{}) || !validNewFlow(flow) || now.IsZero() {
		return FlowRecord{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return FlowRecord{}, storeError(err)
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `
		UPDATE wallet_transaction_authorization_intents
		SET browser_start_hash = NULL, browser_started_at = $2
		WHERE browser_start_hash = $1
		  AND authorized_at IS NULL
		  AND expires_at > $2
		RETURNING intent_hash, tenant_id, issuer, subject, operation, request_digest, idempotency_key
	`, browserStartHash[:], now.UTC())
	intentHash, binding, err := scanIntentIdentity(row)
	if errors.Is(err, sql.ErrNoRows) {
		return FlowRecord{}, ErrInvalidBrowserStart
	}
	if err != nil {
		return FlowRecord{}, storeError(err)
	}
	row = tx.QueryRowContext(ctx, `
		INSERT INTO wallet_transaction_authorization_flows (
			state_hash, intent_hash, browser_hash, pkce_key_id, pkce_nonce,
			pkce_ciphertext, nonce_hash, created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, LEAST($9, (
			SELECT expires_at FROM wallet_transaction_authorization_intents WHERE intent_hash = $2
		)))
		RETURNING expires_at
	`, flow.StateHash[:], intentHash[:], flow.BrowserHash[:], flow.PKCEVerifier.KeyID,
		flow.PKCEVerifier.Nonce, flow.PKCEVerifier.Ciphertext, flow.NonceHash[:],
		flow.CreatedAt.UTC(), flow.ExpiresAt.UTC())
	err = row.Scan(&flow.ExpiresAt)
	if err != nil {
		if isUniqueViolation(err) {
			return FlowRecord{}, ErrInvalidBrowserStart
		}
		return FlowRecord{}, storeError(err)
	}
	if err := tx.Commit(); err != nil {
		return FlowRecord{}, storeError(err)
	}
	return FlowRecord{NewFlowRecord: flow, IntentHash: intentHash, Binding: binding}, nil
}

func (s *PostgresStore) ConsumeFlow(
	ctx context.Context,
	stateHash Digest,
	browserHash Digest,
	now time.Time,
) (FlowRecord, error) {
	if s == nil || s.db == nil || ctx == nil || stateHash == (Digest{}) || browserHash == (Digest{}) || now.IsZero() {
		return FlowRecord{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return FlowRecord{}, storeError(err)
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `
		DELETE FROM wallet_transaction_authorization_flows
		WHERE state_hash = $1 AND browser_hash = $2 AND expires_at > $3
		RETURNING intent_hash, state_hash, browser_hash, pkce_key_id, pkce_nonce,
			pkce_ciphertext, nonce_hash, created_at, expires_at
	`, stateHash[:], browserHash[:], now.UTC())
	flow, err := scanFlow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return FlowRecord{}, ErrInvalidFlow
	}
	if err != nil {
		return FlowRecord{}, storeError(err)
	}
	row = tx.QueryRowContext(ctx, `
		SELECT intent_hash, tenant_id, issuer, subject, operation, request_digest, idempotency_key
		FROM wallet_transaction_authorization_intents
		WHERE intent_hash = $1 AND authorized_at IS NULL AND expires_at > $2
	`, flow.IntentHash[:], now.UTC())
	intentHash, binding, err := scanIntentIdentity(row)
	if errors.Is(err, sql.ErrNoRows) {
		return FlowRecord{}, ErrInvalidFlow
	}
	if err != nil {
		return FlowRecord{}, storeError(err)
	}
	if intentHash != flow.IntentHash {
		return FlowRecord{}, ErrInvalidFlow
	}
	flow.Binding = binding
	if err := tx.Commit(); err != nil {
		return FlowRecord{}, storeError(err)
	}
	return flow, nil
}

func (s *PostgresStore) AuthorizeIntent(
	ctx context.Context,
	intentHash Digest,
	authorizedAt time.Time,
	authenticationTime time.Time,
	expiresAt time.Time,
) error {
	if s == nil || s.db == nil || ctx == nil || intentHash == (Digest{}) || authorizedAt.IsZero() ||
		authenticationTime.IsZero() || expiresAt.IsZero() || !expiresAt.After(authorizedAt) {
		return ErrInvalidInput
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE wallet_transaction_authorization_intents
		SET authorized_at = $2, authentication_time = $3, expires_at = $4
		WHERE intent_hash = $1
		  AND browser_start_hash IS NULL
		  AND authorized_at IS NULL
		  AND expires_at > $2
	`, intentHash[:], authorizedAt.UTC(), authenticationTime.UTC(), expiresAt.UTC())
	if err != nil {
		return storeError(err)
	}
	return requireOneRow(result, ErrAuthorizationDenied)
}

func (s *PostgresStore) ClaimIntent(ctx context.Context, intentHash Digest, binding Binding, now time.Time) error {
	if s == nil || s.db == nil || ctx == nil || intentHash == (Digest{}) || now.IsZero() {
		return ErrInvalidInput
	}
	if err := binding.Validate(); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM wallet_transaction_authorization_intents
		WHERE intent_hash = $1
		  AND tenant_id = $2
		  AND issuer = $3
		  AND subject = $4
		  AND operation = $5
		  AND request_digest = $6
		  AND idempotency_key = $7
		  AND authorized_at IS NOT NULL
		  AND expires_at > $8
	`, intentHash[:], binding.TenantID, binding.Issuer, binding.Subject, string(binding.Operation),
		binding.RequestDigest[:], binding.IdempotencyKey, now.UTC())
	if err != nil {
		return storeError(err)
	}
	return requireOneRow(result, ErrIntentNotFound)
}

func (s *PostgresStore) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	if s == nil || s.db == nil || ctx == nil || before.IsZero() {
		return 0, ErrInvalidInput
	}
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM wallet_transaction_authorization_intents WHERE expires_at <= $1
	`, before.UTC())
	if err != nil {
		return 0, storeError(err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, storeError(err)
	}
	return rows, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanIntentIdentity(row rowScanner) (Digest, Binding, error) {
	var intentHash, requestDigest []byte
	var binding Binding
	var operation string
	if err := row.Scan(
		&intentHash,
		&binding.TenantID,
		&binding.Issuer,
		&binding.Subject,
		&operation,
		&requestDigest,
		&binding.IdempotencyKey,
	); err != nil {
		return Digest{}, Binding{}, err
	}
	var parsedIntentHash Digest
	if !copyDigest(&parsedIntentHash, intentHash) || !copyDigest(&binding.RequestDigest, requestDigest) {
		return Digest{}, Binding{}, ErrInvalidInput
	}
	binding.Operation = Operation(operation)
	if err := binding.Validate(); err != nil {
		return Digest{}, Binding{}, ErrInvalidInput
	}
	return parsedIntentHash, binding, nil
}

func scanFlow(row rowScanner) (FlowRecord, error) {
	var flow FlowRecord
	var intentHash, stateHash, browserHash, nonceHash []byte
	if err := row.Scan(
		&intentHash,
		&stateHash,
		&browserHash,
		&flow.PKCEVerifier.KeyID,
		&flow.PKCEVerifier.Nonce,
		&flow.PKCEVerifier.Ciphertext,
		&nonceHash,
		&flow.CreatedAt,
		&flow.ExpiresAt,
	); err != nil {
		return FlowRecord{}, err
	}
	if !copyDigest(&flow.IntentHash, intentHash) || !copyDigest(&flow.StateHash, stateHash) ||
		!copyDigest(&flow.BrowserHash, browserHash) || !copyDigest(&flow.NonceHash, nonceHash) ||
		!validNewFlow(flow.NewFlowRecord) {
		return FlowRecord{}, ErrInvalidInput
	}
	return flow, nil
}

func validPendingIntent(intent IntentRecord) bool {
	return intent.IntentHash != (Digest{}) && intent.BrowserStartHash != (Digest{}) && intent.Binding.Validate() == nil &&
		!intent.CreatedAt.IsZero() && intent.ExpiresAt.After(intent.CreatedAt) && intent.AuthorizedAt.IsZero() &&
		intent.AuthenticationTime.IsZero()
}

func validNewFlow(flow NewFlowRecord) bool {
	return flow.StateHash != (Digest{}) && flow.BrowserHash != (Digest{}) && flow.NonceHash != (Digest{}) &&
		validEnvelope(flow.PKCEVerifier) && !flow.CreatedAt.IsZero() && flow.ExpiresAt.After(flow.CreatedAt)
}

func copyDigest(target *Digest, raw []byte) bool {
	if len(raw) != len(target) {
		return false
	}
	copy(target[:], raw)
	return true
}

func requireOneRow(result sql.Result, notFound error) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return storeError(err)
	}
	if rows == 0 {
		return notFound
	}
	if rows != 1 {
		return ErrInvalidInput
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}

func storeError(err error) error {
	return fmt.Errorf("%w: %w", ErrStoreUnavailable, err)
}
