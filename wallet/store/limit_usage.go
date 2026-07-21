package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

const (
	LimitReservationStatusReserved = "reserved"
	LimitReservationStatusConsumed = "consumed"
	LimitReservationStatusReleased = "released"

	LimitExceededPerTransaction = "exceeds_per_transaction_limit"
	LimitExceededDaily          = "exceeds_daily_limit"
	LimitExceededMonthly        = "exceeds_monthly_limit"
)

type LimitUsageParams struct {
	TenantID        string
	CommandID       string
	WalletID        uuid.UUID
	TransactionType string
	Currency        string
	Amount          int64
}

type LimitUsageReservation struct {
	ID                  int64         `db:"id"`
	TenantID            string        `db:"tenant_id"`
	CommandID           string        `db:"command_id"`
	WalletID            uuid.UUID     `db:"wallet_id"`
	TransactionType     string        `db:"transaction_type"`
	Currency            string        `db:"currency"`
	Amount              int64         `db:"amount"`
	DailyPeriodStart    time.Time     `db:"daily_period_start"`
	MonthlyPeriodStart  time.Time     `db:"monthly_period_start"`
	Status              string        `db:"status"`
	LedgerTransactionID sql.NullInt64 `db:"ledger_transaction_id"`
	CreatedAt           time.Time     `db:"created_at"`
	ConsumedAt          sql.NullTime  `db:"consumed_at"`
	ReleasedAt          sql.NullTime  `db:"released_at"`
}

type ConsumeLimitUsageParams struct {
	Reservation         LimitUsageParams
	LedgerTransactionID int64
}

type limitPeriodUsage struct {
	PeriodKind     string `db:"period_kind"`
	ReservedAmount int64  `db:"reserved_amount"`
	ConsumedAmount int64  `db:"consumed_amount"`
}

func ValidateLimitUsageParams(params LimitUsageParams) error {
	if _, err := ValidateTenantID(params.TenantID); err != nil {
		return err
	}
	if params.CommandID == "" {
		return ErrMissingLimitCommandID
	}
	if params.WalletID == uuid.Nil {
		return ErrMissingWalletID
	}
	if params.TransactionType == "" {
		return ErrMissingTransactionType
	}
	if params.Currency == "" {
		return ErrMissingCurrency
	}
	if params.Amount <= 0 {
		return ErrInvalidAmount
	}
	return nil
}

func ValidateConsumeLimitUsageParams(params ConsumeLimitUsageParams) error {
	if err := ValidateLimitUsageParams(params.Reservation); err != nil {
		return err
	}
	if params.LedgerTransactionID <= 0 {
		return ErrInvalidLedgerTransactionID
	}
	return nil
}

func (s *Store) ReserveLimitUsage(ctx context.Context, params LimitUsageParams) (*LimitUsageReservation, error) {
	if err := ValidateLimitUsageParams(params); err != nil {
		return nil, err
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

	wallet, err := s.lockLimitWallet(ctx, tx, params.TenantID, params.WalletID)
	if err != nil {
		return nil, err
	}
	reservation, err := s.reserveLimitUsageInTx(ctx, tx, params, wallet)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return reservation, nil
}

func (s *Store) ReleaseLimitUsage(ctx context.Context, params LimitUsageParams) error {
	if err := ValidateLimitUsageParams(params); err != nil {
		return err
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

	reservation, err := s.lockLimitReservation(ctx, tx, params.TenantID, params.CommandID)
	if err != nil {
		return err
	}
	if !limitReservationMatches(reservation, params) {
		return ErrDuplicateLimitReservation
	}
	switch reservation.Status {
	case LimitReservationStatusReleased:
		return tx.Commit()
	case LimitReservationStatusConsumed:
		return ErrLimitReservationConsumed
	case LimitReservationStatusReserved:
	default:
		return ErrInvalidLimitUsage
	}
	if _, err := s.lockReservationPeriodUsage(ctx, tx, reservation); err != nil {
		return err
	}
	if err := s.moveReservedLimitUsage(ctx, tx, reservation, false); err != nil {
		return err
	}
	stmt := s.DB.Rebind(`UPDATE transaction_limit_reservations
		SET status = ?, released_at = clock_timestamp()
		WHERE tenant_id = ? AND id = ? AND status = ?`)
	result, err := tx.ExecContext(ctx, stmt,
		LimitReservationStatusReleased,
		reservation.TenantID,
		reservation.ID,
		LimitReservationStatusReserved,
	)
	if err != nil {
		return err
	}
	if err := requireOneRow(result); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ConsumeLimitUsage(ctx context.Context, params ConsumeLimitUsageParams) error {
	if err := ValidateConsumeLimitUsageParams(params); err != nil {
		return err
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
	if err := s.consumeLimitUsageInTx(ctx, tx, params); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) reserveLimitUsageInTx(
	ctx context.Context,
	tx *sqlx.Tx,
	params LimitUsageParams,
	wallet *Wallet,
) (*LimitUsageReservation, error) {
	if wallet == nil || wallet.TenantID != params.TenantID || wallet.ID != params.WalletID {
		return nil, ErrWalletNotFound
	}
	existing, err := s.findLimitReservationForUpdate(ctx, tx, params.TenantID, params.CommandID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if !limitReservationMatches(existing, params) {
			return nil, ErrDuplicateLimitReservation
		}
		switch existing.Status {
		case LimitReservationStatusReserved:
			return existing, nil
		case LimitReservationStatusConsumed:
			return nil, ErrLimitReservationConsumed
		case LimitReservationStatusReleased:
			return nil, ErrLimitReservationReleased
		default:
			return nil, ErrInvalidLimitUsage
		}
	}
	if wallet.Status != WalletStatusActive {
		return nil, ErrWalletInactive
	}
	if wallet.Currency != params.Currency {
		return nil, ErrCurrencyMismatch
	}
	if err := ValidateCurrencyUnitID(wallet.CurrencyUnitID); err != nil {
		return nil, err
	}

	limit, err := s.loadActiveTransactionLimit(ctx, tx, params, wallet.KYCTier, wallet.CurrencyUnitID)
	if err != nil {
		return nil, err
	}
	if params.Amount > limit.PerTransactionLimit {
		return nil, TransactionLimitExceededError{Reason: LimitExceededPerTransaction}
	}
	dailyStart, monthlyStart, err := s.limitPeriodStarts(ctx, tx)
	if err != nil {
		return nil, err
	}
	usage, err := s.ensureAndLockLimitPeriodUsage(ctx, tx, params, dailyStart, monthlyStart)
	if err != nil {
		return nil, err
	}
	daily := usage["daily"]
	monthly := usage["monthly"]
	if daily.ReservedAmount+daily.ConsumedAmount > limit.DailyLimit-params.Amount {
		return nil, TransactionLimitExceededError{Reason: LimitExceededDaily}
	}
	if monthly.ReservedAmount+monthly.ConsumedAmount > limit.MonthlyLimit-params.Amount {
		return nil, TransactionLimitExceededError{Reason: LimitExceededMonthly}
	}

	stmt := s.DB.Rebind(`INSERT INTO transaction_limit_reservations(
		tenant_id, command_id, wallet_id, transaction_type, currency, amount,
		daily_period_start, monthly_period_start, status
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(tenant_id, command_id) DO NOTHING
	RETURNING *`)
	var reservation LimitUsageReservation
	err = tx.GetContext(ctx, &reservation, stmt,
		params.TenantID,
		params.CommandID,
		params.WalletID,
		params.TransactionType,
		params.Currency,
		params.Amount,
		dailyStart,
		monthlyStart,
		LimitReservationStatusReserved,
	)
	if errors.Is(err, sql.ErrNoRows) {
		existing, loadErr := s.lockLimitReservation(ctx, tx, params.TenantID, params.CommandID)
		if loadErr != nil {
			return nil, loadErr
		}
		if !limitReservationMatches(existing, params) {
			return nil, ErrDuplicateLimitReservation
		}
		return nil, ErrInvalidLimitUsage
	}
	if err != nil {
		return nil, err
	}
	if err := s.addReservedLimitUsage(ctx, tx, &reservation); err != nil {
		return nil, err
	}
	return &reservation, nil
}

func (s *Store) consumeLimitUsageInTx(ctx context.Context, tx *sqlx.Tx, params ConsumeLimitUsageParams) error {
	reservation, err := s.lockLimitReservation(
		ctx,
		tx,
		params.Reservation.TenantID,
		params.Reservation.CommandID,
	)
	if err != nil {
		return err
	}
	if !limitReservationMatches(reservation, params.Reservation) {
		return ErrDuplicateLimitReservation
	}
	switch reservation.Status {
	case LimitReservationStatusConsumed:
		if reservation.LedgerTransactionID.Valid && reservation.LedgerTransactionID.Int64 == params.LedgerTransactionID {
			return nil
		}
		return ErrDuplicateLimitReservation
	case LimitReservationStatusReleased:
		return ErrLimitReservationReleased
	case LimitReservationStatusReserved:
	default:
		return ErrInvalidLimitUsage
	}
	if _, err := s.lockReservationPeriodUsage(ctx, tx, reservation); err != nil {
		return err
	}
	if err := s.moveReservedLimitUsage(ctx, tx, reservation, true); err != nil {
		return err
	}
	stmt := s.DB.Rebind(`UPDATE transaction_limit_reservations
		SET status = ?, ledger_transaction_id = ?, consumed_at = clock_timestamp()
		WHERE tenant_id = ? AND id = ? AND status = ?`)
	result, err := tx.ExecContext(ctx, stmt,
		LimitReservationStatusConsumed,
		params.LedgerTransactionID,
		reservation.TenantID,
		reservation.ID,
		LimitReservationStatusReserved,
	)
	if err != nil {
		return err
	}
	return requireOneRow(result)
}

func (s *Store) lockLimitWallet(ctx context.Context, tx *sqlx.Tx, tenantID string, walletID uuid.UUID) (*Wallet, error) {
	stmt := s.DB.Rebind(`SELECT * FROM wallets WHERE tenant_id = ? AND id = ? FOR UPDATE`)
	var wallet Wallet
	if err := tx.GetContext(ctx, &wallet, stmt, tenantID, walletID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrWalletNotFound
		}
		return nil, err
	}
	return &wallet, nil
}

func (s *Store) loadActiveTransactionLimit(
	ctx context.Context,
	tx *sqlx.Tx,
	params LimitUsageParams,
	kycTier string,
	currencyUnitID int64,
) (*TransactionLimit, error) {
	if err := ValidateCurrencyUnitID(currencyUnitID); err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind(`SELECT * FROM transaction_limits
		WHERE tenant_id = ? AND kyc_tier = ? AND transaction_type = ? AND currency = ?
		AND currency_unit_version_id = ? AND is_active = TRUE`)
	var limit TransactionLimit
	if err := tx.GetContext(ctx, &limit, stmt,
		params.TenantID,
		kycTier,
		params.TransactionType,
		params.Currency,
		currencyUnitID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrTransactionLimitNotFound
		}
		return nil, err
	}
	return &limit, nil
}

func (s *Store) limitPeriodStarts(ctx context.Context, tx *sqlx.Tx) (time.Time, time.Time, error) {
	var dailyStart time.Time
	var monthlyStart time.Time
	err := tx.QueryRowxContext(ctx, `WITH instant AS (
		SELECT clock_timestamp() AT TIME ZONE 'UTC' AS value
	)
	SELECT value::date, date_trunc('month', value)::date FROM instant`).Scan(&dailyStart, &monthlyStart)
	return dailyStart, monthlyStart, err
}

func (s *Store) ensureAndLockLimitPeriodUsage(
	ctx context.Context,
	tx *sqlx.Tx,
	params LimitUsageParams,
	dailyStart time.Time,
	monthlyStart time.Time,
) (map[string]limitPeriodUsage, error) {
	insert := s.DB.Rebind(`INSERT INTO transaction_limit_period_usage(
		tenant_id, wallet_id, transaction_type, currency, period_kind, period_start
	) VALUES(?, ?, ?, ?, ?, ?)
	ON CONFLICT DO NOTHING`)
	periods := []struct {
		kind  string
		start time.Time
	}{
		{kind: "daily", start: dailyStart},
		{kind: "monthly", start: monthlyStart},
	}
	for _, period := range periods {
		if _, err := tx.ExecContext(ctx, insert,
			params.TenantID,
			params.WalletID,
			params.TransactionType,
			params.Currency,
			period.kind,
			period.start,
		); err != nil {
			return nil, err
		}
	}
	stmt := s.DB.Rebind(`SELECT period_kind, reserved_amount, consumed_amount
		FROM transaction_limit_period_usage
		WHERE tenant_id = ? AND wallet_id = ? AND transaction_type = ? AND currency = ?
		  AND ((period_kind = 'daily' AND period_start = ?)
		    OR (period_kind = 'monthly' AND period_start = ?))
		ORDER BY period_kind
		FOR UPDATE`)
	var rows []limitPeriodUsage
	if err := tx.SelectContext(ctx, &rows, stmt,
		params.TenantID,
		params.WalletID,
		params.TransactionType,
		params.Currency,
		dailyStart,
		monthlyStart,
	); err != nil {
		return nil, err
	}
	if len(rows) != 2 {
		return nil, ErrInvalidLimitUsage
	}
	usage := make(map[string]limitPeriodUsage, 2)
	for _, row := range rows {
		usage[row.PeriodKind] = row
	}
	if _, ok := usage["daily"]; !ok {
		return nil, ErrInvalidLimitUsage
	}
	if _, ok := usage["monthly"]; !ok {
		return nil, ErrInvalidLimitUsage
	}
	return usage, nil
}

func (s *Store) lockReservationPeriodUsage(
	ctx context.Context,
	tx *sqlx.Tx,
	reservation *LimitUsageReservation,
) (map[string]limitPeriodUsage, error) {
	stmt := s.DB.Rebind(`SELECT period_kind, reserved_amount, consumed_amount
		FROM transaction_limit_period_usage
		WHERE tenant_id = ? AND wallet_id = ? AND transaction_type = ? AND currency = ?
		  AND ((period_kind = 'daily' AND period_start = ?)
		    OR (period_kind = 'monthly' AND period_start = ?))
		ORDER BY period_kind
		FOR UPDATE`)
	var rows []limitPeriodUsage
	if err := tx.SelectContext(ctx, &rows, stmt,
		reservation.TenantID,
		reservation.WalletID,
		reservation.TransactionType,
		reservation.Currency,
		reservation.DailyPeriodStart,
		reservation.MonthlyPeriodStart,
	); err != nil {
		return nil, err
	}
	if len(rows) != 2 {
		return nil, ErrInvalidLimitUsage
	}
	usage := make(map[string]limitPeriodUsage, 2)
	for _, row := range rows {
		usage[row.PeriodKind] = row
	}
	return usage, nil
}

func (s *Store) addReservedLimitUsage(ctx context.Context, tx *sqlx.Tx, reservation *LimitUsageReservation) error {
	stmt := s.DB.Rebind(`UPDATE transaction_limit_period_usage
		SET reserved_amount = reserved_amount + ?, updated_at = clock_timestamp()
		WHERE tenant_id = ? AND wallet_id = ? AND transaction_type = ? AND currency = ?
		  AND ((period_kind = 'daily' AND period_start = ?)
		    OR (period_kind = 'monthly' AND period_start = ?))`)
	result, err := tx.ExecContext(ctx, stmt,
		reservation.Amount,
		reservation.TenantID,
		reservation.WalletID,
		reservation.TransactionType,
		reservation.Currency,
		reservation.DailyPeriodStart,
		reservation.MonthlyPeriodStart,
	)
	if err != nil {
		return err
	}
	return requireRows(result, 2)
}

func (s *Store) moveReservedLimitUsage(
	ctx context.Context,
	tx *sqlx.Tx,
	reservation *LimitUsageReservation,
	consume bool,
) error {
	consumedIncrement := int64(0)
	if consume {
		consumedIncrement = reservation.Amount
	}
	stmt := s.DB.Rebind(`UPDATE transaction_limit_period_usage
		SET reserved_amount = reserved_amount - ?,
		    consumed_amount = consumed_amount + ?,
		    updated_at = clock_timestamp()
		WHERE tenant_id = ? AND wallet_id = ? AND transaction_type = ? AND currency = ?
		  AND reserved_amount >= ?
		  AND ((period_kind = 'daily' AND period_start = ?)
		    OR (period_kind = 'monthly' AND period_start = ?))`)
	result, err := tx.ExecContext(ctx, stmt,
		reservation.Amount,
		consumedIncrement,
		reservation.TenantID,
		reservation.WalletID,
		reservation.TransactionType,
		reservation.Currency,
		reservation.Amount,
		reservation.DailyPeriodStart,
		reservation.MonthlyPeriodStart,
	)
	if err != nil {
		return err
	}
	return requireRows(result, 2)
}

func (s *Store) findLimitReservationForUpdate(
	ctx context.Context,
	tx *sqlx.Tx,
	tenantID string,
	commandID string,
) (*LimitUsageReservation, error) {
	stmt := s.DB.Rebind(`SELECT * FROM transaction_limit_reservations
		WHERE tenant_id = ? AND command_id = ? FOR UPDATE`)
	var reservation LimitUsageReservation
	if err := tx.GetContext(ctx, &reservation, stmt, tenantID, commandID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &reservation, nil
}

func (s *Store) lockLimitReservation(
	ctx context.Context,
	tx *sqlx.Tx,
	tenantID string,
	commandID string,
) (*LimitUsageReservation, error) {
	reservation, err := s.findLimitReservationForUpdate(ctx, tx, tenantID, commandID)
	if err != nil {
		return nil, err
	}
	if reservation == nil {
		return nil, ErrLimitReservationNotFound
	}
	return reservation, nil
}

func limitReservationMatches(reservation *LimitUsageReservation, params LimitUsageParams) bool {
	return reservation != nil &&
		reservation.TenantID == params.TenantID &&
		reservation.CommandID == params.CommandID &&
		reservation.WalletID == params.WalletID &&
		reservation.TransactionType == params.TransactionType &&
		reservation.Currency == params.Currency &&
		reservation.Amount == params.Amount
}

func requireOneRow(result sql.Result) error {
	return requireRows(result, 1)
}

func requireRows(result sql.Result, expected int64) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != expected {
		return ErrInvalidLimitUsage
	}
	return nil
}
