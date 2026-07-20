package backofficeauth

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

func (s *PostgresStore) CreateFlow(ctx context.Context, flow FlowRecord) error {
	if s == nil || s.db == nil || ctx == nil || !validFlow(flow) {
		return ErrInvalidInput
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO backoffice_auth_flows (
			state_hash, browser_hash, pkce_key_id, pkce_nonce, pkce_ciphertext,
			nonce_hash, return_path, created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, flow.StateHash[:], flow.BrowserHash[:], flow.PKCEVerifier.KeyID, flow.PKCEVerifier.Nonce,
		flow.PKCEVerifier.Ciphertext, flow.NonceHash[:], flow.ReturnPath, flow.CreatedAt.UTC(), flow.ExpiresAt.UTC())
	if err != nil {
		if isUniqueViolation(err) {
			return ErrFlowConflict
		}
		return storeError(err)
	}
	return nil
}

func (s *PostgresStore) ConsumeFlow(ctx context.Context, stateHash, browserHash Digest, now time.Time) (FlowRecord, error) {
	if s == nil || s.db == nil || ctx == nil || now.IsZero() {
		return FlowRecord{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
		DELETE FROM backoffice_auth_flows
		WHERE state_hash = $1 AND browser_hash = $2 AND expires_at > $3
		RETURNING state_hash, browser_hash, pkce_key_id, pkce_nonce, pkce_ciphertext,
			nonce_hash, return_path, created_at, expires_at
	`, stateHash[:], browserHash[:], now.UTC())
	flow, err := scanFlow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return FlowRecord{}, ErrInvalidFlow
	}
	if err != nil {
		return FlowRecord{}, storeError(err)
	}
	return flow, nil
}

func (s *PostgresStore) CreateSession(ctx context.Context, session SessionRecord) error {
	if s == nil || s.db == nil || ctx == nil || !validSession(session) {
		return ErrInvalidInput
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO backoffice_sessions (
			session_hash, issuer, subject,
			tokens_key_id, tokens_nonce, tokens_ciphertext,
			access_expires_at, refresh_expires_at, idle_expires_at, absolute_expires_at,
			last_seen_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, session.SessionHash[:], session.Issuer, session.Subject,
		session.Tokens.KeyID, session.Tokens.Nonce, session.Tokens.Ciphertext,
		session.AccessExpiresAt.UTC(), session.RefreshExpiresAt.UTC(), session.IdleExpiresAt.UTC(),
		session.AbsoluteExpiresAt.UTC(), session.LastSeenAt.UTC(), session.CreatedAt.UTC(), session.UpdatedAt.UTC())
	if err != nil {
		if isUniqueViolation(err) {
			return ErrSessionConflict
		}
		return storeError(err)
	}
	return nil
}

func (s *PostgresStore) LoadSession(ctx context.Context, sessionHash Digest) (SessionRecord, error) {
	if s == nil || s.db == nil || ctx == nil {
		return SessionRecord{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, sessionSelect+` WHERE session_hash = $1`, sessionHash[:])
	session, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionRecord{}, ErrSessionNotFound
	}
	if err != nil {
		return SessionRecord{}, storeError(err)
	}
	return session, nil
}

func (s *PostgresStore) RefreshSession(
	ctx context.Context,
	sessionHash Digest,
	clock Clock,
	refreshSkew time.Duration,
	refresh RefreshSessionFunc,
) (SessionRecord, error) {
	if s == nil || s.db == nil || ctx == nil || clock == nil || refreshSkew <= 0 || refresh == nil {
		return SessionRecord{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return SessionRecord{}, storeError(err)
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, sessionSelect+` WHERE session_hash = $1 FOR UPDATE`, sessionHash[:])
	session, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionRecord{}, ErrSessionNotFound
	}
	if err != nil {
		return SessionRecord{}, storeError(err)
	}
	lockedAt := clock.Now().UTC()
	if sessionDeadlinePassed(session, lockedAt) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM backoffice_sessions WHERE session_hash = $1`, sessionHash[:]); err != nil {
			return SessionRecord{}, storeError(err)
		}
		if err := tx.Commit(); err != nil {
			return SessionRecord{}, storeError(err)
		}
		return SessionRecord{}, ErrSessionExpired
	}
	if session.AccessExpiresAt.After(lockedAt.Add(refreshSkew)) {
		if err := tx.Commit(); err != nil {
			return SessionRecord{}, storeError(err)
		}
		return session, nil
	}

	updated, err := refresh(ctx, session)
	completedAt := clock.Now().UTC()
	if sessionDeadlinePassed(session, completedAt) {
		if _, deleteErr := tx.ExecContext(ctx, `DELETE FROM backoffice_sessions WHERE session_hash = $1`, sessionHash[:]); deleteErr != nil {
			return SessionRecord{}, storeError(deleteErr)
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return SessionRecord{}, storeError(commitErr)
		}
		return SessionRecord{}, ErrSessionExpired
	}
	if err != nil {
		if errors.Is(err, ErrSessionRevoked) {
			if _, deleteErr := tx.ExecContext(ctx, `DELETE FROM backoffice_sessions WHERE session_hash = $1`, sessionHash[:]); deleteErr != nil {
				return SessionRecord{}, storeError(deleteErr)
			}
			if commitErr := tx.Commit(); commitErr != nil {
				return SessionRecord{}, storeError(commitErr)
			}
		}
		return SessionRecord{}, err
	}
	if !validEnvelope(updated.Tokens) || !updated.AccessExpiresAt.After(completedAt) || !updated.RefreshExpiresAt.After(completedAt) {
		return SessionRecord{}, ErrInvalidInput
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE backoffice_sessions
		SET tokens_key_id = $2,
			tokens_nonce = $3,
			tokens_ciphertext = $4,
			access_expires_at = $5,
			refresh_expires_at = $6,
			updated_at = $7
		WHERE session_hash = $1
	`, sessionHash[:], updated.Tokens.KeyID, updated.Tokens.Nonce, updated.Tokens.Ciphertext,
		updated.AccessExpiresAt.UTC(), updated.RefreshExpiresAt.UTC(), completedAt)
	if err != nil {
		return SessionRecord{}, storeError(err)
	}
	if err := tx.Commit(); err != nil {
		return SessionRecord{}, storeError(err)
	}
	session.Tokens = updated.Tokens
	session.AccessExpiresAt = updated.AccessExpiresAt.UTC()
	session.RefreshExpiresAt = updated.RefreshExpiresAt.UTC()
	session.UpdatedAt = completedAt
	return session, nil
}

func (s *PostgresStore) TouchSession(
	ctx context.Context,
	sessionHash Digest,
	now time.Time,
	idleExpiresAt time.Time,
	touchBefore time.Time,
) (SessionRecord, error) {
	if s == nil || s.db == nil || ctx == nil || now.IsZero() || idleExpiresAt.IsZero() || touchBefore.After(now) {
		return SessionRecord{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return SessionRecord{}, storeError(err)
	}
	defer func() { _ = tx.Rollback() }()

	session, err := scanSession(tx.QueryRowContext(ctx, sessionSelect+` WHERE session_hash = $1 FOR UPDATE`, sessionHash[:]))
	if errors.Is(err, sql.ErrNoRows) {
		return SessionRecord{}, ErrSessionNotFound
	}
	if err != nil {
		return SessionRecord{}, storeError(err)
	}
	now = now.UTC()
	if sessionDeadlinePassed(session, now) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM backoffice_sessions WHERE session_hash = $1`, sessionHash[:]); err != nil {
			return SessionRecord{}, storeError(err)
		}
		if err := tx.Commit(); err != nil {
			return SessionRecord{}, storeError(err)
		}
		return SessionRecord{}, ErrSessionExpired
	}
	if !session.LastSeenAt.After(touchBefore) {
		if !idleExpiresAt.After(now) {
			return SessionRecord{}, ErrInvalidInput
		}
		session.LastSeenAt = now
		session.IdleExpiresAt = minTime(idleExpiresAt.UTC(), session.AbsoluteExpiresAt)
		session.UpdatedAt = now
		_, err = tx.ExecContext(ctx, `
			UPDATE backoffice_sessions
			SET last_seen_at = $2,
				idle_expires_at = $3,
				updated_at = $2
			WHERE session_hash = $1
		`, sessionHash[:], session.LastSeenAt, session.IdleExpiresAt)
		if err != nil {
			return SessionRecord{}, storeError(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return SessionRecord{}, storeError(err)
	}
	return session, nil
}

func (s *PostgresStore) DeleteSession(ctx context.Context, sessionHash Digest) (bool, error) {
	if s == nil || s.db == nil || ctx == nil {
		return false, ErrInvalidInput
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM backoffice_sessions WHERE session_hash = $1`, sessionHash[:])
	if err != nil {
		return false, storeError(err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, storeError(err)
	}
	if rows != 0 && rows != 1 {
		return false, ErrInvalidInput
	}
	return rows == 1, nil
}

type CleanupResult struct {
	Flows    int64
	Sessions int64
}

func (s *PostgresStore) DeleteExpired(ctx context.Context, before time.Time) (CleanupResult, error) {
	if s == nil || s.db == nil || ctx == nil || before.IsZero() {
		return CleanupResult{}, ErrInvalidInput
	}
	flows, err := s.db.ExecContext(ctx, `DELETE FROM backoffice_auth_flows WHERE expires_at <= $1`, before.UTC())
	if err != nil {
		return CleanupResult{}, storeError(err)
	}
	sessions, err := s.db.ExecContext(ctx, `
		DELETE FROM backoffice_sessions
		WHERE refresh_expires_at <= $1 OR idle_expires_at <= $1 OR absolute_expires_at <= $1
	`, before.UTC())
	if err != nil {
		return CleanupResult{}, storeError(err)
	}
	flowCount, err := flows.RowsAffected()
	if err != nil {
		return CleanupResult{}, storeError(err)
	}
	sessionCount, err := sessions.RowsAffected()
	if err != nil {
		return CleanupResult{}, storeError(err)
	}
	return CleanupResult{Flows: flowCount, Sessions: sessionCount}, nil
}

const sessionSelect = `
	SELECT session_hash, issuer, subject,
		tokens_key_id, tokens_nonce, tokens_ciphertext,
		access_expires_at, refresh_expires_at, idle_expires_at, absolute_expires_at,
		last_seen_at, created_at, updated_at
	FROM backoffice_sessions`

type rowScanner interface {
	Scan(...any) error
}

func scanFlow(row rowScanner) (FlowRecord, error) {
	var flow FlowRecord
	var stateHash, browserHash, nonceHash []byte
	err := row.Scan(
		&stateHash, &browserHash, &flow.PKCEVerifier.KeyID, &flow.PKCEVerifier.Nonce,
		&flow.PKCEVerifier.Ciphertext, &nonceHash, &flow.ReturnPath, &flow.CreatedAt, &flow.ExpiresAt,
	)
	if err != nil {
		return FlowRecord{}, err
	}
	if !copyDigest(&flow.StateHash, stateHash) || !copyDigest(&flow.BrowserHash, browserHash) ||
		!copyDigest(&flow.NonceHash, nonceHash) || !validFlow(flow) {
		return FlowRecord{}, ErrInvalidInput
	}
	return flow, nil
}

func scanSession(row rowScanner) (SessionRecord, error) {
	var session SessionRecord
	var sessionHash []byte
	err := row.Scan(
		&sessionHash, &session.Issuer, &session.Subject,
		&session.Tokens.KeyID, &session.Tokens.Nonce, &session.Tokens.Ciphertext,
		&session.AccessExpiresAt, &session.RefreshExpiresAt, &session.IdleExpiresAt, &session.AbsoluteExpiresAt,
		&session.LastSeenAt, &session.CreatedAt, &session.UpdatedAt,
	)
	if err != nil {
		return SessionRecord{}, err
	}
	if !copyDigest(&session.SessionHash, sessionHash) || !validSession(session) {
		return SessionRecord{}, ErrInvalidInput
	}
	return session, nil
}

func validFlow(flow FlowRecord) bool {
	return validEnvelope(flow.PKCEVerifier) && flow.ReturnPath != "" && len(flow.ReturnPath) <= 2048 &&
		!flow.CreatedAt.IsZero() && flow.ExpiresAt.After(flow.CreatedAt)
}

func validSession(session SessionRecord) bool {
	return session.Issuer != "" && len(session.Issuer) <= 2048 && session.Subject != "" && len(session.Subject) <= 512 &&
		validEnvelope(session.Tokens) && session.AccessExpiresAt.After(session.CreatedAt) &&
		session.RefreshExpiresAt.After(session.CreatedAt) &&
		session.IdleExpiresAt.After(session.CreatedAt) && !session.IdleExpiresAt.After(session.AbsoluteExpiresAt) &&
		session.AbsoluteExpiresAt.After(session.CreatedAt) && !session.LastSeenAt.Before(session.CreatedAt) &&
		!session.UpdatedAt.Before(session.CreatedAt) && !session.CreatedAt.IsZero()
}

func validEnvelope(envelope Envelope) bool {
	return envelope.KeyID != "" && len(envelope.KeyID) <= 128 && len(envelope.Nonce) == 12 && len(envelope.Ciphertext) >= 16
}

func copyDigest(target *Digest, raw []byte) bool {
	if len(raw) != len(target) {
		return false
	}
	copy(target[:], raw)
	return true
}

func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}

func storeError(err error) error {
	return fmt.Errorf("%w: %w", ErrStoreUnavailable, err)
}
