package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type SettlementTransfer struct {
	DebitWalletID  uuid.UUID
	CreditWalletID uuid.UUID
	Amount         int64
	Description    string
	Metadata       json.RawMessage
}

type MultiLegSettlementParams struct {
	TenantID       string
	IdempotencyKey string
	Currency       string
	ReferenceType  string
	ReferenceID    string
	Metadata       json.RawMessage
	Transfers      []SettlementTransfer
	LimitUsage     LimitUsageParams
}

type HeldWithdrawalSettlementParams struct {
	HoldID                     int64
	Settlement                 MultiLegSettlementParams
	FundingSourceID            int64
	FundingReservationID       int64
	WithdrawalDestinationID    int64
	FundingTransferIndex       int
	FundingReservationProvider string
}

type SettlementTransferResult struct {
	DebitEntry  *LedgerEntry
	CreditEntry *LedgerEntry
}

type MultiLegSettlementResult struct {
	TransactionID int64
	Transfers     []SettlementTransferResult
	Existing      bool
}

type multiLegSettlementMode struct {
	SystemFundingWalletID uuid.UUID
	HeldWithdrawal        *HeldWithdrawalSettlementParams
}

func ValidateMultiLegSettlementParams(params MultiLegSettlementParams) error {
	if _, err := ValidateTenantID(params.TenantID); err != nil {
		return err
	}
	if params.IdempotencyKey == "" {
		return ErrMissingIdempotencyKey
	}
	if params.Currency == "" {
		return ErrMissingCurrency
	}
	if params.ReferenceType == "" {
		return ErrMissingReferenceType
	}
	if params.ReferenceID == "" {
		return ErrMissingReferenceID
	}
	if len(params.Transfers) == 0 {
		return ErrMissingSettlementTransfers
	}
	walletPresent := false
	for _, transfer := range params.Transfers {
		if transfer.DebitWalletID == uuid.Nil || transfer.CreditWalletID == uuid.Nil {
			return ErrMissingWalletID
		}
		if transfer.DebitWalletID == transfer.CreditWalletID {
			return ErrInvalidWalletPair
		}
		if transfer.Amount <= 0 {
			return ErrInvalidAmount
		}
		walletPresent = walletPresent ||
			transfer.DebitWalletID == params.LimitUsage.WalletID ||
			transfer.CreditWalletID == params.LimitUsage.WalletID
	}
	if err := ValidateLimitUsageParams(params.LimitUsage); err != nil {
		return err
	}
	if params.LimitUsage.TenantID != params.TenantID || params.LimitUsage.Currency != params.Currency {
		return ErrDuplicateLimitReservation
	}
	if !walletPresent {
		return ErrWalletNotFound
	}
	return nil
}

func ValidateHeldWithdrawalSettlementParams(params HeldWithdrawalSettlementParams) error {
	if params.HoldID <= 0 {
		return ErrInvalidHoldID
	}
	if err := ValidateMultiLegSettlementParams(params.Settlement); err != nil {
		return err
	}
	if params.FundingSourceID <= 0 {
		return ErrMissingFundingSourceID
	}
	if params.FundingReservationID <= 0 {
		return ErrMissingFundingSourceReservation
	}
	if params.FundingReservationProvider == "" || params.FundingReservationProvider != strings.TrimSpace(params.FundingReservationProvider) {
		return ErrMissingProviderCode
	}
	if params.WithdrawalDestinationID < 0 {
		return ErrInvalidDestinationID
	}
	if params.FundingTransferIndex < 0 || params.FundingTransferIndex >= len(params.Settlement.Transfers) {
		return ErrInvalidSettlementTransfer
	}
	fundingTransfer := params.Settlement.Transfers[params.FundingTransferIndex]
	if fundingTransfer.DebitWalletID != params.Settlement.LimitUsage.WalletID ||
		fundingTransfer.Amount != params.Settlement.LimitUsage.Amount {
		return ErrDuplicateFundingSourceReservation
	}
	var total int64
	for _, transfer := range params.Settlement.Transfers {
		if transfer.DebitWalletID != params.Settlement.LimitUsage.WalletID {
			return ErrInvalidSettlementTransfer
		}
		var err error
		total, err = checkedAddInt64(total, transfer.Amount)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) PostMultiLegSettlement(
	ctx context.Context,
	params MultiLegSettlementParams,
) (*MultiLegSettlementResult, error) {
	if err := ValidateMultiLegSettlementParams(params); err != nil {
		return nil, err
	}
	return s.postMultiLegSettlement(ctx, params, multiLegSettlementMode{})
}

func (s *Store) PostSystemFundedMultiLegSettlement(
	ctx context.Context,
	params MultiLegSettlementParams,
) (*MultiLegSettlementResult, error) {
	if err := ValidateMultiLegSettlementParams(params); err != nil {
		return nil, err
	}
	return s.postMultiLegSettlement(ctx, params, multiLegSettlementMode{
		SystemFundingWalletID: params.Transfers[0].DebitWalletID,
	})
}

func (s *Store) PostHeldWithdrawalSettlement(
	ctx context.Context,
	params HeldWithdrawalSettlementParams,
) (*MultiLegSettlementResult, error) {
	if err := ValidateHeldWithdrawalSettlementParams(params); err != nil {
		return nil, err
	}
	return s.postMultiLegSettlement(ctx, params.Settlement, multiLegSettlementMode{
		HeldWithdrawal: &params,
	})
}

func (s *Store) postMultiLegSettlement(
	ctx context.Context,
	params MultiLegSettlementParams,
	mode multiLegSettlementMode,
) (*MultiLegSettlementResult, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var withdrawalHold *BalanceHold
	if mode.HeldWithdrawal != nil {
		withdrawalHold, err = s.lockHold(ctx, tx, params.TenantID, mode.HeldWithdrawal.HoldID)
		if err != nil {
			return nil, err
		}
		if err := validateHeldWithdrawalHold(withdrawalHold, *mode.HeldWithdrawal); err != nil {
			return nil, err
		}
	}

	wallets, orderedWallets, err := s.lockSettlementWallets(ctx, tx, params)
	if err != nil {
		return nil, err
	}
	existing, err := s.findMultiLegSettlement(ctx, tx, params)
	if err != nil {
		return nil, err
	}

	var withdrawalResources *heldWithdrawalResources
	if mode.HeldWithdrawal != nil {
		withdrawalResources, err = s.lockHeldWithdrawalResources(ctx, tx, *mode.HeldWithdrawal)
		if err != nil {
			return nil, err
		}
		if err := validateHeldWithdrawalResources(*mode.HeldWithdrawal, withdrawalResources); err != nil {
			return nil, err
		}
		if existing != nil {
			if err := s.validateHeldWithdrawalReplay(
				ctx,
				tx,
				*mode.HeldWithdrawal,
				withdrawalHold,
				withdrawalResources,
				existing,
			); err != nil {
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return existing, nil
		}
		if withdrawalHold.Status != HoldStatusCommitted {
			return nil, ErrHoldNotActive
		}
		if withdrawalResources.FundingReservation.Status != FundingSourceReservationReserved ||
			withdrawalResources.FundingReservation.LedgerEntryID.Valid {
			return nil, ErrInvalidStatusTransition
		}
		if withdrawalResources.LimitReservation.Status != LimitReservationStatusReserved {
			return nil, ErrDuplicateLimitReservation
		}
		if err := prepareHeldWithdrawalTotals(*mode.HeldWithdrawal, withdrawalResources); err != nil {
			return nil, err
		}
	}
	if existing != nil {
		if err := s.validateConsumedLimitUsage(ctx, tx, params.LimitUsage, existing.TransactionID); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return existing, nil
	}
	if err := validateSettlementWallets(wallets, params, mode); err != nil {
		return nil, err
	}

	var createdAt time.Time
	if err := tx.GetContext(ctx, &createdAt, `SELECT clock_timestamp()`); err != nil {
		return nil, err
	}
	txID, existing, err := s.insertMultiLegLedgerTransaction(ctx, tx, params, orderedWallets[0].CurrencyUnitID, createdAt)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if mode.HeldWithdrawal != nil {
			if err := s.validateHeldWithdrawalReplay(
				ctx,
				tx,
				*mode.HeldWithdrawal,
				withdrawalHold,
				withdrawalResources,
				existing,
			); err != nil {
				return nil, err
			}
		} else {
			if err := s.validateConsumedLimitUsage(ctx, tx, params.LimitUsage, existing.TransactionID); err != nil {
				return nil, err
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return existing, nil
	}

	if mode.HeldWithdrawal == nil {
		limitWallet := wallets[params.LimitUsage.WalletID]
		reservation, err := s.reserveLimitUsageInTx(ctx, tx, params.LimitUsage, limitWallet)
		if err != nil {
			return nil, err
		}
		if reservation.Status == LimitReservationStatusReleased {
			return nil, ErrLimitReservationReleased
		}
		if reservation.Status == LimitReservationStatusConsumed {
			return nil, ErrLimitReservationConsumed
		}
	}

	result := &MultiLegSettlementResult{
		TransactionID: txID,
		Transfers:     make([]SettlementTransferResult, 0, len(params.Transfers)),
	}
	for _, transfer := range params.Transfers {
		debitWallet := wallets[transfer.DebitWalletID]
		creditWallet := wallets[transfer.CreditWalletID]
		debitBalance, err := checkedSubtractInt64(debitWallet.Balance, transfer.Amount)
		if err != nil {
			return nil, err
		}
		debitWallet.Balance = debitBalance
		if mode.HeldWithdrawal == nil {
			debitAvailable, err := checkedSubtractInt64(debitWallet.AvailableBalance, transfer.Amount)
			if err != nil {
				return nil, err
			}
			debitWallet.AvailableBalance = debitAvailable
		}
		creditBalance, err := checkedAddInt64(creditWallet.Balance, transfer.Amount)
		if err != nil {
			return nil, err
		}
		creditAvailable, err := checkedAddInt64(creditWallet.AvailableBalance, transfer.Amount)
		if err != nil {
			return nil, err
		}
		creditWallet.Balance = creditBalance
		creditWallet.AvailableBalance = creditAvailable

		debitSequence, err := s.nextWalletSequence(ctx, tx, params.TenantID, debitWallet.ID)
		if err != nil {
			return nil, err
		}
		creditSequence, err := s.nextWalletSequence(ctx, tx, params.TenantID, creditWallet.ID)
		if err != nil {
			return nil, err
		}
		debitEntry, err := s.insertEntry(ctx, tx, LedgerEntry{
			TenantID:       params.TenantID,
			TransactionID:  txID,
			WalletID:       debitWallet.ID,
			EntryType:      "debit",
			Amount:         transfer.Amount,
			Currency:       params.Currency,
			CurrencyUnitID: debitWallet.CurrencyUnitID,
			BalanceAfter:   debitWallet.Balance,
			WalletSeq:      debitSequence,
			Status:         "completed",
			Description:    sql.NullString{String: transfer.Description, Valid: transfer.Description != ""},
			Metadata:       RawJSON(transfer.Metadata),
			CreatedAt:      createdAt,
		})
		if err != nil {
			return nil, err
		}
		creditEntry, err := s.insertEntry(ctx, tx, LedgerEntry{
			TenantID:       params.TenantID,
			TransactionID:  txID,
			WalletID:       creditWallet.ID,
			EntryType:      "credit",
			Amount:         transfer.Amount,
			Currency:       params.Currency,
			CurrencyUnitID: creditWallet.CurrencyUnitID,
			BalanceAfter:   creditWallet.Balance,
			WalletSeq:      creditSequence,
			Status:         "completed",
			Description:    sql.NullString{String: transfer.Description, Valid: transfer.Description != ""},
			Metadata:       RawJSON(transfer.Metadata),
			CreatedAt:      createdAt,
		})
		if err != nil {
			return nil, err
		}
		if err := s.linkCounterEntries(ctx, tx, debitEntry.ID, creditEntry.ID); err != nil {
			return nil, err
		}
		debitEntry.CounterID = sql.NullInt64{Int64: creditEntry.ID, Valid: true}
		creditEntry.CounterID = sql.NullInt64{Int64: debitEntry.ID, Valid: true}
		result.Transfers = append(result.Transfers, SettlementTransferResult{
			DebitEntry:  debitEntry,
			CreditEntry: creditEntry,
		})
	}
	for _, wallet := range orderedWallets {
		stmt := s.DB.Rebind(`UPDATE wallets
			SET balance = ?, available_balance = ?, version = version + 1, updated_at = ?
			WHERE tenant_id = ? AND id = ?`)
		updated, err := tx.ExecContext(ctx, stmt,
			wallet.Balance,
			wallet.AvailableBalance,
			createdAt,
			params.TenantID,
			wallet.ID,
		)
		if err != nil {
			return nil, err
		}
		if err := requireOneRow(updated); err != nil {
			return nil, err
		}
	}
	if mode.HeldWithdrawal != nil {
		if err := s.finalizeHeldWithdrawal(
			ctx,
			tx,
			*mode.HeldWithdrawal,
			withdrawalHold,
			withdrawalResources,
			result,
			createdAt,
		); err != nil {
			return nil, err
		}
	}
	if err := s.consumeLimitUsageInTx(ctx, tx, ConsumeLimitUsageParams{
		Reservation:         params.LimitUsage,
		LedgerTransactionID: txID,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) lockSettlementWallets(
	ctx context.Context,
	tx *sqlx.Tx,
	params MultiLegSettlementParams,
) (map[uuid.UUID]*Wallet, []*Wallet, error) {
	unique := make(map[uuid.UUID]struct{}, len(params.Transfers)*2)
	ids := make([]uuid.UUID, 0, len(params.Transfers)*2)
	for _, transfer := range params.Transfers {
		for _, id := range []uuid.UUID{transfer.DebitWalletID, transfer.CreditWalletID} {
			if _, exists := unique[id]; exists {
				continue
			}
			unique[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	query, args, err := sqlx.In(
		`SELECT * FROM wallets WHERE tenant_id = ? AND id IN (?) ORDER BY id FOR UPDATE`,
		params.TenantID,
		ids,
	)
	if err != nil {
		return nil, nil, err
	}
	query = s.DB.Rebind(query)
	var rows []Wallet
	if err := tx.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, nil, err
	}
	if len(rows) != len(ids) {
		return nil, nil, ErrWalletNotFound
	}
	wallets := make(map[uuid.UUID]*Wallet, len(rows))
	ordered := make([]*Wallet, 0, len(rows))
	for index := range rows {
		wallet := &rows[index]
		wallets[wallet.ID] = wallet
		ordered = append(ordered, wallet)
	}
	return wallets, ordered, nil
}

func validateSettlementWallets(
	wallets map[uuid.UUID]*Wallet,
	params MultiLegSettlementParams,
	mode multiLegSettlementMode,
) error {
	balanceDeltas := make(map[uuid.UUID]int64, len(wallets))
	availableDeltas := make(map[uuid.UUID]int64, len(wallets))
	var currencyUnitID int64
	for _, wallet := range wallets {
		if wallet.TenantID != params.TenantID {
			return ErrWalletNotFound
		}
		if wallet.Status != WalletStatusActive {
			return ErrWalletInactive
		}
		if wallet.Currency != params.Currency {
			return ErrCurrencyMismatch
		}
		if err := ValidateCurrencyUnitID(wallet.CurrencyUnitID); err != nil {
			return err
		}
		if currencyUnitID == 0 {
			currencyUnitID = wallet.CurrencyUnitID
		} else if wallet.CurrencyUnitID != currencyUnitID {
			return ErrCurrencyMismatch
		}
	}
	if mode.SystemFundingWalletID != uuid.Nil {
		fundingWallet := wallets[mode.SystemFundingWalletID]
		if fundingWallet == nil || fundingWallet.OwnerType != OwnerTypeSystem {
			return ErrSystemDebitWalletRequired
		}
	}
	for _, transfer := range params.Transfers {
		debitBalanceDelta, err := checkedSubtractInt64(balanceDeltas[transfer.DebitWalletID], transfer.Amount)
		if err != nil {
			return err
		}
		balanceDeltas[transfer.DebitWalletID] = debitBalanceDelta
		if mode.HeldWithdrawal == nil {
			debitAvailableDelta, err := checkedSubtractInt64(availableDeltas[transfer.DebitWalletID], transfer.Amount)
			if err != nil {
				return err
			}
			availableDeltas[transfer.DebitWalletID] = debitAvailableDelta
		}
		creditBalanceDelta, err := checkedAddInt64(balanceDeltas[transfer.CreditWalletID], transfer.Amount)
		if err != nil {
			return err
		}
		balanceDeltas[transfer.CreditWalletID] = creditBalanceDelta
		creditAvailableDelta, err := checkedAddInt64(availableDeltas[transfer.CreditWalletID], transfer.Amount)
		if err != nil {
			return err
		}
		availableDeltas[transfer.CreditWalletID] = creditAvailableDelta
	}
	for walletID, wallet := range wallets {
		balance, err := checkedAddInt64(wallet.Balance, balanceDeltas[walletID])
		if err != nil {
			return err
		}
		available, err := checkedAddInt64(wallet.AvailableBalance, availableDeltas[walletID])
		if err != nil {
			return err
		}
		if available < 0 && walletID != mode.SystemFundingWalletID {
			return ErrInsufficientFunds
		}
		if available > balance {
			return ErrInvalidSettlementTransfer
		}
	}
	balances := make(map[uuid.UUID]int64, len(wallets))
	availableBalances := make(map[uuid.UUID]int64, len(wallets))
	for walletID, wallet := range wallets {
		balances[walletID] = wallet.Balance
		availableBalances[walletID] = wallet.AvailableBalance
	}
	for _, transfer := range params.Transfers {
		var err error
		balances[transfer.DebitWalletID], err = checkedSubtractInt64(balances[transfer.DebitWalletID], transfer.Amount)
		if err != nil {
			return err
		}
		if mode.HeldWithdrawal == nil {
			availableBalances[transfer.DebitWalletID], err = checkedSubtractInt64(
				availableBalances[transfer.DebitWalletID],
				transfer.Amount,
			)
			if err != nil {
				return err
			}
		}
		balances[transfer.CreditWalletID], err = checkedAddInt64(balances[transfer.CreditWalletID], transfer.Amount)
		if err != nil {
			return err
		}
		availableBalances[transfer.CreditWalletID], err = checkedAddInt64(
			availableBalances[transfer.CreditWalletID],
			transfer.Amount,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

type heldWithdrawalResources struct {
	LimitReservation        *LimitUsageReservation
	FundingSource           *FundingSource
	FundingReservation      *FundingSourceWithdrawalReservation
	Destination             *WithdrawalDestination
	FundingTotalWithdrawn   int64
	DestinationTotalDebited int64
}

func validateHeldWithdrawalHold(hold *BalanceHold, params HeldWithdrawalSettlementParams) error {
	if hold == nil {
		return ErrHoldNotFound
	}
	total, err := heldWithdrawalAmount(params)
	if err != nil {
		return err
	}
	if hold.TenantID != params.Settlement.TenantID || hold.ID != params.HoldID {
		return ErrDuplicateHold
	}
	if hold.WalletID != params.Settlement.LimitUsage.WalletID {
		return ErrHoldWalletMismatch
	}
	if hold.Amount != total ||
		hold.ReferenceType != params.Settlement.ReferenceType ||
		hold.ReferenceID != params.Settlement.ReferenceID ||
		!hold.CommittedAt.Valid {
		return ErrDuplicateHold
	}
	switch hold.Status {
	case HoldStatusCommitted:
		if hold.AmountRemaining != total || hold.CapturedAt.Valid {
			return ErrDuplicateHold
		}
	case HoldStatusCaptured:
		if hold.AmountRemaining != 0 || !hold.CapturedAt.Valid {
			return ErrDuplicateHold
		}
	default:
		return ErrHoldNotActive
	}
	return nil
}

func heldWithdrawalAmount(params HeldWithdrawalSettlementParams) (int64, error) {
	var total int64
	for _, transfer := range params.Settlement.Transfers {
		var err error
		total, err = checkedAddInt64(total, transfer.Amount)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

func (s *Store) lockHeldWithdrawalResources(
	ctx context.Context,
	tx *sqlx.Tx,
	params HeldWithdrawalSettlementParams,
) (*heldWithdrawalResources, error) {
	limitReservation, err := s.lockLimitReservation(
		ctx,
		tx,
		params.Settlement.LimitUsage.TenantID,
		params.Settlement.LimitUsage.CommandID,
	)
	if err != nil {
		return nil, err
	}
	if !limitReservationMatches(limitReservation, params.Settlement.LimitUsage) {
		return nil, ErrDuplicateLimitReservation
	}
	if _, err := s.lockReservationPeriodUsage(ctx, tx, limitReservation); err != nil {
		return nil, err
	}
	fundingSource, err := getFundingSourceForLinkTx(
		ctx,
		tx,
		params.Settlement.TenantID,
		params.FundingSourceID,
	)
	if err != nil {
		return nil, err
	}
	fundingReservation, err := getFundingSourceWithdrawalReservationByIDTx(
		ctx,
		tx,
		params.Settlement.TenantID,
		params.FundingReservationID,
	)
	if err != nil {
		return nil, err
	}

	var destination *WithdrawalDestination
	if params.WithdrawalDestinationID > 0 {
		destination, err = getWithdrawalDestinationForSettlementTx(
			ctx,
			tx,
			params.Settlement.TenantID,
			params.WithdrawalDestinationID,
		)
		if err != nil {
			return nil, err
		}
	}
	return &heldWithdrawalResources{
		LimitReservation:   limitReservation,
		FundingSource:      fundingSource,
		FundingReservation: fundingReservation,
		Destination:        destination,
	}, nil
}

func getWithdrawalDestinationForSettlementTx(
	ctx context.Context,
	tx *sqlx.Tx,
	tenantID string,
	destinationID int64,
) (*WithdrawalDestination, error) {
	stmt := tx.Rebind(`SELECT * FROM withdrawal_destinations
		WHERE tenant_id = ? AND id = ? FOR UPDATE`)
	var destination WithdrawalDestination
	if err := tx.GetContext(ctx, &destination, stmt, tenantID, destinationID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrDestinationNotFound
		}
		return nil, err
	}
	return &destination, nil
}

func validateHeldWithdrawalResources(
	params HeldWithdrawalSettlementParams,
	resources *heldWithdrawalResources,
) error {
	if resources == nil || resources.FundingSource == nil || resources.FundingReservation == nil || resources.LimitReservation == nil {
		return ErrInvalidSettlementTransfer
	}
	if !limitReservationMatches(resources.LimitReservation, params.Settlement.LimitUsage) {
		return ErrDuplicateLimitReservation
	}
	fundingTransfer := params.Settlement.Transfers[params.FundingTransferIndex]
	source := resources.FundingSource
	if source.TenantID != params.Settlement.TenantID ||
		source.ID != params.FundingSourceID ||
		source.WalletID != fundingTransfer.DebitWalletID {
		return ErrFundingSourceNotFound
	}
	if source.Currency != params.Settlement.Currency {
		return ErrCurrencyMismatch
	}
	if !source.PSPProvider.Valid || source.PSPProvider.String != params.FundingReservationProvider {
		return ErrDuplicateFundingSourceReservation
	}
	if err := ValidateFundingSourceReadyForWithdrawal(source); err != nil {
		return err
	}
	reservation := resources.FundingReservation
	if reservation.TenantID != params.Settlement.TenantID ||
		reservation.ID != params.FundingReservationID ||
		reservation.FundingSourceID != params.FundingSourceID ||
		reservation.Amount != fundingTransfer.Amount ||
		reservation.Currency != params.Settlement.Currency ||
		reservation.ProviderCode != params.FundingReservationProvider {
		return ErrDuplicateFundingSourceReservation
	}
	if params.WithdrawalDestinationID == 0 {
		if resources.Destination != nil {
			return ErrDuplicateDestinationLink
		}
		return nil
	}
	if resources.Destination == nil || resources.Destination.ID != params.WithdrawalDestinationID {
		return ErrDestinationNotFound
	}
	return ValidateWithdrawalDestinationFundingSource(*resources.Destination, source)
}

func prepareHeldWithdrawalTotals(
	params HeldWithdrawalSettlementParams,
	resources *heldWithdrawalResources,
) error {
	amount := params.Settlement.Transfers[params.FundingTransferIndex].Amount
	total, err := checkedAddInt64(resources.FundingSource.TotalWithdrawn, amount)
	if err != nil {
		return err
	}
	if total > resources.FundingSource.TotalFunded {
		return ErrFundingSourceLimitExceeded
	}
	resources.FundingTotalWithdrawn = total
	if resources.Destination != nil {
		total, err := checkedAddInt64(resources.Destination.TotalWithdrawn, amount)
		if err != nil {
			return err
		}
		resources.DestinationTotalDebited = total
	}
	return nil
}

func (s *Store) finalizeHeldWithdrawal(
	ctx context.Context,
	tx *sqlx.Tx,
	params HeldWithdrawalSettlementParams,
	hold *BalanceHold,
	resources *heldWithdrawalResources,
	result *MultiLegSettlementResult,
	createdAt time.Time,
) error {
	if result == nil || params.FundingTransferIndex >= len(result.Transfers) {
		return ErrInvalidSettlementTransfer
	}
	principalEntry := result.Transfers[params.FundingTransferIndex].DebitEntry
	if principalEntry == nil {
		return ErrLedgerEntryNotFound
	}
	amount := params.Settlement.Transfers[params.FundingTransferIndex].Amount
	fundingLink := LedgerFundingLink{
		TenantID:                params.Settlement.TenantID,
		LedgerEntryID:           principalEntry.ID,
		FundingSourceID:         params.FundingSourceID,
		WithdrawalReservationID: sql.NullInt64{Int64: params.FundingReservationID, Valid: true},
		Amount:                  amount,
		Currency:                params.Settlement.Currency,
	}
	if err := ValidateFundingLinkLedgerEntry(principalEntry, resources.FundingSource, fundingLink); err != nil {
		return err
	}
	if err := ValidateFundingSourceWithdrawalConsumption(resources.FundingReservation, principalEntry, fundingLink); err != nil {
		return err
	}
	insertFundingLink := tx.Rebind(`INSERT INTO ledger_funding_links(
		tenant_id, ledger_entry_id, funding_source_id, withdrawal_reservation_id, amount, currency, created_at
	) VALUES(?, ?, ?, ?, ?, ?, ?)`)
	inserted, err := tx.ExecContext(ctx, insertFundingLink,
		fundingLink.TenantID,
		fundingLink.LedgerEntryID,
		fundingLink.FundingSourceID,
		fundingLink.WithdrawalReservationID,
		fundingLink.Amount,
		fundingLink.Currency,
		createdAt,
	)
	if err != nil {
		return err
	}
	if err := requireOneRow(inserted); err != nil {
		return err
	}
	updateSource := tx.Rebind(`UPDATE funding_sources
		SET total_withdrawn = ?,
			last_withdrawn_at = GREATEST(COALESCE(last_withdrawn_at, ?), ?),
			updated_at = ?
		WHERE tenant_id = ? AND id = ?`)
	updated, err := tx.ExecContext(ctx, updateSource,
		resources.FundingTotalWithdrawn,
		createdAt,
		createdAt,
		createdAt,
		params.Settlement.TenantID,
		params.FundingSourceID,
	)
	if err != nil {
		return err
	}
	if err := requireOneRow(updated); err != nil {
		return err
	}
	consumeFundingReservation := tx.Rebind(`UPDATE funding_source_withdrawal_reservations
		SET status = 'consumed', ledger_entry_id = ?, consumed_at = ?
		WHERE tenant_id = ? AND id = ? AND status = 'reserved'`)
	updated, err = tx.ExecContext(ctx, consumeFundingReservation,
		principalEntry.ID,
		createdAt,
		params.Settlement.TenantID,
		params.FundingReservationID,
	)
	if err != nil {
		return err
	}
	if err := requireOneRow(updated); err != nil {
		return ErrInvalidStatusTransition
	}

	if resources.Destination != nil {
		destinationLink := LedgerWithdrawalDestinationLink{
			TenantID:      params.Settlement.TenantID,
			LedgerEntryID: principalEntry.ID,
			DestinationID: params.WithdrawalDestinationID,
			Amount:        amount,
			Currency:      params.Settlement.Currency,
		}
		if err := ValidateWithdrawalDestinationLinkLedgerEntry(principalEntry, resources.Destination, destinationLink); err != nil {
			return err
		}
		insertDestinationLink := tx.Rebind(`INSERT INTO ledger_withdrawal_destination_links(
			tenant_id, ledger_entry_id, destination_id, amount, currency, created_at
		) VALUES(?, ?, ?, ?, ?, ?)`)
		inserted, err := tx.ExecContext(ctx, insertDestinationLink,
			destinationLink.TenantID,
			destinationLink.LedgerEntryID,
			destinationLink.DestinationID,
			destinationLink.Amount,
			destinationLink.Currency,
			createdAt,
		)
		if err != nil {
			return err
		}
		if err := requireOneRow(inserted); err != nil {
			return err
		}
		updateDestination := tx.Rebind(`UPDATE withdrawal_destinations
			SET last_used_at = GREATEST(COALESCE(last_used_at, ?), ?),
				total_withdrawn = ?,
				updated_at = ?
			WHERE tenant_id = ? AND id = ?`)
		updated, err := tx.ExecContext(ctx, updateDestination,
			createdAt,
			createdAt,
			resources.DestinationTotalDebited,
			createdAt,
			params.Settlement.TenantID,
			params.WithdrawalDestinationID,
		)
		if err != nil {
			return err
		}
		if err := requireOneRow(updated); err != nil {
			return err
		}
	}

	total, err := heldWithdrawalAmount(params)
	if err != nil {
		return err
	}
	return s.captureDebitHold(ctx, tx, params.Settlement.TenantID, hold, total, createdAt)
}

func (s *Store) validateHeldWithdrawalReplay(
	ctx context.Context,
	tx *sqlx.Tx,
	params HeldWithdrawalSettlementParams,
	hold *BalanceHold,
	resources *heldWithdrawalResources,
	result *MultiLegSettlementResult,
) error {
	if hold.Status != HoldStatusCaptured {
		return ErrDuplicateHold
	}
	if result == nil || params.FundingTransferIndex >= len(result.Transfers) {
		return ErrDuplicateTransaction
	}
	principalEntry := result.Transfers[params.FundingTransferIndex].DebitEntry
	if principalEntry == nil {
		return ErrDuplicateTransaction
	}
	if resources.LimitReservation.Status != LimitReservationStatusConsumed ||
		!resources.LimitReservation.LedgerTransactionID.Valid ||
		resources.LimitReservation.LedgerTransactionID.Int64 != result.TransactionID {
		return ErrDuplicateLimitReservation
	}
	if resources.FundingReservation.Status != FundingSourceReservationConsumed ||
		!resources.FundingReservation.LedgerEntryID.Valid ||
		resources.FundingReservation.LedgerEntryID.Int64 != principalEntry.ID {
		return ErrDuplicateFundingSourceReservation
	}
	amount := params.Settlement.Transfers[params.FundingTransferIndex].Amount
	fundingLink := LedgerFundingLink{
		TenantID:                params.Settlement.TenantID,
		LedgerEntryID:           principalEntry.ID,
		FundingSourceID:         params.FundingSourceID,
		WithdrawalReservationID: sql.NullInt64{Int64: params.FundingReservationID, Valid: true},
		Amount:                  amount,
		Currency:                params.Settlement.Currency,
	}
	existingFundingLink, err := getFundingLinkTx(ctx, tx, fundingLink.TenantID, fundingLink.LedgerEntryID, fundingLink.FundingSourceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDuplicateFundingLink
		}
		return err
	}
	if err := ValidateFundingLinkReplay(existingFundingLink, fundingLink); err != nil {
		return err
	}
	fundingLinks, err := countSettlementLinks(ctx, tx, "ledger_funding_links", params.Settlement.TenantID, result.TransactionID)
	if err != nil {
		return err
	}
	if fundingLinks != 1 {
		return ErrDuplicateFundingLink
	}

	expectedDestinationLinks := 0
	if resources.Destination != nil {
		expectedDestinationLinks = 1
		destinationLink := LedgerWithdrawalDestinationLink{
			TenantID:      params.Settlement.TenantID,
			LedgerEntryID: principalEntry.ID,
			DestinationID: params.WithdrawalDestinationID,
			Amount:        amount,
			Currency:      params.Settlement.Currency,
		}
		existingDestinationLink, err := getWithdrawalDestinationLinkTx(
			ctx,
			tx,
			destinationLink.TenantID,
			destinationLink.LedgerEntryID,
			destinationLink.DestinationID,
		)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrDuplicateDestinationLink
			}
			return err
		}
		if err := ValidateWithdrawalDestinationLinkReplay(existingDestinationLink, destinationLink); err != nil {
			return err
		}
	}
	destinationLinks, err := countSettlementLinks(
		ctx,
		tx,
		"ledger_withdrawal_destination_links",
		params.Settlement.TenantID,
		result.TransactionID,
	)
	if err != nil {
		return err
	}
	if destinationLinks != expectedDestinationLinks {
		return ErrDuplicateDestinationLink
	}
	return nil
}

func countSettlementLinks(
	ctx context.Context,
	tx *sqlx.Tx,
	table string,
	tenantID string,
	transactionID int64,
) (int, error) {
	var query string
	switch table {
	case "ledger_funding_links":
		query = `SELECT COUNT(*) FROM ledger_funding_links link
			JOIN ledger_entries entry ON entry.tenant_id = link.tenant_id AND entry.id = link.ledger_entry_id
			WHERE entry.tenant_id = ? AND entry.transaction_id = ?`
	case "ledger_withdrawal_destination_links":
		query = `SELECT COUNT(*) FROM ledger_withdrawal_destination_links link
			JOIN ledger_entries entry ON entry.tenant_id = link.tenant_id AND entry.id = link.ledger_entry_id
			WHERE entry.tenant_id = ? AND entry.transaction_id = ?`
	default:
		return 0, ErrInvalidSettlementTransfer
	}
	var count int
	if err := tx.GetContext(ctx, &count, tx.Rebind(query), tenantID, transactionID); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) findMultiLegSettlement(
	ctx context.Context,
	tx *sqlx.Tx,
	params MultiLegSettlementParams,
) (*MultiLegSettlementResult, error) {
	stmt := s.DB.Rebind(`SELECT * FROM ledger_transactions
		WHERE tenant_id = ? AND idempotency_key = ?`)
	var transaction LedgerTransaction
	if err := tx.GetContext(ctx, &transaction, stmt, params.TenantID, params.IdempotencyKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return s.loadAndValidateMultiLegSettlement(ctx, tx, transaction, params)
}

func (s *Store) insertMultiLegLedgerTransaction(
	ctx context.Context,
	tx *sqlx.Tx,
	params MultiLegSettlementParams,
	currencyUnitID int64,
	createdAt time.Time,
) (int64, *MultiLegSettlementResult, error) {
	stmt := s.DB.Rebind(`INSERT INTO ledger_transactions(
		tenant_id, idempotency_key, currency, currency_unit_version_id,
		reference_type, reference_id, status, metadata, created_at
	) VALUES(?, ?, ?, ?, ?, ?, 'completed', ?, ?)
	ON CONFLICT(tenant_id, idempotency_key) DO NOTHING
	RETURNING id`)
	var transactionID int64
	err := tx.GetContext(ctx, &transactionID, stmt,
		params.TenantID,
		params.IdempotencyKey,
		params.Currency,
		currencyUnitID,
		params.ReferenceType,
		params.ReferenceID,
		params.Metadata,
		createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		existing, loadErr := s.findMultiLegSettlement(ctx, tx, params)
		return 0, existing, loadErr
	}
	if err != nil {
		return 0, nil, err
	}
	return transactionID, nil, nil
}

func (s *Store) loadAndValidateMultiLegSettlement(
	ctx context.Context,
	tx *sqlx.Tx,
	transaction LedgerTransaction,
	params MultiLegSettlementParams,
) (*MultiLegSettlementResult, error) {
	if transaction.TenantID != params.TenantID ||
		transaction.IdempotencyKey != params.IdempotencyKey ||
		transaction.Currency != params.Currency ||
		transaction.ReferenceType != params.ReferenceType ||
		!transaction.ReferenceID.Valid ||
		transaction.ReferenceID.String != params.ReferenceID ||
		transaction.Status != "completed" ||
		!rawJSONMatches(transaction.Metadata, params.Metadata) {
		return nil, ErrDuplicateTransaction
	}
	entries, err := s.listEntriesByTransaction(ctx, tx, params.TenantID, transaction.ID)
	if err != nil {
		return nil, err
	}
	if len(entries) != len(params.Transfers)*2 {
		return nil, ErrDuplicateTransaction
	}
	result := &MultiLegSettlementResult{
		TransactionID: transaction.ID,
		Transfers:     make([]SettlementTransferResult, 0, len(params.Transfers)),
		Existing:      true,
	}
	for index, transfer := range params.Transfers {
		debit := entries[index*2]
		credit := entries[index*2+1]
		if !settlementEntryMatches(debit, transfer.DebitWalletID, "debit", params.Currency, transfer) ||
			!settlementEntryMatches(credit, transfer.CreditWalletID, "credit", params.Currency, transfer) ||
			transaction.CurrencyUnitID <= 0 ||
			debit.CurrencyUnitID != transaction.CurrencyUnitID ||
			credit.CurrencyUnitID != transaction.CurrencyUnitID ||
			!debit.CounterID.Valid || debit.CounterID.Int64 != credit.ID ||
			!credit.CounterID.Valid || credit.CounterID.Int64 != debit.ID {
			return nil, ErrDuplicateTransaction
		}
		result.Transfers = append(result.Transfers, SettlementTransferResult{
			DebitEntry:  debit,
			CreditEntry: credit,
		})
	}
	return result, nil
}

func settlementEntryMatches(
	entry *LedgerEntry,
	walletID uuid.UUID,
	entryType string,
	currency string,
	transfer SettlementTransfer,
) bool {
	return entry != nil &&
		entry.WalletID == walletID &&
		entry.EntryType == entryType &&
		entry.Amount == transfer.Amount &&
		entry.Currency == currency &&
		entry.Status == "completed" &&
		ledgerEntryDescriptionMatches(entry, transfer.Description) &&
		rawJSONMatches(entry.Metadata, transfer.Metadata)
}

func (s *Store) validateConsumedLimitUsage(
	ctx context.Context,
	tx *sqlx.Tx,
	params LimitUsageParams,
	ledgerTransactionID int64,
) error {
	reservation, err := s.lockLimitReservation(ctx, tx, params.TenantID, params.CommandID)
	if err != nil {
		return err
	}
	if !limitReservationMatches(reservation, params) ||
		reservation.Status != LimitReservationStatusConsumed ||
		!reservation.LedgerTransactionID.Valid ||
		reservation.LedgerTransactionID.Int64 != ledgerTransactionID {
		return ErrDuplicateLimitReservation
	}
	return nil
}
