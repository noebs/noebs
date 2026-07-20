package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func (s *Store) ReserveFundingSourceWithdrawal(ctx context.Context, params ReserveFundingSourceWithdrawalParams) (*FundingSourceWithdrawalReservationResult, error) {
	tenantID, err := ValidateTenantID(params.TenantID)
	if err != nil {
		return nil, err
	}
	params.TenantID = tenantID
	if err := validateBoundedIdentifier(params.WorkflowID, WorkflowDecisionMaxWorkflowIDLength, ErrMissingWorkflowID, ErrInvalidWorkflowID); err != nil {
		return nil, err
	}
	if len(params.CandidateSourceIDs) == 0 {
		return nil, ErrMissingFundingSourceID
	}
	for _, sourceID := range params.CandidateSourceIDs {
		if sourceID <= 0 {
			return nil, ErrMissingFundingSourceID
		}
	}
	params.CandidateSourceIDs = slices.Clone(params.CandidateSourceIDs)
	slices.Sort(params.CandidateSourceIDs)
	params.CandidateSourceIDs = slices.Compact(params.CandidateSourceIDs)
	if params.WalletID == uuid.Nil {
		return nil, ErrMissingWalletID
	}
	if params.Amount <= 0 {
		return nil, ErrInvalidAmount
	}
	if params.Currency == "" {
		return nil, ErrMissingCurrency
	}
	if params.ProviderCode == "" || params.ProviderCode != strings.TrimSpace(params.ProviderCode) {
		return nil, ErrMissingProviderCode
	}

	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := getFundingSourceWithdrawalReservationTx(ctx, tx, tenantID, params.WorkflowID, false)
	if err == nil {
		if !fundingSourceWithdrawalReservationMatches(*existing, params) {
			return nil, ErrDuplicateFundingSourceReservation
		}
		source, err := getFundingSourceForReservationTx(ctx, tx, tenantID, existing.FundingSourceID)
		if err != nil {
			return nil, err
		}
		existing, err = getFundingSourceWithdrawalReservationTx(ctx, tx, tenantID, params.WorkflowID, true)
		if err != nil {
			return nil, err
		}
		if !fundingSourceWithdrawalReservationMatches(*existing, params) || source.WalletID != params.WalletID {
			return nil, ErrDuplicateFundingSourceReservation
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &FundingSourceWithdrawalReservationResult{Reservation: *existing, Source: *source}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	var selected *FundingSource
	hadEligibleSource := false
	for _, sourceID := range params.CandidateSourceIDs {
		source, err := getFundingSourceForReservationTx(ctx, tx, tenantID, sourceID)
		if errors.Is(err, ErrFundingSourceNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if source.WalletID != params.WalletID || source.Currency != params.Currency ||
			!source.PSPProvider.Valid || source.PSPProvider.String != params.ProviderCode ||
			ValidateFundingSourceReadyForWithdrawal(source) != nil {
			continue
		}
		hadEligibleSource = true
		reserved, err := fundingSourceReservedAmountTx(ctx, tx, tenantID, source.ID)
		if err != nil {
			return nil, err
		}
		if source.TotalFunded-source.TotalWithdrawn-reserved >= params.Amount {
			selected = source
			break
		}
	}
	if selected == nil {
		if hadEligibleSource {
			return nil, ErrFundingSourceLimitExceeded
		}
		return nil, ErrFundingSourceNotFound
	}

	stmt := tx.Rebind(`INSERT INTO funding_source_withdrawal_reservations(
		tenant_id, workflow_id, funding_source_id, amount, currency, provider_code, status
	) VALUES (?, ?, ?, ?, ?, ?, 'reserved')
	ON CONFLICT (tenant_id, workflow_id) DO NOTHING
	RETURNING *`)
	var reservation FundingSourceWithdrawalReservation
	if err := tx.GetContext(ctx, &reservation, stmt,
		tenantID, params.WorkflowID, selected.ID, params.Amount, params.Currency, params.ProviderCode,
	); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		existing, err := getFundingSourceWithdrawalReservationTx(ctx, tx, tenantID, params.WorkflowID, true)
		if err != nil {
			return nil, err
		}
		if !fundingSourceWithdrawalReservationMatches(*existing, params) {
			return nil, ErrDuplicateFundingSourceReservation
		}
		reservation = *existing
		selected, err = getFundingSourceForReservationTx(ctx, tx, tenantID, reservation.FundingSourceID)
		if err != nil {
			return nil, err
		}
		if selected.WalletID != params.WalletID {
			return nil, ErrDuplicateFundingSourceReservation
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &FundingSourceWithdrawalReservationResult{Reservation: reservation, Source: *selected}, nil
}

func (s *Store) ReleaseFundingSourceWithdrawal(ctx context.Context, params ReleaseFundingSourceWithdrawalParams) error {
	tenantID, err := ValidateTenantID(params.TenantID)
	if err != nil {
		return err
	}
	if err := validateBoundedIdentifier(params.WorkflowID, WorkflowDecisionMaxWorkflowIDLength, ErrMissingWorkflowID, ErrInvalidWorkflowID); err != nil {
		return err
	}
	if params.ReleasedAt.IsZero() {
		return ErrMissingUpdatedAt
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	reservation, err := getFundingSourceWithdrawalReservationTx(ctx, tx, tenantID, params.WorkflowID, true)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrFundingSourceReservationNotFound
	}
	if err != nil {
		return err
	}
	switch reservation.Status {
	case FundingSourceReservationReleased:
		return tx.Commit()
	case FundingSourceReservationConsumed:
		return ErrInvalidStatusTransition
	case FundingSourceReservationReserved:
	default:
		return ErrInvalidStatus
	}
	stmt := tx.Rebind(`UPDATE funding_source_withdrawal_reservations
		SET status = 'released', released_at = ?
		WHERE tenant_id = ? AND workflow_id = ? AND status = 'reserved'`)
	result, err := tx.ExecContext(ctx, stmt, params.ReleasedAt, tenantID, params.WorkflowID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrInvalidStatusTransition
	}
	return tx.Commit()
}

func getFundingSourceWithdrawalReservationTx(ctx context.Context, tx *sqlx.Tx, tenantID, workflowID string, lock bool) (*FundingSourceWithdrawalReservation, error) {
	query := `SELECT * FROM funding_source_withdrawal_reservations
		WHERE tenant_id = ? AND workflow_id = ?`
	if lock {
		query += " FOR UPDATE"
	}
	var reservation FundingSourceWithdrawalReservation
	if err := tx.GetContext(ctx, &reservation, tx.Rebind(query), tenantID, workflowID); err != nil {
		return nil, err
	}
	return &reservation, nil
}

func getFundingSourceForReservationTx(ctx context.Context, tx *sqlx.Tx, tenantID string, sourceID int64) (*FundingSource, error) {
	stmt := tx.Rebind(`SELECT * FROM funding_sources
		WHERE tenant_id = ? AND id = ? FOR UPDATE`)
	var source FundingSource
	if err := tx.GetContext(ctx, &source, stmt, tenantID, sourceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFundingSourceNotFound
		}
		return nil, err
	}
	return &source, nil
}

func fundingSourceReservedAmountTx(ctx context.Context, tx *sqlx.Tx, tenantID string, sourceID int64) (int64, error) {
	stmt := tx.Rebind(`SELECT COALESCE(SUM(amount), 0)
		FROM funding_source_withdrawal_reservations
		WHERE tenant_id = ? AND funding_source_id = ? AND status = 'reserved'`)
	var amount int64
	if err := tx.GetContext(ctx, &amount, stmt, tenantID, sourceID); err != nil {
		return 0, err
	}
	return amount, nil
}

func fundingSourceWithdrawalReservationMatches(existing FundingSourceWithdrawalReservation, params ReserveFundingSourceWithdrawalParams) bool {
	return existing.TenantID == params.TenantID &&
		existing.WorkflowID == params.WorkflowID &&
		slices.Contains(params.CandidateSourceIDs, existing.FundingSourceID) &&
		existing.Amount == params.Amount &&
		existing.Currency == params.Currency &&
		existing.ProviderCode == params.ProviderCode
}
