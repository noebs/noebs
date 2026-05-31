package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

func (s *Store) UpsertFundingSource(ctx context.Context, source FundingSource) (*FundingSource, error) {
	tenantID, err := ValidateTenantID(source.TenantID)
	if err != nil {
		return nil, err
	}
	source.TenantID = tenantID
	if source.WalletID == uuid.Nil {
		return nil, ErrMissingWalletID
	}
	if source.SourceType == "" {
		return nil, ErrMissingSourceType
	}
	if source.VerificationStatus == "" {
		return nil, ErrMissingStatus
	}
	if source.Currency == "" {
		return nil, ErrMissingCurrency
	}
	if len(source.SourceDetails) == 0 {
		return nil, ErrMissingSourceDetails
	}
	if source.TotalFunded != 0 || source.TotalWithdrawn != 0 {
		return nil, ErrInvalidAmount
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()

	if existing, err := s.GetFundingSource(ctx, tenantID, source.WalletID, source.SourceType, source.ExternalReference); err == nil {
		if err := ValidateFundingSourceMerge(existing, source); err != nil {
			return nil, err
		}
		return s.updateFundingSourceMetadata(ctx, existing.ID, source, now)
	} else if !errors.Is(err, ErrFundingSourceNotFound) {
		return nil, err
	}

	stmt := db.Rebind(`INSERT INTO funding_sources(
		tenant_id, wallet_id, source_type, psp_provider, external_reference, verification_status,
		verified_at, verified_by, currency, source_details, total_funded, last_funded_at,
		total_withdrawn, last_withdrawn_at, supports_withdrawal, withdrawal_method, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	RETURNING *`)
	var stored FundingSource
	if err := db.GetContext(ctx, &stored, stmt,
		tenantID,
		source.WalletID,
		source.SourceType,
		source.PSPProvider,
		source.ExternalReference,
		source.VerificationStatus,
		source.VerifiedAt,
		source.VerifiedBy,
		source.Currency,
		source.SourceDetails,
		int64(0),
		sql.NullTime{},
		int64(0),
		sql.NullTime{},
		source.SupportsWithdrawal,
		source.WithdrawalMethod,
		now,
		now,
	); err != nil {
		if existing, getErr := s.GetFundingSource(ctx, tenantID, source.WalletID, source.SourceType, source.ExternalReference); getErr == nil {
			if err := ValidateFundingSourceMerge(existing, source); err != nil {
				return nil, err
			}
			return s.updateFundingSourceMetadata(ctx, existing.ID, source, now)
		}
		return nil, err
	}
	return &stored, nil
}

func ValidateFundingSourceMerge(existing *FundingSource, incoming FundingSource) error {
	if existing == nil ||
		existing.TenantID != incoming.TenantID ||
		existing.WalletID != incoming.WalletID ||
		existing.SourceType != incoming.SourceType ||
		existing.Currency != incoming.Currency ||
		!nullStringEqual(existing.PSPProvider, incoming.PSPProvider) ||
		!nullStringEqual(existing.ExternalReference, incoming.ExternalReference) ||
		!rawJSONMatches(existing.SourceDetails, incoming.SourceDetails) ||
		!fundingSourceWithdrawalMethodMergeAllowed(existing.WithdrawalMethod, incoming.WithdrawalMethod) {
		return ErrDuplicateFundingSource
	}
	return nil
}

func fundingSourceWithdrawalMethodMergeAllowed(existing, incoming json.RawMessage) bool {
	if len(existing) == 0 || len(incoming) == 0 {
		return true
	}
	return rawJSONMatches(existing, incoming)
}

func nullStringEqual(left, right sql.NullString) bool {
	return left.Valid == right.Valid && (!left.Valid || left.String == right.String)
}

func (s *Store) updateFundingSourceMetadata(ctx context.Context, sourceID int64, source FundingSource, updatedAt time.Time) (*FundingSource, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`UPDATE funding_sources
		SET supports_withdrawal = funding_sources.supports_withdrawal OR ?,
			verification_status = CASE
				WHEN funding_sources.verification_status = 'verified' THEN funding_sources.verification_status
				WHEN ? = 'verified' THEN ?
				ELSE funding_sources.verification_status
			END,
			verified_at = COALESCE(funding_sources.verified_at, ?),
			verified_by = COALESCE(funding_sources.verified_by, ?),
			withdrawal_method = COALESCE(funding_sources.withdrawal_method, ?),
			updated_at = ?
		WHERE tenant_id = ? AND id = ?
		RETURNING *`)
	var stored FundingSource
	if err := db.GetContext(ctx, &stored, stmt,
		source.SupportsWithdrawal,
		source.VerificationStatus,
		source.VerificationStatus,
		source.VerifiedAt,
		source.VerifiedBy,
		source.WithdrawalMethod,
		updatedAt,
		source.TenantID,
		sourceID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFundingSourceNotFound
		}
		return nil, err
	}
	return &stored, nil
}

func (s *Store) GetFundingSource(ctx context.Context, tenantID string, walletID uuid.UUID, sourceType string, externalRef sql.NullString) (*FundingSource, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
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
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFundingSourceNotFound
		}
		return nil, err
	}
	return &source, nil
}

func (s *Store) ListFundingSources(ctx context.Context, tenantID string, walletID uuid.UUID) ([]FundingSource, error) {
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
	stmt := db.Rebind(`SELECT * FROM funding_sources
		WHERE tenant_id = ? AND wallet_id = ?
		ORDER BY last_funded_at DESC NULLS LAST, created_at DESC`)
	var sources []FundingSource
	if err := db.SelectContext(ctx, &sources, stmt, tenantID, walletID); err != nil {
		return nil, err
	}
	return sources, nil
}

func (s *Store) GetFundingSourceByID(ctx context.Context, tenantID string, sourceID int64) (*FundingSource, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if sourceID <= 0 {
		return nil, ErrMissingFundingSourceID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind("SELECT * FROM funding_sources WHERE tenant_id = ? AND id = ?")
	var source FundingSource
	if err := db.GetContext(ctx, &source, stmt, tenantID, sourceID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrFundingSourceNotFound
		}
		return nil, err
	}
	return &source, nil
}

func (s *Store) CreateFundingLink(ctx context.Context, link LedgerFundingLink) (*LedgerFundingLink, error) {
	tenantID, err := ValidateTenantID(link.TenantID)
	if err != nil {
		return nil, err
	}
	link.TenantID = tenantID
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
	source, err := getFundingSourceForLinkTx(ctx, tx, tenantID, link.FundingSourceID)
	if err != nil {
		return nil, err
	}
	if err := ValidateFundingLinkLedgerEntry(ledgerEntry, source, link); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	stmt := tx.Rebind(`INSERT INTO ledger_funding_links(
		tenant_id, ledger_entry_id, funding_source_id, amount, currency, created_at
	) VALUES(?, ?, ?, ?, ?, ?)
	ON CONFLICT(tenant_id, ledger_entry_id, funding_source_id) DO NOTHING
	RETURNING *`)
	var stored LedgerFundingLink
	if err := tx.GetContext(ctx, &stored, stmt,
		tenantID,
		link.LedgerEntryID,
		link.FundingSourceID,
		link.Amount,
		link.Currency,
		now,
	); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		existing, err := getFundingLinkTx(ctx, tx, tenantID, link.LedgerEntryID, link.FundingSourceID)
		if err != nil {
			return nil, err
		}
		if err := ValidateFundingLinkReplay(existing, link); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		committed = true
		return existing, nil
	}
	updateStmt := tx.Rebind(fundingSourceUsageUpdateSQL(ledgerEntry.EntryType))
	result, err := tx.ExecContext(ctx, updateStmt, link.Amount, now, now, now, tenantID, link.FundingSourceID)
	if err != nil {
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, ErrFundingSourceNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return &stored, nil
}

func getLedgerEntryForUsageLinkTx(ctx context.Context, tx interface {
	Rebind(string) string
	GetContext(context.Context, any, string, ...any) error
}, tenantID string, ledgerEntryID int64) (*LedgerEntry, error) {
	stmt := tx.Rebind(`SELECT * FROM ledger_entries
		WHERE tenant_id = ? AND id = ?`)
	var entry LedgerEntry
	if err := tx.GetContext(ctx, &entry, stmt, tenantID, ledgerEntryID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrLedgerEntryNotFound
		}
		return nil, err
	}
	return &entry, nil
}

func getFundingSourceForLinkTx(ctx context.Context, tx interface {
	Rebind(string) string
	GetContext(context.Context, any, string, ...any) error
}, tenantID string, sourceID int64) (*FundingSource, error) {
	stmt := tx.Rebind(`SELECT * FROM funding_sources
		WHERE tenant_id = ? AND id = ?`)
	var source FundingSource
	if err := tx.GetContext(ctx, &source, stmt, tenantID, sourceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFundingSourceNotFound
		}
		return nil, err
	}
	return &source, nil
}

func getFundingLinkTx(ctx context.Context, tx interface {
	Rebind(string) string
	GetContext(context.Context, any, string, ...any) error
}, tenantID string, ledgerEntryID, fundingSourceID int64) (*LedgerFundingLink, error) {
	stmt := tx.Rebind(`SELECT * FROM ledger_funding_links
		WHERE tenant_id = ? AND ledger_entry_id = ? AND funding_source_id = ?`)
	var link LedgerFundingLink
	if err := tx.GetContext(ctx, &link, stmt, tenantID, ledgerEntryID, fundingSourceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrDuplicateFundingLink
		}
		return nil, err
	}
	return &link, nil
}

func ValidateFundingLinkLedgerEntry(entry *LedgerEntry, source *FundingSource, link LedgerFundingLink) error {
	if entry == nil {
		return ErrLedgerEntryNotFound
	}
	if source == nil {
		return ErrFundingSourceNotFound
	}
	if entry.TenantID != link.TenantID || entry.ID != link.LedgerEntryID ||
		source.TenantID != link.TenantID || source.ID != link.FundingSourceID {
		return ErrDuplicateFundingLink
	}
	if source.WalletID != entry.WalletID {
		return ErrFundingSourceNotFound
	}
	if entry.Amount != link.Amount {
		return ErrInvalidAmount
	}
	if entry.Currency != link.Currency {
		return ErrCurrencyMismatch
	}
	if source.Currency != link.Currency {
		return ErrCurrencyMismatch
	}
	switch entry.EntryType {
	case "credit", "debit":
		return nil
	default:
		return ErrInvalidDirection
	}
}

func ValidateFundingLinkReplay(existing *LedgerFundingLink, requested LedgerFundingLink) error {
	if existing == nil ||
		existing.TenantID != requested.TenantID ||
		existing.LedgerEntryID != requested.LedgerEntryID ||
		existing.FundingSourceID != requested.FundingSourceID ||
		existing.Amount != requested.Amount ||
		existing.Currency != requested.Currency {
		return ErrDuplicateFundingLink
	}
	return nil
}

func fundingSourceUsageUpdateSQL(entryType string) string {
	switch entryType {
	case "credit":
		return `UPDATE funding_sources
			SET total_funded = total_funded + ?,
				last_funded_at = GREATEST(COALESCE(last_funded_at, ?), ?),
				updated_at = ?
			WHERE tenant_id = ? AND id = ?`
	case "debit":
		return `UPDATE funding_sources
			SET total_withdrawn = total_withdrawn + ?,
				last_withdrawn_at = GREATEST(COALESCE(last_withdrawn_at, ?), ?),
				updated_at = ?
			WHERE tenant_id = ? AND id = ?`
	default:
		return ""
	}
}

func (s *Store) GetFundingSourceByPSPRef(ctx context.Context, tenantID, provider string, externalRef string) (*FundingSource, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
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
