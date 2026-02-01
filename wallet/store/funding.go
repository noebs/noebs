package store

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
)

func (s *Store) UpsertFundingSource(ctx context.Context, source FundingSource) (*FundingSource, error) {
	if source.TenantID == "" {
		return nil, ErrMissingTenantID
	}
	if source.WalletID == uuid.Nil {
		return nil, ErrMissingWalletID
	}
	if source.SourceType == "" {
		return nil, ErrMissingSourceType
	}
	if source.Currency == "" {
		return nil, ErrMissingCurrency
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}

	stmt := db.Rebind(`INSERT INTO funding_sources(
		tenant_id, wallet_id, source_type, psp_provider, external_reference, verification_status,
		verified_at, verified_by, currency, source_details, total_funded, last_funded_at,
		total_withdrawn, last_withdrawn_at, supports_withdrawal, withdrawal_method
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(tenant_id, wallet_id, source_type, external_reference) DO NOTHING
	RETURNING *`)
	var stored FundingSource
	if err := db.GetContext(ctx, &stored, stmt,
		source.TenantID,
		source.WalletID,
		source.SourceType,
		source.PSPProvider,
		source.ExternalReference,
		source.VerificationStatus,
		source.VerifiedAt,
		source.VerifiedBy,
		source.Currency,
		source.SourceDetails,
		source.TotalFunded,
		source.LastFundedAt,
		source.TotalWithdrawn,
		source.LastWithdrawnAt,
		source.SupportsWithdrawal,
		source.WithdrawalMethod,
	); err != nil {
		if err == sql.ErrNoRows {
			return s.GetFundingSource(ctx, source.TenantID, source.WalletID, source.SourceType, source.ExternalReference)
		}
		return nil, err
	}
	return &stored, nil
}

func (s *Store) GetFundingSource(ctx context.Context, tenantID string, walletID uuid.UUID, sourceType string, externalRef sql.NullString) (*FundingSource, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if walletID == uuid.Nil {
		return nil, ErrMissingWalletID
	}
	if sourceType == "" {
		return nil, ErrMissingSourceType
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}

	query := "SELECT * FROM funding_sources WHERE tenant_id = ? AND wallet_id = ? AND source_type = ?"
	args := []any{tenantID, walletID, sourceType}
	if externalRef.Valid {
		query += " AND external_reference = ?"
		args = append(args, externalRef)
	} else {
		query += " AND external_reference IS NULL"
	}
	stmt := db.Rebind(query)
	var source FundingSource
	if err := db.GetContext(ctx, &source, stmt, args...); err != nil {
		return nil, err
	}
	return &source, nil
}

func (s *Store) ListFundingSources(ctx context.Context, tenantID string, walletID uuid.UUID) ([]FundingSource, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if walletID == uuid.Nil {
		return nil, ErrMissingWalletID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT * FROM funding_sources
		WHERE tenant_id = ? AND wallet_id = ?
		ORDER BY last_funded_at DESC NULLS LAST, created_at DESC`)
	var sources []FundingSource
	if err := db.SelectContext(ctx, &sources, stmt, tenantID, walletID); err != nil {
		return nil, err
	}
	return sources, nil
}

func (s *Store) CreateFundingLink(ctx context.Context, link LedgerFundingLink) (*LedgerFundingLink, error) {
	if link.TenantID == "" {
		return nil, ErrMissingTenantID
	}
	if link.LedgerEntryID <= 0 {
		return nil, ErrMissingLedgerEntryID
	}
	if link.FundingSourceID <= 0 {
		return nil, ErrMissingFundingSourceID
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
	stmt := db.Rebind(`INSERT INTO ledger_funding_links(
		tenant_id, ledger_entry_id, funding_source_id, amount, currency
	) VALUES(?, ?, ?, ?, ?)
	RETURNING *`)
	var stored LedgerFundingLink
	if err := db.GetContext(ctx, &stored, stmt,
		link.TenantID,
		link.LedgerEntryID,
		link.FundingSourceID,
		link.Amount,
		link.Currency,
	); err != nil {
		return nil, err
	}
	return &stored, nil
}

func (s *Store) GetFundingSourceByPSPRef(ctx context.Context, tenantID, provider string, externalRef string) (*FundingSource, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if provider == "" {
		return nil, ErrMissingProviderCode
	}
	if externalRef == "" {
		return nil, ErrMissingReferenceID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT * FROM funding_sources
		WHERE tenant_id = ? AND psp_provider = ? AND external_reference = ?
		ORDER BY created_at DESC
		LIMIT 1`)
	var source FundingSource
	if err := db.GetContext(ctx, &source, stmt, tenantID, provider, externalRef); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrFundingSourceNotFound
		}
		return nil, err
	}
	return &source, nil
}
