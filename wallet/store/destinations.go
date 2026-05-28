package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

func (s *Store) CreateWithdrawalDestination(ctx context.Context, dest WithdrawalDestination) (*WithdrawalDestination, error) {
	tenantID, err := ValidateTenantID(dest.TenantID)
	if err != nil {
		return nil, err
	}
	if dest.WalletID == uuid.Nil {
		return nil, ErrMissingWalletID
	}
	if dest.DestinationType == "" {
		return nil, ErrMissingDestinationType
	}
	if dest.Currency == "" {
		return nil, ErrMissingCurrency
	}
	if len(dest.DestinationDetails) == 0 {
		return nil, ErrMissingDestinationDetails
	}
	if dest.OwnershipStatus == "" {
		return nil, ErrMissingStatus
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	stmt := db.Rebind(`INSERT INTO withdrawal_destinations(
		tenant_id, wallet_id, destination_type, psp_provider, destination_details, display_name,
		currency, country, ownership_status, ownership_verification_method, ownership_verified_at,
		ownership_verified_by, ownership_proof, linked_funding_source_id, is_return_to_source,
		is_active, last_used_at, total_withdrawn, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	RETURNING *`)
	var stored WithdrawalDestination
	if err := db.GetContext(ctx, &stored, stmt,
		tenantID,
		dest.WalletID,
		dest.DestinationType,
		dest.PSPProvider,
		dest.DestinationDetails,
		dest.DisplayName,
		dest.Currency,
		dest.Country,
		dest.OwnershipStatus,
		dest.OwnershipVerificationMethod,
		dest.OwnershipVerifiedAt,
		dest.OwnershipVerifiedBy,
		dest.OwnershipProof,
		dest.LinkedFundingSourceID,
		dest.IsReturnToSource,
		dest.IsActive,
		dest.LastUsedAt,
		dest.TotalWithdrawn,
		now,
		now,
	); err != nil {
		return nil, err
	}
	return &stored, nil
}

func (s *Store) GetWithdrawalDestination(ctx context.Context, tenantID string, destinationID int64) (*WithdrawalDestination, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if destinationID <= 0 {
		return nil, ErrMissingDestinationID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind("SELECT * FROM withdrawal_destinations WHERE tenant_id = ? AND id = ?")
	var dest WithdrawalDestination
	if err := db.GetContext(ctx, &dest, stmt, tenantID, destinationID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrDestinationNotFound
		}
		return nil, err
	}
	return &dest, nil
}

func (s *Store) ListWithdrawalDestinations(ctx context.Context, tenantID string, walletID uuid.UUID, activeOnly bool) ([]WithdrawalDestination, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if walletID == uuid.Nil {
		return nil, ErrMissingWalletID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	query := "SELECT * FROM withdrawal_destinations WHERE tenant_id = ? AND wallet_id = ?"
	args := []any{tenantID, walletID}
	if activeOnly {
		query += " AND is_active = TRUE"
	}
	query += " ORDER BY updated_at DESC"
	stmt := db.Rebind(query)
	var dests []WithdrawalDestination
	if err := db.SelectContext(ctx, &dests, stmt, args...); err != nil {
		return nil, err
	}
	return dests, nil
}

func (s *Store) UpdateWithdrawalDestinationUsage(ctx context.Context, tenantID string, destinationID int64, amount int64, usedAt time.Time) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if destinationID <= 0 {
		return ErrMissingDestinationID
	}
	if amount <= 0 {
		return ErrInvalidAmount
	}
	if usedAt.IsZero() {
		return ErrMissingUsageTime
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := db.Rebind(`UPDATE withdrawal_destinations
		SET last_used_at = ?, total_withdrawn = total_withdrawn + ?, updated_at = ?
		WHERE tenant_id = ? AND id = ?`)
	result, err := db.ExecContext(ctx, stmt, usedAt, amount, usedAt, tenantID, destinationID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrDestinationNotFound
	}
	return nil
}

func (s *Store) UpdateWithdrawalDestinationOwnership(ctx context.Context, tenantID string, destinationID int64, status string, verifiedAt sql.NullTime, updatedAt time.Time) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if destinationID <= 0 {
		return ErrMissingDestinationID
	}
	if status == "" {
		return ErrMissingStatus
	}
	if updatedAt.IsZero() {
		return ErrMissingUpdatedAt
	}
	if status == "verified" && !verifiedAt.Valid {
		return ErrMissingVerificationTime
	}
	if status != "verified" {
		verifiedAt = sql.NullTime{}
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := db.Rebind(`UPDATE withdrawal_destinations
		SET ownership_status = ?, ownership_verified_at = ?, updated_at = ?
		WHERE tenant_id = ? AND id = ?`)
	result, err := db.ExecContext(ctx, stmt, status, verifiedAt, updatedAt, tenantID, destinationID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrDestinationNotFound
	}
	return nil
}

func (s *Store) DeactivateWithdrawalDestination(ctx context.Context, tenantID string, destinationID int64, updatedAt time.Time) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if destinationID <= 0 {
		return ErrMissingDestinationID
	}
	if updatedAt.IsZero() {
		return ErrMissingUpdatedAt
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := db.Rebind(`UPDATE withdrawal_destinations
		SET is_active = FALSE, updated_at = ?
		WHERE tenant_id = ? AND id = ?`)
	result, err := db.ExecContext(ctx, stmt, updatedAt, tenantID, destinationID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrDestinationNotFound
	}
	return nil
}
