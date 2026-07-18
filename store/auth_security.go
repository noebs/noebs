package store

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const (
	OTPChallengePurposeSignIn           = "signin"
	OTPChallengePurposePasswordRecovery = "password_recovery"
)

// AuthRateLimitResult describes the fixed window after recording one attempt.
type AuthRateLimitResult struct {
	Count   int
	ResetAt time.Time
}

// RecordAuthAttempt atomically records one attempt in an explicit fixed window.
func (s *Store) RecordAuthAttempt(ctx context.Context, tenantID, action, subjectHash string, now time.Time, window time.Duration) (AuthRateLimitResult, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return AuthRateLimitResult{}, err
	}
	action = strings.TrimSpace(action)
	if action == "" {
		return AuthRateLimitResult{}, ErrMissingAuthAction
	}
	if err := validateSHA256Digest(subjectHash, ErrMissingAuthSubject); err != nil {
		return AuthRateLimitResult{}, err
	}
	if now.IsZero() {
		return AuthRateLimitResult{}, ErrInvalidAuthTime
	}
	if window <= 0 {
		return AuthRateLimitResult{}, ErrInvalidAuthWindow
	}
	db, err := s.ensureDB()
	if err != nil {
		return AuthRateLimitResult{}, err
	}

	now = now.UTC()
	cutoff := now.Add(-window)
	pruneBefore := now.Add(-24 * time.Hour)
	stmt := s.DB.Rebind(`WITH pruned AS (
		DELETE FROM auth_rate_limits WHERE updated_at < ?
	)
	INSERT INTO auth_rate_limits(
		tenant_id, action, subject_hash, attempt_count, window_started_at, updated_at
	) VALUES(?, ?, ?, 1, ?, ?)
	ON CONFLICT(tenant_id, action, subject_hash) DO UPDATE SET
		attempt_count = CASE
			WHEN auth_rate_limits.window_started_at <= ? OR auth_rate_limits.window_started_at > ? THEN 1
			ELSE auth_rate_limits.attempt_count + 1
		END,
		window_started_at = CASE
			WHEN auth_rate_limits.window_started_at <= ? OR auth_rate_limits.window_started_at > ? THEN EXCLUDED.window_started_at
			ELSE auth_rate_limits.window_started_at
		END,
		updated_at = EXCLUDED.updated_at
	RETURNING attempt_count, window_started_at`)
	var result AuthRateLimitResult
	var windowStartedAt time.Time
	if err := db.QueryRowContext(ctx, stmt,
		pruneBefore,
		tenantID, action, subjectHash, now, now,
		cutoff, now,
		cutoff, now,
	).Scan(&result.Count, &windowStartedAt); err != nil {
		return AuthRateLimitResult{}, err
	}
	result.ResetAt = windowStartedAt.UTC().Add(window)
	return result, nil
}

// StoreOTPChallenge replaces any earlier challenge for the same tenant and mobile.
func (s *Store) StoreOTPChallenge(ctx context.Context, tenantID, mobile, codeDigest string, now, expiresAt time.Time, maxAttempts int) error {
	return s.StoreOTPChallengeForPurpose(ctx, tenantID, mobile, OTPChallengePurposeSignIn, codeDigest, now, expiresAt, maxAttempts)
}

// StoreOTPChallengeForPurpose replaces the challenge for one tenant, mobile,
// and purpose without invalidating a challenge from another authentication flow.
func (s *Store) StoreOTPChallengeForPurpose(ctx context.Context, tenantID, mobile, purpose, codeDigest string, now, expiresAt time.Time, maxAttempts int) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	purpose, err = validateOTPChallengePurpose(purpose)
	if err != nil {
		return err
	}
	if err := validateSHA256Digest(codeDigest, ErrMissingOTPDigest); err != nil {
		return err
	}
	if now.IsZero() {
		return ErrInvalidAuthTime
	}
	if !expiresAt.After(now) {
		return ErrInvalidOTPExpiry
	}
	if maxAttempts <= 0 {
		return ErrInvalidOTPMaxAttempts
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}

	now = now.UTC()
	stmt := s.DB.Rebind(`INSERT INTO otp_challenges(
		tenant_id, mobile, purpose, code_digest, expires_at, attempts, max_attempts, consumed_at, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, 0, ?, NULL, ?, ?)
	ON CONFLICT(tenant_id, mobile, purpose) DO UPDATE SET
		code_digest = EXCLUDED.code_digest,
		expires_at = EXCLUDED.expires_at,
		attempts = 0,
		max_attempts = EXCLUDED.max_attempts,
		consumed_at = NULL,
		created_at = EXCLUDED.created_at,
		updated_at = EXCLUDED.updated_at`)
	_, err = db.ExecContext(ctx, stmt, tenantID, mobile, purpose, codeDigest, expiresAt.UTC(), maxAttempts, now, now)
	return err
}

// ConsumeOTPChallenge validates and consumes a challenge under a row lock.
func (s *Store) ConsumeOTPChallenge(ctx context.Context, tenantID, mobile, codeDigest string, now time.Time) error {
	return s.ConsumeOTPChallengeForPurpose(ctx, tenantID, mobile, OTPChallengePurposeSignIn, codeDigest, now)
}

// ConsumeOTPChallengeForPurpose validates and consumes exactly one scoped
// challenge under a row lock.
func (s *Store) ConsumeOTPChallengeForPurpose(ctx context.Context, tenantID, mobile, purpose, codeDigest string, now time.Time) error {
	return s.consumeOTPChallengeForPurpose(ctx, tenantID, mobile, purpose, codeDigest, now, nil)
}

// ConsumeSignInChallengeAndVerifyUser commits challenge consumption and the
// verified identity transition together, before a caller can issue a session.
func (s *Store) ConsumeSignInChallengeAndVerifyUser(ctx context.Context, tenantID, mobile, codeDigest string, userID int64, now time.Time) error {
	var err error
	tenantID, err = ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	return s.consumeOTPChallengeForPurpose(ctx, tenantID, mobile, OTPChallengePurposeSignIn, codeDigest, now, func(tx sqlExecer) error {
		stmt := s.DB.Rebind(`UPDATE users
			SET is_verified = TRUE, is_password_otp = TRUE, updated_at = ?
			WHERE tenant_id = ? AND id = ? AND mobile = ? AND deleted_at IS NULL`)
		return execContextRequireRowsAffected(ctx, tx, stmt, now.UTC(), tenantID, userID, mobile)
	})
}

func (s *Store) consumeOTPChallengeForPurpose(ctx context.Context, tenantID, mobile, purpose, codeDigest string, now time.Time, onSuccess func(sqlExecer) error) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	purpose, err = validateOTPChallengePurpose(purpose)
	if err != nil {
		return err
	}
	if err := validateSHA256Digest(codeDigest, ErrMissingOTPDigest); err != nil {
		return err
	}
	if now.IsZero() {
		return ErrInvalidAuthTime
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}

	now = now.UTC()
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var storedDigest string
	var expiresAt time.Time
	var attempts, maxAttempts int
	var consumedAt sql.NullTime
	selectStmt := s.DB.Rebind(`SELECT code_digest, expires_at, attempts, max_attempts, consumed_at
		FROM otp_challenges WHERE tenant_id = ? AND mobile = ? AND purpose = ? FOR UPDATE`)
	if err := tx.QueryRowContext(ctx, selectStmt, tenantID, mobile, purpose).Scan(
		&storedDigest, &expiresAt, &attempts, &maxAttempts, &consumedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrOTPChallengeNotFound
		}
		return err
	}
	if consumedAt.Valid {
		return ErrOTPChallengeConsumed
	}
	if !now.Before(expiresAt) {
		return ErrOTPChallengeExpired
	}
	if attempts >= maxAttempts {
		return ErrOTPAttemptsExceeded
	}

	if subtle.ConstantTimeCompare([]byte(storedDigest), []byte(codeDigest)) != 1 {
		updateStmt := s.DB.Rebind(`UPDATE otp_challenges
			SET attempts = attempts + 1, updated_at = ?
			WHERE tenant_id = ? AND mobile = ? AND purpose = ?`)
		if _, err := tx.ExecContext(ctx, updateStmt, now, tenantID, mobile, purpose); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return ErrInvalidOTPChallenge
	}

	consumeStmt := s.DB.Rebind(`UPDATE otp_challenges
		SET consumed_at = ?, updated_at = ?
		WHERE tenant_id = ? AND mobile = ? AND purpose = ?`)
	if _, err := tx.ExecContext(ctx, consumeStmt, now, now, tenantID, mobile, purpose); err != nil {
		return err
	}
	if onSuccess != nil {
		if err := onSuccess(tx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteOTPChallenge(ctx context.Context, tenantID, mobile string) error {
	return s.DeleteOTPChallengeForPurpose(ctx, tenantID, mobile, OTPChallengePurposeSignIn)
}

func (s *Store) DeleteOTPChallengeForPurpose(ctx context.Context, tenantID, mobile, purpose string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	purpose, err = validateOTPChallengePurpose(purpose)
	if err != nil {
		return err
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("DELETE FROM otp_challenges WHERE tenant_id = ? AND mobile = ? AND purpose = ?")
	_, err = db.ExecContext(ctx, stmt, tenantID, mobile, purpose)
	return err
}

// ConsumeRefreshToken records a refresh token hash exactly once.
func (s *Store) ConsumeRefreshToken(ctx context.Context, tenantID string, userID int64, tokenHash string, now, expiresAt time.Time) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	if err := validateSHA256Digest(tokenHash, ErrMissingRefreshHash); err != nil {
		return err
	}
	if now.IsZero() {
		return ErrInvalidAuthTime
	}
	if !expiresAt.After(now) {
		return ErrInvalidRefreshExpiry
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}

	now = now.UTC()
	stmt := s.DB.Rebind(`WITH pruned AS (
		DELETE FROM used_refresh_tokens WHERE expires_at <= ?
	)
	INSERT INTO used_refresh_tokens(tenant_id, token_hash, user_id, expires_at, consumed_at)
	VALUES(?, ?, ?, ?, ?)
	ON CONFLICT(tenant_id, token_hash) DO NOTHING`)
	result, err := db.ExecContext(ctx, stmt, now, tenantID, tokenHash, userID, expiresAt.UTC(), now)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		return ErrRefreshTokenReplay
	}
	return nil
}

func validateSHA256Digest(value string, missing error) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return missing
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return missing
	}
	return nil
}

func validateOTPChallengePurpose(purpose string) (string, error) {
	purpose = strings.TrimSpace(purpose)
	switch purpose {
	case OTPChallengePurposeSignIn, OTPChallengePurposePasswordRecovery:
		return purpose, nil
	default:
		return "", ErrInvalidOTPChallenge
	}
}
