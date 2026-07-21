package store

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func validatePSPTransactionAmount(amount PSPTransactionAmount) (string, error) {
	tenantID, err := ValidateTenantID(amount.TenantID)
	if err != nil {
		return "", err
	}
	if amount.PSPTransactionID <= 0 {
		return "", ErrMissingPSPTransactionID
	}
	if amount.AmountKind == "" {
		return "", ErrMissingAmountKind
	}
	if !amount.AmountKind.Valid() {
		return "", ErrInvalidAmountKind
	}
	if amount.Amount <= 0 {
		return "", ErrInvalidAmount
	}
	if amount.Currency == "" {
		return "", ErrMissingCurrency
	}
	if err := ValidateCurrencyUnitID(amount.CurrencyUnitID); err != nil {
		return "", err
	}
	if !amount.FxRate.Valid {
		if hasPSPFXMetadata(amount) {
			return "", ErrMissingFXRate
		}
		return tenantID, nil
	}
	if amount.FxRate.Decimal.Cmp(decimal.Zero) <= 0 {
		return "", ErrInvalidRate
	}
	if !decimalFitsNumeric(amount.FxRate.Decimal, 18, 8) {
		return "", ErrPSPFXRateNotRepresentable
	}
	if err := validatePSPFXCurrency(amount.FxBaseCurrency); err != nil {
		return "", err
	}
	if err := validatePSPFXCurrency(amount.FxQuoteCurrency); err != nil {
		return "", err
	}
	if amount.FxBaseCurrency.String == amount.FxQuoteCurrency.String {
		return "", ErrIdenticalCurrencies
	}
	if !amount.FxBaseCurrencyUnitID.Valid || !amount.FxQuoteCurrencyUnitID.Valid {
		return "", ErrMissingCurrencyUnitID
	}
	if err := ValidateCurrencyUnitID(amount.FxBaseCurrencyUnitID.Int64); err != nil {
		return "", err
	}
	if err := ValidateCurrencyUnitID(amount.FxQuoteCurrencyUnitID.Int64); err != nil {
		return "", err
	}
	if !amount.FxSource.Valid || strings.TrimSpace(amount.FxSource.String) == "" {
		return "", ErrMissingFXSource
	}
	if amount.FxSource.String != strings.TrimSpace(amount.FxSource.String) {
		return "", ErrInvalidFXSource
	}
	if !amount.FxConversionAt.Valid || amount.FxConversionAt.Time.IsZero() {
		return "", ErrMissingFXConversionTime
	}
	if amount.FxConversionAt.Time.Nanosecond()%1_000 != 0 {
		return "", ErrInvalidFXConversionTime
	}
	if !pspFXAmountMatchesDeclaredSide(amount) {
		return "", ErrCurrencyMismatch
	}
	if err := validatePSPFXRateFraction(amount.FxRate, amount.FxRateNumerator, amount.FxRateDenominator); err != nil {
		return "", err
	}
	if err := validatePSPFXReferenceMetadata(amount); err != nil {
		return "", err
	}
	return tenantID, nil
}

func hasPSPFXMetadata(amount PSPTransactionAmount) bool {
	return !amount.FxRate.Decimal.IsZero() ||
		amount.FxRateNumerator.Valid || !amount.FxRateNumerator.Decimal.IsZero() ||
		amount.FxRateDenominator.Valid || !amount.FxRateDenominator.Decimal.IsZero() ||
		amount.FxBaseCurrency.Valid || amount.FxBaseCurrency.String != "" ||
		amount.FxQuoteCurrency.Valid || amount.FxQuoteCurrency.String != "" ||
		amount.FxBaseCurrencyUnitID.Valid || amount.FxBaseCurrencyUnitID.Int64 != 0 ||
		amount.FxQuoteCurrencyUnitID.Valid || amount.FxQuoteCurrencyUnitID.Int64 != 0 ||
		amount.FxSource.Valid || amount.FxSource.String != "" ||
		amount.FxObservationID.Valid || amount.FxObservationID.Int64 != 0 ||
		amount.FxQuoteID.Valid || amount.FxQuoteID.UUID != uuid.Nil ||
		amount.FxConversionAt.Valid || !amount.FxConversionAt.Time.IsZero() ||
		amount.FxObservationBaseCurrency.Valid || amount.FxObservationBaseCurrency.String != "" ||
		amount.FxObservationQuoteCurrency.Valid || amount.FxObservationQuoteCurrency.String != "" ||
		amount.FxObservationBaseCurrencyUnitID.Valid || amount.FxObservationBaseCurrencyUnitID.Int64 != 0 ||
		amount.FxObservationQuoteCurrencyUnitID.Valid || amount.FxObservationQuoteCurrencyUnitID.Int64 != 0
}

// CanonicalFXRateFraction returns the exact, reduced rational represented by a
// finite decimal FX rate. Callers at the workflow boundary use this to persist
// an explicit applied-rate identity; the store never derives missing identity
// fields on behalf of a caller.
func CanonicalFXRateFraction(rate decimal.NullDecimal) (decimal.NullDecimal, decimal.NullDecimal, error) {
	if !rate.Valid {
		return decimal.NullDecimal{}, decimal.NullDecimal{}, ErrMissingFXRate
	}
	if rate.Decimal.Cmp(decimal.Zero) <= 0 {
		return decimal.NullDecimal{}, decimal.NullDecimal{}, ErrInvalidRate
	}
	if !decimalFitsNumeric(rate.Decimal, 18, 8) {
		return decimal.NullDecimal{}, decimal.NullDecimal{}, ErrPSPFXRateNotRepresentable
	}
	projected, numerator, denominator, err := CanonicalPSPFXRateSnapshot(rate.Decimal.Rat())
	if err != nil {
		return decimal.NullDecimal{}, decimal.NullDecimal{}, err
	}
	if !projected.Decimal.Equal(rate.Decimal) {
		return decimal.NullDecimal{}, decimal.NullDecimal{}, ErrInvalidFXRateFraction
	}
	return numerator, denominator, nil
}

// CanonicalPSPFXRateSnapshot validates an exact applied rate and returns its
// legacy eight-decimal projection plus canonical reduced numerator and
// denominator. Observation-backed callers must invert the observation as a
// big.Rat first and call this function; deriving the fraction from the rounded
// projection would destroy exact provenance.
func CanonicalPSPFXRateSnapshot(rate *big.Rat) (decimal.NullDecimal, decimal.NullDecimal, decimal.NullDecimal, error) {
	if rate == nil || rate.Sign() <= 0 {
		return decimal.NullDecimal{}, decimal.NullDecimal{}, decimal.NullDecimal{}, ErrInvalidRate
	}
	exact := new(big.Rat).Set(rate)
	numerator := decimal.NewFromBigInt(exact.Num(), 0)
	denominator := decimal.NewFromBigInt(exact.Denom(), 0)
	if !decimalFitsNumeric(numerator, 38, 0) || !decimalFitsNumeric(denominator, 38, 0) {
		return decimal.NullDecimal{}, decimal.NullDecimal{}, decimal.NullDecimal{}, ErrPSPFXRateFractionNotRepresentable
	}
	projected := projectPSPFXRate(exact.Num(), exact.Denom())
	if projected.Cmp(decimal.Zero) <= 0 || !decimalFitsNumeric(projected, 18, 8) {
		return decimal.NullDecimal{}, decimal.NullDecimal{}, decimal.NullDecimal{}, ErrPSPFXRateNotRepresentable
	}
	return decimal.NullDecimal{Decimal: projected, Valid: true},
		decimal.NullDecimal{Decimal: numerator, Valid: true},
		decimal.NullDecimal{Decimal: denominator, Valid: true}, nil
}

func validatePSPFXRateFraction(rate, numerator, denominator decimal.NullDecimal) error {
	if !numerator.Valid || !denominator.Valid {
		return ErrMissingFXRateFraction
	}
	// Reject hostile exponents and oversized coefficients before Rat allocates
	// the corresponding big integers.
	if !decimalFitsNumeric(numerator.Decimal, 38, 0) || !decimalFitsNumeric(denominator.Decimal, 38, 0) {
		return ErrPSPFXRateFractionNotRepresentable
	}
	numeratorInteger, numeratorOK := positiveDecimalInteger(numerator.Decimal)
	denominatorInteger, denominatorOK := positiveDecimalInteger(denominator.Decimal)
	if !numeratorOK || !denominatorOK {
		return ErrInvalidFXRateFraction
	}
	if new(big.Int).GCD(nil, nil, numeratorInteger, denominatorInteger).Cmp(big.NewInt(1)) != 0 {
		return ErrInvalidFXRateFraction
	}
	projected := projectPSPFXRate(numeratorInteger, denominatorInteger)
	if projected.Cmp(decimal.Zero) <= 0 || !decimalFitsNumeric(projected, 18, 8) {
		return ErrPSPFXRateNotRepresentable
	}
	if !rate.Valid || !rate.Decimal.Equal(projected) {
		return ErrInvalidFXRateFraction
	}
	return nil
}

func positiveDecimalInteger(value decimal.Decimal) (*big.Int, bool) {
	if value.Cmp(decimal.Zero) <= 0 {
		return nil, false
	}
	rational := value.Rat()
	if rational.Denom().Cmp(big.NewInt(1)) != 0 {
		return nil, false
	}
	return new(big.Int).Set(rational.Num()), true
}

// projectPSPFXRate is the legacy NUMERIC(18,8) audit/display projection of an
// exact positive applied rate. The database implements the same operation with
// integer div/mod arithmetic so it cannot double-round an intermediate value.
func projectPSPFXRate(numerator, denominator *big.Int) decimal.Decimal {
	scaledNumerator := new(big.Int).Mul(new(big.Int).Set(numerator), big.NewInt(100_000_000))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(scaledNumerator, denominator, remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(denominator) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return decimal.NewFromBigInt(quotient, -8)
}

func pspFXAmountMatchesDeclaredSide(amount PSPTransactionAmount) bool {
	return amount.Currency == amount.FxBaseCurrency.String &&
		amount.CurrencyUnitID == amount.FxBaseCurrencyUnitID.Int64 ||
		amount.Currency == amount.FxQuoteCurrency.String &&
			amount.CurrencyUnitID == amount.FxQuoteCurrencyUnitID.Int64
}

func validatePSPFXReferenceMetadata(amount PSPTransactionAmount) error {
	hasObservationMetadata := amount.FxObservationID.Valid || amount.FxObservationID.Int64 != 0 ||
		amount.FxObservationBaseCurrency.Valid || amount.FxObservationBaseCurrency.String != "" ||
		amount.FxObservationQuoteCurrency.Valid || amount.FxObservationQuoteCurrency.String != "" ||
		amount.FxObservationBaseCurrencyUnitID.Valid || amount.FxObservationBaseCurrencyUnitID.Int64 != 0 ||
		amount.FxObservationQuoteCurrencyUnitID.Valid || amount.FxObservationQuoteCurrencyUnitID.Int64 != 0
	hasQuoteMetadata := amount.FxQuoteID.Valid || amount.FxQuoteID.UUID != uuid.Nil
	if !amount.FxObservationID.Valid {
		if hasObservationMetadata || hasQuoteMetadata {
			return ErrMissingFXObservation
		}
		return nil
	}
	if amount.FxObservationID.Int64 <= 0 {
		return ErrMissingFXObservation
	}
	if err := validatePSPFXCurrency(amount.FxObservationBaseCurrency); err != nil {
		return err
	}
	if err := validatePSPFXCurrency(amount.FxObservationQuoteCurrency); err != nil {
		return err
	}
	if amount.FxObservationBaseCurrency.String == amount.FxObservationQuoteCurrency.String {
		return ErrIdenticalCurrencies
	}
	if !amount.FxObservationBaseCurrencyUnitID.Valid ||
		!amount.FxObservationQuoteCurrencyUnitID.Valid {
		return ErrMissingCurrencyUnitID
	}
	if err := ValidateCurrencyUnitID(amount.FxObservationBaseCurrencyUnitID.Int64); err != nil {
		return err
	}
	if err := ValidateCurrencyUnitID(amount.FxObservationQuoteCurrencyUnitID.Int64); err != nil {
		return err
	}
	direct := amount.FxBaseCurrency.String == amount.FxObservationBaseCurrency.String &&
		amount.FxQuoteCurrency.String == amount.FxObservationQuoteCurrency.String
	inverse := amount.FxBaseCurrency.String == amount.FxObservationQuoteCurrency.String &&
		amount.FxQuoteCurrency.String == amount.FxObservationBaseCurrency.String
	if !direct && !inverse {
		return ErrPSPFXProvenanceMismatch
	}
	if hasQuoteMetadata && (!amount.FxQuoteID.Valid || amount.FxQuoteID.UUID == uuid.Nil) {
		return ErrMissingConversionQuoteID
	}
	return nil
}

func validatePSPFXCurrency(currency sql.NullString) error {
	if !currency.Valid || strings.TrimSpace(currency.String) == "" {
		return ErrMissingFXCurrency
	}
	if currency.String != strings.TrimSpace(currency.String) {
		return ErrInvalidCurrency
	}
	_, err := ValidateCurrencyCode(currency.String)
	return err
}

func buildPSPTransactionAmount(tenantID string, pspTransactionID int64, input PSPTransactionAmountInput) PSPTransactionAmount {
	return PSPTransactionAmount{
		TenantID:                         tenantID,
		PSPTransactionID:                 pspTransactionID,
		AmountKind:                       input.AmountKind,
		Amount:                           input.Amount,
		Currency:                         input.Currency,
		CurrencyUnitID:                   input.CurrencyUnitID,
		FxRate:                           input.FxRate,
		FxRateNumerator:                  input.FxRateNumerator,
		FxRateDenominator:                input.FxRateDenominator,
		FxBaseCurrency:                   nullString(input.FxBaseCurrency),
		FxQuoteCurrency:                  nullString(input.FxQuoteCurrency),
		FxBaseCurrencyUnitID:             nullNonZeroInt64(input.FxBaseCurrencyUnitID),
		FxQuoteCurrencyUnitID:            nullNonZeroInt64(input.FxQuoteCurrencyUnitID),
		FxSource:                         nullString(input.FxSource),
		FxObservationID:                  nullNonZeroInt64(input.FxObservationID),
		FxQuoteID:                        nullUUID(input.FxQuoteID),
		FxConversionAt:                   nullTime(input.FxConversionAt),
		FxObservationBaseCurrency:        nullString(input.FxObservationBaseCurrency),
		FxObservationQuoteCurrency:       nullString(input.FxObservationQuoteCurrency),
		FxObservationBaseCurrencyUnitID:  nullNonZeroInt64(input.FxObservationBaseCurrencyUnitID),
		FxObservationQuoteCurrencyUnitID: nullNonZeroInt64(input.FxObservationQuoteCurrencyUnitID),
	}
}

func nullUUID(value uuid.UUID) uuid.NullUUID {
	if value == uuid.Nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: value, Valid: true}
}

func nullTime(value time.Time) sql.NullTime {
	if value.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: value, Valid: true}
}

func nullNonZeroInt64(value int64) sql.NullInt64 {
	if value == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: value, Valid: true}
}

func nullString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func (s *Store) AddPSPTransactionAmount(ctx context.Context, amount PSPTransactionAmount) (*PSPTransactionAmount, error) {
	tenantID, err := validatePSPTransactionAmount(amount)
	if err != nil {
		return nil, err
	}
	amount.TenantID = tenantID

	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	if err := s.validatePSPTransactionAmountCurrencyUnits(ctx, amount); err != nil {
		return nil, err
	}

	stmt := db.Rebind(`INSERT INTO psp_transaction_amounts(
		tenant_id, psp_transaction_id, amount_kind, amount, currency, currency_unit_version_id,
		fx_rate, fx_rate_numerator, fx_rate_denominator,
		fx_base_currency, fx_quote_currency, fx_base_currency_unit_version_id,
		fx_quote_currency_unit_version_id, fx_source, fx_observation_id, fx_quote_id,
		fx_conversion_at, fx_observation_base_currency, fx_observation_quote_currency,
		fx_observation_base_currency_unit_version_id, fx_observation_quote_currency_unit_version_id
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(tenant_id, psp_transaction_id, amount_kind, currency) DO NOTHING
	RETURNING *`)
	var stored PSPTransactionAmount
	if err := db.GetContext(ctx, &stored, stmt,
		tenantID,
		amount.PSPTransactionID,
		amount.AmountKind,
		amount.Amount,
		amount.Currency,
		amount.CurrencyUnitID,
		amount.FxRate,
		amount.FxRateNumerator,
		amount.FxRateDenominator,
		amount.FxBaseCurrency,
		amount.FxQuoteCurrency,
		amount.FxBaseCurrencyUnitID,
		amount.FxQuoteCurrencyUnitID,
		amount.FxSource,
		amount.FxObservationID,
		amount.FxQuoteID,
		amount.FxConversionAt,
		amount.FxObservationBaseCurrency,
		amount.FxObservationQuoteCurrency,
		amount.FxObservationBaseCurrencyUnitID,
		amount.FxObservationQuoteCurrencyUnitID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			existing, getErr := getPSPTransactionAmount(ctx, db, amount.TenantID, amount.PSPTransactionID, amount.AmountKind, amount.Currency)
			if getErr != nil {
				return nil, getErr
			}
			if err := ValidatePSPTransactionAmountReplay(existing, amount); err != nil {
				return nil, err
			}
			return existing, nil
		}
		return nil, err
	}
	return &stored, nil
}

func (s *Store) AddPSPTransactionAmounts(ctx context.Context, tenantID string, pspTransactionID int64, amounts []PSPTransactionAmountInput) ([]PSPTransactionAmount, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if pspTransactionID <= 0 {
		return nil, ErrMissingPSPTransactionID
	}
	if len(amounts) == 0 {
		return nil, ErrMissingAmounts
	}
	prepared := make([]PSPTransactionAmount, 0, len(amounts))
	for _, input := range amounts {
		amount := buildPSPTransactionAmount(tenantID, pspTransactionID, input)
		if _, err := validatePSPTransactionAmount(amount); err != nil {
			return nil, err
		}
		prepared = append(prepared, amount)
	}

	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	for _, amount := range prepared {
		if err := s.validatePSPTransactionAmountCurrencyUnits(ctx, amount); err != nil {
			return nil, err
		}
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}

	stmt := tx.Rebind(`INSERT INTO psp_transaction_amounts(
		tenant_id, psp_transaction_id, amount_kind, amount, currency, currency_unit_version_id,
		fx_rate, fx_rate_numerator, fx_rate_denominator,
		fx_base_currency, fx_quote_currency, fx_base_currency_unit_version_id,
		fx_quote_currency_unit_version_id, fx_source, fx_observation_id, fx_quote_id,
		fx_conversion_at, fx_observation_base_currency, fx_observation_quote_currency,
		fx_observation_base_currency_unit_version_id, fx_observation_quote_currency_unit_version_id
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(tenant_id, psp_transaction_id, amount_kind, currency) DO NOTHING
	RETURNING *`)
	stored := make([]PSPTransactionAmount, 0, len(prepared))
	for _, amount := range prepared {
		var row PSPTransactionAmount
		if err := tx.GetContext(ctx, &row, stmt,
			amount.TenantID,
			amount.PSPTransactionID,
			amount.AmountKind,
			amount.Amount,
			amount.Currency,
			amount.CurrencyUnitID,
			amount.FxRate,
			amount.FxRateNumerator,
			amount.FxRateDenominator,
			amount.FxBaseCurrency,
			amount.FxQuoteCurrency,
			amount.FxBaseCurrencyUnitID,
			amount.FxQuoteCurrencyUnitID,
			amount.FxSource,
			amount.FxObservationID,
			amount.FxQuoteID,
			amount.FxConversionAt,
			amount.FxObservationBaseCurrency,
			amount.FxObservationQuoteCurrency,
			amount.FxObservationBaseCurrencyUnitID,
			amount.FxObservationQuoteCurrencyUnitID,
		); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				_ = tx.Rollback()
				return nil, err
			}
			existing, getErr := getPSPTransactionAmount(ctx, tx, amount.TenantID, amount.PSPTransactionID, amount.AmountKind, amount.Currency)
			if getErr != nil {
				_ = tx.Rollback()
				return nil, getErr
			}
			if err := ValidatePSPTransactionAmountReplay(existing, amount); err != nil {
				_ = tx.Rollback()
				return nil, err
			}
			row = *existing
		}
		stored = append(stored, row)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return stored, nil
}

func getPSPTransactionAmount(ctx context.Context, q interface {
	Rebind(string) string
	GetContext(context.Context, any, string, ...any) error
}, tenantID string, pspTransactionID int64, amountKind PSPAmountKind, currency string) (*PSPTransactionAmount, error) {
	stmt := q.Rebind(`SELECT * FROM psp_transaction_amounts
		WHERE tenant_id = ? AND psp_transaction_id = ? AND amount_kind = ? AND currency = ?`)
	var amount PSPTransactionAmount
	if err := q.GetContext(ctx, &amount, stmt, tenantID, pspTransactionID, amountKind, currency); err != nil {
		return nil, err
	}
	return &amount, nil
}

func ValidatePSPTransactionAmountReplay(existing *PSPTransactionAmount, requested PSPTransactionAmount) error {
	if existing == nil ||
		existing.TenantID != requested.TenantID ||
		existing.PSPTransactionID != requested.PSPTransactionID ||
		existing.AmountKind != requested.AmountKind ||
		existing.Amount != requested.Amount ||
		existing.Currency != requested.Currency ||
		existing.CurrencyUnitID != requested.CurrencyUnitID ||
		!nullDecimalEqual(existing.FxRate, requested.FxRate) ||
		!nullDecimalEqual(existing.FxRateNumerator, requested.FxRateNumerator) ||
		!nullDecimalEqual(existing.FxRateDenominator, requested.FxRateDenominator) ||
		!nullStringEqual(existing.FxBaseCurrency, requested.FxBaseCurrency) ||
		!nullStringEqual(existing.FxQuoteCurrency, requested.FxQuoteCurrency) ||
		!nullInt64Equal(existing.FxBaseCurrencyUnitID, requested.FxBaseCurrencyUnitID) ||
		!nullInt64Equal(existing.FxQuoteCurrencyUnitID, requested.FxQuoteCurrencyUnitID) ||
		!nullStringEqual(existing.FxSource, requested.FxSource) ||
		!nullInt64Equal(existing.FxObservationID, requested.FxObservationID) ||
		!nullUUIDEqual(existing.FxQuoteID, requested.FxQuoteID) ||
		!nullTimeEqual(existing.FxConversionAt, requested.FxConversionAt) ||
		!nullStringEqual(existing.FxObservationBaseCurrency, requested.FxObservationBaseCurrency) ||
		!nullStringEqual(existing.FxObservationQuoteCurrency, requested.FxObservationQuoteCurrency) ||
		!nullInt64Equal(existing.FxObservationBaseCurrencyUnitID, requested.FxObservationBaseCurrencyUnitID) ||
		!nullInt64Equal(existing.FxObservationQuoteCurrencyUnitID, requested.FxObservationQuoteCurrencyUnitID) {
		return ErrDuplicateAmount
	}
	return nil
}

func (s *Store) validatePSPTransactionAmountCurrencyUnits(ctx context.Context, amount PSPTransactionAmount) error {
	if err := s.validateCurrencyUnitIdentity(ctx, amount.Currency, amount.CurrencyUnitID); err != nil {
		return err
	}
	if !amount.FxRate.Valid {
		return nil
	}
	baseUnit, err := s.getCurrencyUnitIdentity(ctx, amount.FxBaseCurrency.String, amount.FxBaseCurrencyUnitID.Int64)
	if err != nil {
		return err
	}
	quoteUnit, err := s.getCurrencyUnitIdentity(ctx, amount.FxQuoteCurrency.String, amount.FxQuoteCurrencyUnitID.Int64)
	if err != nil {
		return err
	}
	if !currencyUnitEffectiveAt(baseUnit, amount.FxConversionAt.Time) ||
		!currencyUnitEffectiveAt(quoteUnit, amount.FxConversionAt.Time) {
		return ErrInvalidFXConversionTime
	}
	if !amount.FxObservationID.Valid {
		return nil
	}
	observation, err := s.GetFXObservationByID(ctx, amount.FxObservationID.Int64)
	if err != nil {
		return err
	}
	if observation.ID != amount.FxObservationID.Int64 ||
		observation.BaseCurrencyCode != amount.FxObservationBaseCurrency.String ||
		observation.QuoteCurrencyCode != amount.FxObservationQuoteCurrency.String ||
		observation.BaseCurrencyUnitID != amount.FxObservationBaseCurrencyUnitID.Int64 ||
		observation.QuoteCurrencyUnitID != amount.FxObservationQuoteCurrencyUnitID.Int64 {
		return ErrPSPFXProvenanceMismatch
	}
	conversionAt := amount.FxConversionAt.Time
	if observation.ObservationAt.After(conversionAt) || observation.RetrievedAt.After(conversionAt) ||
		observation.CreatedAt.IsZero() || observation.CreatedAt.After(conversionAt) ||
		!observation.ExpiresAt.After(conversionAt) {
		return ErrPSPFXProvenanceMismatch
	}
	source, err := s.GetFXSourceByID(ctx, observation.SourceID)
	if err != nil {
		return err
	}
	if source.ID != observation.SourceID || source.Code != amount.FxSource.String {
		return ErrPSPFXProvenanceMismatch
	}
	direct := amount.FxBaseCurrency.String == observation.BaseCurrencyCode &&
		amount.FxQuoteCurrency.String == observation.QuoteCurrencyCode
	inverse := amount.FxBaseCurrency.String == observation.QuoteCurrencyCode &&
		amount.FxQuoteCurrency.String == observation.BaseCurrencyCode
	if !direct && !inverse {
		return ErrPSPFXProvenanceMismatch
	}
	appliedRate := new(big.Rat).SetFrac(
		amount.FxRateNumerator.Decimal.Rat().Num(),
		amount.FxRateDenominator.Decimal.Rat().Num(),
	)
	expectedRate := observation.Rate.Rat()
	if inverse {
		expectedRate = new(big.Rat).Inv(expectedRate)
	}
	if appliedRate.Cmp(expectedRate) != 0 {
		return ErrPSPFXProvenanceMismatch
	}
	if !amount.FxQuoteID.Valid {
		return nil
	}
	quote, err := s.getPSPFXConversionQuote(ctx, amount.TenantID, amount.FxQuoteID.UUID)
	if err != nil {
		return err
	}
	if quote.ID != amount.FxQuoteID.UUID ||
		quote.TenantID != amount.TenantID ||
		quote.ObservationID != observation.ID ||
		!quote.ConversionAt.Equal(conversionAt) ||
		quote.InputCurrencyCode != amount.FxBaseCurrency.String ||
		quote.OutputCurrencyCode != amount.FxQuoteCurrency.String ||
		quote.InputCurrencyUnitID != amount.FxBaseCurrencyUnitID.Int64 ||
		quote.OutputCurrencyUnitID != amount.FxQuoteCurrencyUnitID.Int64 ||
		quote.Inverse != inverse {
		return ErrPSPFXProvenanceMismatch
	}
	if amount.Currency == amount.FxBaseCurrency.String && amount.CurrencyUnitID == amount.FxBaseCurrencyUnitID.Int64 {
		if amount.Amount != quote.InputMinorUnits {
			return ErrPSPFXProvenanceMismatch
		}
	} else if amount.Amount != quote.OutputMinorUnits {
		return ErrPSPFXProvenanceMismatch
	}
	return nil
}

func (s *Store) getPSPFXConversionQuote(ctx context.Context, tenantID string, quoteID uuid.UUID) (*MoneyConversionQuote, error) {
	if quoteID == uuid.Nil {
		return nil, ErrMissingConversionQuoteID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var quote MoneyConversionQuote
	if err := db.GetContext(ctx, &quote, db.Rebind(`SELECT * FROM money_conversion_quotes
		WHERE tenant_id = ? AND id = ?`), tenantID, quoteID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrConversionQuoteNotFound
		}
		return nil, err
	}
	return &quote, nil
}

func nullDecimalEqual(left, right decimal.NullDecimal) bool {
	return left.Valid == right.Valid && (!left.Valid || left.Decimal.Equal(right.Decimal))
}

func nullTimeEqual(left, right sql.NullTime) bool {
	return left.Valid == right.Valid && (!left.Valid || left.Time.Equal(right.Time))
}

func (s *Store) ListPSPTransactionAmounts(ctx context.Context, tenantID string, pspTransactionID int64) ([]PSPTransactionAmount, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if pspTransactionID <= 0 {
		return nil, ErrMissingPSPTransactionID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT * FROM psp_transaction_amounts
		WHERE tenant_id = ? AND psp_transaction_id = ?
		ORDER BY id`)
	var rows []PSPTransactionAmount
	if err := db.SelectContext(ctx, &rows, stmt, tenantID, pspTransactionID); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Store) ListPSPTransactionAmountsByKind(ctx context.Context, tenantID string, pspTransactionID int64, kind PSPAmountKind) ([]PSPTransactionAmount, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if pspTransactionID <= 0 {
		return nil, ErrMissingPSPTransactionID
	}
	if kind == "" {
		return nil, ErrMissingAmountKind
	}
	if !kind.Valid() {
		return nil, ErrInvalidAmountKind
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT * FROM psp_transaction_amounts
		WHERE tenant_id = ? AND psp_transaction_id = ? AND amount_kind = ?
		ORDER BY id`)
	var rows []PSPTransactionAmount
	if err := db.SelectContext(ctx, &rows, stmt, tenantID, pspTransactionID, kind); err != nil {
		if err == sql.ErrNoRows {
			return []PSPTransactionAmount{}, nil
		}
		return nil, err
	}
	return rows, nil
}
