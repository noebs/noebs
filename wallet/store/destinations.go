package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

func (s *Store) CreateWithdrawalDestination(ctx context.Context, dest WithdrawalDestination) (*WithdrawalDestination, error) {
	if dest.TenantID == "" {
		return nil, ErrMissingTenantID
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
	if _, err := s.ensureDB(); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	stmt := s.DB.Rebind(`INSERT INTO withdrawal_destinations(
		tenant_id, wallet_id, destination_type, psp_provider, destination_details, display_name,
		currency, country, ownership_status, ownership_verification_method, ownership_verified_at,
		ownership_verified_by, ownership_proof, linked_funding_source_id, is_return_to_source,
		is_active, last_used_at, total_withdrawn, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	RETURNING *`)
	var stored WithdrawalDestination
	if err := s.DB.GetContext(ctx, &stored, stmt,
		dest.TenantID,
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
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if destinationID <= 0 {
		return nil, ErrMissingDestinationID
	}
	if _, err := s.ensureDB(); err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM withdrawal_destinations WHERE tenant_id = ? AND id = ?")
	var dest WithdrawalDestination
	if err := s.DB.GetContext(ctx, &dest, stmt, tenantID, destinationID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrDestinationNotFound
		}
		return nil, err
	}
	return &dest, nil
}

func (s *Store) ListWithdrawalDestinations(ctx context.Context, tenantID string, walletID uuid.UUID, activeOnly bool) ([]WithdrawalDestination, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if walletID == uuid.Nil {
		return nil, ErrMissingWalletID
	}
	if _, err := s.ensureDB(); err != nil {
		return nil, err
	}
	query := "SELECT * FROM withdrawal_destinations WHERE tenant_id = ? AND wallet_id = ?"
	args := []any{tenantID, walletID}
	if activeOnly {
		query += " AND is_active = TRUE"
	}
	query += " ORDER BY updated_at DESC"
	stmt := s.DB.Rebind(query)
	var dests []WithdrawalDestination
	if err := s.DB.SelectContext(ctx, &dests, stmt, args...); err != nil {
		return nil, err
	}
	return dests, nil
}
