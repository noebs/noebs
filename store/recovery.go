package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

var (
	ErrMissingRecoveryCredential = errors.New("missing recovery credential")
	ErrInvalidRecoveryCredential = errors.New("invalid recovery credential")
	ErrInvalidRecoveryExpiry     = errors.New("invalid recovery expiry")
	ErrSessionRevoked            = errors.New("session revoked")
)

// StorePasswordRecoveryCredential stores only a digest of an opaque,
// short-lived credential. The raw credential must never be persisted.
func (s *Store) StorePasswordRecoveryCredential(ctx context.Context, tenantID, tokenHash string, userID int64, now, expiresAt time.Time) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if err := validateSHA256Digest(tokenHash, ErrMissingRecoveryCredential); err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	if now.IsZero() {
		return ErrInvalidAuthTime
	}
	if !expiresAt.After(now) {
		return ErrInvalidRecoveryExpiry
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}

	now = now.UTC()
	stmt := s.DB.Rebind(`WITH pruned AS (
		DELETE FROM password_recovery_credentials WHERE expires_at <= ?
	)
	INSERT INTO password_recovery_credentials(
		tenant_id, token_hash, user_id, expires_at, consumed_at, created_at, updated_at
	) VALUES(?, ?, ?, ?, NULL, ?, ?)`)
	_, err = db.ExecContext(ctx, stmt, now, tenantID, tokenHash, userID, expiresAt.UTC(), now, now)
	return err
}

// ResetIdentityWithRecoveryCredential consumes the credential and rotates the
// password and device public key in one transaction. Every outstanding
// credential and recovery challenge for the account is invalidated on success.
func (s *Store) ResetIdentityWithRecoveryCredential(ctx context.Context, tenantID, tokenHash, passwordHash, publicKey string, now time.Time) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if err := validateSHA256Digest(tokenHash, ErrMissingRecoveryCredential); err != nil {
		return err
	}
	passwordHash = strings.TrimSpace(passwordHash)
	if passwordHash == "" {
		return ErrMissingPassword
	}
	publicKey = strings.TrimSpace(publicKey)
	if publicKey == "" {
		return ErrMissingData
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

	var userID int64
	findUserStmt := s.DB.Rebind(`SELECT user_id
		FROM password_recovery_credentials
		WHERE tenant_id = ? AND token_hash = ?`)
	if err := tx.QueryRowContext(ctx, findUserStmt, tenantID, tokenHash).Scan(&userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidRecoveryCredential
		}
		return err
	}

	var mobile string
	lockUserStmt := s.DB.Rebind(`SELECT mobile FROM users
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL AND is_verified = TRUE
		FOR UPDATE`)
	if err := tx.QueryRowContext(ctx, lockUserStmt, tenantID, userID).Scan(&mobile); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidRecoveryCredential
		}
		return err
	}

	var expiresAt time.Time
	var consumedAt sql.NullTime
	lockCredentialStmt := s.DB.Rebind(`SELECT expires_at, consumed_at
		FROM password_recovery_credentials
		WHERE tenant_id = ? AND token_hash = ? AND user_id = ?
		FOR UPDATE`)
	if err := tx.QueryRowContext(ctx, lockCredentialStmt, tenantID, tokenHash, userID).Scan(&expiresAt, &consumedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidRecoveryCredential
		}
		return err
	}
	if consumedAt.Valid || !now.Before(expiresAt) {
		return ErrInvalidRecoveryCredential
	}

	updateUserStmt := s.DB.Rebind(`UPDATE users
		SET password = ?, public_key = ?, device_id = '', device_token = '',
			is_password_otp = FALSE, session_epoch = session_epoch + 1, updated_at = ?
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL AND is_verified = TRUE`)
	result, err := tx.ExecContext(ctx, updateUserStmt, passwordHash, publicKey, now, tenantID, userID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return ErrInvalidRecoveryCredential
	}

	consumeStmt := s.DB.Rebind(`UPDATE password_recovery_credentials
		SET consumed_at = ?, updated_at = ?
		WHERE tenant_id = ? AND user_id = ? AND consumed_at IS NULL`)
	if _, err := tx.ExecContext(ctx, consumeStmt, now, now, tenantID, userID); err != nil {
		return err
	}
	deleteChallengeStmt := s.DB.Rebind(`DELETE FROM otp_challenges
		WHERE tenant_id = ? AND mobile = ? AND purpose = ?`)
	if _, err := tx.ExecContext(ctx, deleteChallengeStmt, tenantID, mobile, OTPChallengePurposePasswordRecovery); err != nil {
		return err
	}
	return tx.Commit()
}

// ValidateSessionEpoch rejects sessions issued before the account's most
// recent security reset.
func (s *Store) ValidateSessionEpoch(ctx context.Context, tenantID string, userID, sessionEpoch int64) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 || sessionEpoch <= 0 {
		return ErrSessionRevoked
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind(`SELECT session_epoch FROM users
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL AND is_verified = TRUE`)
	var currentEpoch int64
	if err := db.QueryRowContext(ctx, stmt, tenantID, userID).Scan(&currentEpoch); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSessionRevoked
		}
		return err
	}
	if currentEpoch != sessionEpoch {
		return ErrSessionRevoked
	}
	return nil
}

// ResumeUnverifiedRegistration rotates credentials only while an account is
// still unverified. A verified account follows the same successful no-op path,
// so callers do not learn account verification state.
func (s *Store) ResumeUnverifiedRegistration(ctx context.Context, tenantID, mobile, passwordHash, publicKey string, now time.Time) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	passwordHash = strings.TrimSpace(passwordHash)
	if passwordHash == "" {
		return ErrMissingPassword
	}
	publicKey = strings.TrimSpace(publicKey)
	if publicKey == "" {
		return ErrMissingData
	}
	if now.IsZero() {
		return ErrInvalidAuthTime
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind(`UPDATE users
		SET password = ?, public_key = ?,
			is_merchant = FALSE, is_password_otp = FALSE, is_verified = FALSE,
			device_id = '', device_token = '', otp = '', signed_otp = '',
			main_card = '', main_card_enc = '', main_expdate = '',
			session_epoch = session_epoch + 1, updated_at = ?
		WHERE tenant_id = ? AND mobile = ? AND deleted_at IS NULL AND is_verified = FALSE`)
	_, err = db.ExecContext(ctx, stmt, passwordHash, publicKey, now.UTC(), tenantID, mobile)
	return err
}
