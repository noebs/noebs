package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) CreateOrResetUserTwoFA(ctx context.Context, tenantID string, userID int64, secret string) (*UserTwoFA, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, ErrInvalidUserID
	}
	if secret == "" {
		return nil, ErrMissingTwoFASecret
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	stmt := db.Rebind(`INSERT INTO wallet_user_2fa(
		tenant_id, user_id, secret, enabled, created_at, updated_at, enabled_at, disabled_at
	) VALUES(?, ?, ?, FALSE, ?, ?, NULL, NULL)
	ON CONFLICT(tenant_id, user_id) DO UPDATE
		SET secret = EXCLUDED.secret,
			enabled = FALSE,
			updated_at = EXCLUDED.updated_at,
			enabled_at = NULL,
			disabled_at = NULL,
			last_used_at = NULL
		WHERE wallet_user_2fa.enabled = FALSE
	RETURNING *`)
	var stored UserTwoFA
	if err := db.GetContext(ctx, &stored, stmt, tenantID, userID, secret, now, now); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserTwoFAAlreadyEnabled
		}
		return nil, err
	}
	return &stored, nil
}

func (s *Store) GetUserTwoFA(ctx context.Context, tenantID string, userID int64) (*UserTwoFA, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, ErrInvalidUserID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind("SELECT * FROM wallet_user_2fa WHERE tenant_id = ? AND user_id = ?")
	var record UserTwoFA
	if err := db.GetContext(ctx, &record, stmt, tenantID, userID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrUserTwoFANotFound
		}
		return nil, err
	}
	return &record, nil
}

func (s *Store) SetUserTwoFAEnabled(ctx context.Context, tenantID string, userID int64, enabled bool, changedAt time.Time) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	if changedAt.IsZero() {
		return ErrMissingUpdatedAt
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	var enabledAt sql.NullTime
	var disabledAt sql.NullTime
	if enabled {
		enabledAt = sql.NullTime{Time: changedAt, Valid: true}
		disabledAt = sql.NullTime{}
	} else {
		enabledAt = sql.NullTime{}
		disabledAt = sql.NullTime{Time: changedAt, Valid: true}
	}
	stmt := db.Rebind(`UPDATE wallet_user_2fa
		SET enabled = ?, updated_at = ?, enabled_at = ?, disabled_at = ?
		WHERE tenant_id = ? AND user_id = ?`)
	result, err := db.ExecContext(ctx, stmt, enabled, changedAt, enabledAt, disabledAt, tenantID, userID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrUserTwoFANotFound
	}
	return nil
}

func (s *Store) TouchUserTwoFALastUsed(ctx context.Context, tenantID string, userID int64, usedAt time.Time) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	if usedAt.IsZero() {
		return ErrMissingUsageTime
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := db.Rebind(`UPDATE wallet_user_2fa
		SET last_used_at = ?, updated_at = ?
		WHERE tenant_id = ? AND user_id = ? AND enabled = TRUE`)
	result, err := db.ExecContext(ctx, stmt, usedAt, usedAt, tenantID, userID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		record, err := s.GetUserTwoFA(ctx, tenantID, userID)
		if err != nil {
			return err
		}
		if !record.Enabled {
			return ErrUserTwoFANotEnabled
		}
		return ErrUserTwoFANotFound
	}
	return nil
}
