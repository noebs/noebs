package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (s *Store) CreateWithdrawalDestination(ctx context.Context, dest WithdrawalDestination) (*WithdrawalDestination, error) {
	tenantID, err := ValidateTenantID(dest.TenantID)
	if err != nil {
		return nil, err
	}
	dest.TenantID = tenantID
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
	if err := ValidateWithdrawalDestinationOwnership(dest); err != nil {
		return nil, err
	}
	if dest.IsReturnToSource && !dest.LinkedFundingSourceID.Valid {
		return nil, ErrMissingFundingSourceID
	}
	if dest.TotalWithdrawn != 0 {
		return nil, ErrInvalidAmount
	}
	if dest.LastUsedAt.Valid {
		return nil, ErrInvalidUsageTime
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	if dest.LinkedFundingSourceID.Valid {
		source, err := s.GetFundingSourceByID(ctx, tenantID, dest.LinkedFundingSourceID.Int64)
		if err != nil {
			return nil, err
		}
		if err := ValidateWithdrawalDestinationFundingSource(dest, source); err != nil {
			return nil, err
		}
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

func ValidateWithdrawalDestinationFundingSource(dest WithdrawalDestination, source *FundingSource) error {
	if !dest.LinkedFundingSourceID.Valid {
		if dest.IsReturnToSource {
			return ErrMissingFundingSourceID
		}
		return nil
	}
	if source == nil ||
		source.TenantID != dest.TenantID ||
		source.ID != dest.LinkedFundingSourceID.Int64 {
		return ErrFundingSourceNotFound
	}
	if source.WalletID != dest.WalletID {
		return ErrFundingSourceNotFound
	}
	if source.Currency != dest.Currency {
		return ErrCurrencyMismatch
	}
	if err := ValidateFundingSourceReadyForWithdrawal(source); err != nil {
		return err
	}
	return nil
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

func (s *Store) CreateWithdrawalDestinationLink(ctx context.Context, link LedgerWithdrawalDestinationLink) (*LedgerWithdrawalDestinationLink, error) {
	tenantID, err := ValidateTenantID(link.TenantID)
	if err != nil {
		return nil, err
	}
	link.TenantID = tenantID
	if link.LedgerEntryID <= 0 {
		return nil, ErrMissingLedgerEntryID
	}
	if link.DestinationID <= 0 {
		return nil, ErrMissingDestinationID
	}
	if link.Amount <= 0 {
		return nil, ErrInvalidAmount
	}
	if link.Currency == "" {
		return nil, ErrMissingCurrency
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	ledgerEntry, err := getLedgerEntryForUsageLinkTx(ctx, tx, tenantID, link.LedgerEntryID)
	if err != nil {
		return nil, err
	}
	destination, err := getWithdrawalDestinationForLinkTx(ctx, tx, tenantID, link.DestinationID)
	if err != nil {
		return nil, err
	}
	if err := ValidateWithdrawalDestinationLinkLedgerEntry(ledgerEntry, destination, link); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	stmt := tx.Rebind(`INSERT INTO ledger_withdrawal_destination_links(
		tenant_id, ledger_entry_id, destination_id, amount, currency, created_at
	) VALUES(?, ?, ?, ?, ?, ?)
	ON CONFLICT(tenant_id, ledger_entry_id, destination_id) DO NOTHING
	RETURNING *`)
	var stored LedgerWithdrawalDestinationLink
	if err := tx.GetContext(ctx, &stored, stmt,
		tenantID,
		link.LedgerEntryID,
		link.DestinationID,
		link.Amount,
		link.Currency,
		now,
	); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		existing, err := getWithdrawalDestinationLinkTx(ctx, tx, tenantID, link.LedgerEntryID, link.DestinationID)
		if err != nil {
			return nil, err
		}
		if err := ValidateWithdrawalDestinationLinkReplay(existing, link); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		committed = true
		return existing, nil
	}

	updateStmt := tx.Rebind(`UPDATE withdrawal_destinations
		SET last_used_at = GREATEST(COALESCE(last_used_at, ?), ?),
			total_withdrawn = total_withdrawn + ?,
			updated_at = ?
		WHERE tenant_id = ? AND id = ?`)
	result, err := tx.ExecContext(ctx, updateStmt, now, now, link.Amount, now, tenantID, link.DestinationID)
	if err != nil {
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, ErrDestinationNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return &stored, nil
}

func getWithdrawalDestinationForLinkTx(ctx context.Context, tx interface {
	Rebind(string) string
	GetContext(context.Context, any, string, ...any) error
}, tenantID string, destinationID int64) (*WithdrawalDestination, error) {
	stmt := tx.Rebind(`SELECT * FROM withdrawal_destinations
		WHERE tenant_id = ? AND id = ?`)
	var destination WithdrawalDestination
	if err := tx.GetContext(ctx, &destination, stmt, tenantID, destinationID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrDestinationNotFound
		}
		return nil, err
	}
	return &destination, nil
}

func getWithdrawalDestinationLinkTx(ctx context.Context, tx interface {
	Rebind(string) string
	GetContext(context.Context, any, string, ...any) error
}, tenantID string, ledgerEntryID, destinationID int64) (*LedgerWithdrawalDestinationLink, error) {
	stmt := tx.Rebind(`SELECT * FROM ledger_withdrawal_destination_links
		WHERE tenant_id = ? AND ledger_entry_id = ? AND destination_id = ?`)
	var link LedgerWithdrawalDestinationLink
	if err := tx.GetContext(ctx, &link, stmt, tenantID, ledgerEntryID, destinationID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrDuplicateDestinationLink
		}
		return nil, err
	}
	return &link, nil
}

func ValidateWithdrawalDestinationLinkLedgerEntry(entry *LedgerEntry, destination *WithdrawalDestination, link LedgerWithdrawalDestinationLink) error {
	if entry == nil {
		return ErrLedgerEntryNotFound
	}
	if destination == nil {
		return ErrDestinationNotFound
	}
	if entry.TenantID != link.TenantID || entry.ID != link.LedgerEntryID ||
		destination.TenantID != link.TenantID || destination.ID != link.DestinationID {
		return ErrDuplicateDestinationLink
	}
	if destination.WalletID != entry.WalletID {
		return ErrDestinationNotFound
	}
	if !destination.IsActive {
		return ErrDestinationNotFound
	}
	if err := ValidateWithdrawalDestinationReadyForWithdrawal(destination); err != nil {
		return err
	}
	if entry.EntryType != "debit" {
		return ErrInvalidDirection
	}
	if entry.Amount != link.Amount {
		return ErrInvalidAmount
	}
	if entry.Currency != link.Currency {
		return ErrCurrencyMismatch
	}
	if destination.Currency != link.Currency {
		return ErrCurrencyMismatch
	}
	return nil
}

func ValidateWithdrawalDestinationLinkReplay(existing *LedgerWithdrawalDestinationLink, requested LedgerWithdrawalDestinationLink) error {
	if existing == nil ||
		existing.TenantID != requested.TenantID ||
		existing.LedgerEntryID != requested.LedgerEntryID ||
		existing.DestinationID != requested.DestinationID ||
		existing.Amount != requested.Amount ||
		existing.Currency != requested.Currency {
		return ErrDuplicateDestinationLink
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
	if updatedAt.IsZero() {
		return ErrMissingUpdatedAt
	}
	updated := WithdrawalDestination{
		OwnershipStatus:     status,
		OwnershipVerifiedAt: verifiedAt,
	}
	if err := ValidateWithdrawalDestinationOwnership(updated); err != nil {
		return err
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
