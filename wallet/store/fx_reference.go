package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

const (
	FXSideMid            = "mid"
	FXSideBid            = "bid"
	FXSideAsk            = "ask"
	FXSideFixedReference = "fixed_reference"

	FXPurposeReference  = "reference"
	FXPurposeTax        = "tax"
	FXPurposeExecutable = "executable"
)

type FXSource struct {
	ID            int64     `db:"id"`
	Code          string    `db:"code"`
	DisplayName   string    `db:"display_name"`
	Provider      string    `db:"provider"`
	Purpose       string    `db:"purpose"`
	SourceURL     string    `db:"source_url"`
	MaxAgeSeconds int       `db:"max_age_seconds"`
	IsEnabled     bool      `db:"is_enabled"`
	CreatedAt     time.Time `db:"created_at"`
	UpdatedAt     time.Time `db:"updated_at"`
}

type FXSourcePair struct {
	ID                int64     `db:"id"`
	SourceID          int64     `db:"source_id"`
	BaseCurrencyCode  string    `db:"base_currency_code"`
	QuoteCurrencyCode string    `db:"quote_currency_code"`
	ExternalSeries    string    `db:"external_series"`
	IsEnabled         bool      `db:"is_enabled"`
	CreatedAt         time.Time `db:"created_at"`
}

type FXSourcePairSide struct {
	SourcePairID int64  `db:"source_pair_id"`
	Side         string `db:"side"`
}

type FXObservation struct {
	ID                  int64           `db:"id"`
	SourceID            int64           `db:"source_id"`
	SourcePairID        int64           `db:"source_pair_id"`
	ExternalSeries      string          `db:"external_series"`
	BaseCurrencyCode    string          `db:"base_currency_code"`
	QuoteCurrencyCode   string          `db:"quote_currency_code"`
	BaseCurrencyUnitID  int64           `db:"base_currency_unit_id"`
	QuoteCurrencyUnitID int64           `db:"quote_currency_unit_id"`
	Rate                decimal.Decimal `db:"rate"`
	Side                string          `db:"side"`
	Purpose             string          `db:"purpose"`
	ObservationAt       time.Time       `db:"observation_at"`
	PublishedAt         sql.NullTime    `db:"published_at"`
	RetrievedAt         time.Time       `db:"retrieved_at"`
	ExpiresAt           time.Time       `db:"expires_at"`
	RawPayloadSHA256    string          `db:"raw_payload_sha256"`
	SourceRevision      string          `db:"source_revision"`
	CreatedAt           time.Time       `db:"created_at"`
}

type FXObservationInput struct {
	SourceID            int64
	SourcePairID        int64
	ExternalSeries      string
	BaseCurrencyCode    string
	QuoteCurrencyCode   string
	BaseCurrencyUnitID  int64
	QuoteCurrencyUnitID int64
	Rate                decimal.Decimal
	Side                string
	Purpose             string
	ObservationAt       time.Time
	PublishedAt         sql.NullTime
	RetrievedAt         time.Time
	ExpiresAt           time.Time
	RawPayloadSHA256    string
	SourceRevision      string
}

type FXRateObservation struct {
	Observation *FXObservation
	BaseUnit    *CurrencyUnitVersion
	QuoteUnit   *CurrencyUnitVersion
	Inverse     bool
}

type MoneyConversionQuote struct {
	ID                             uuid.UUID `db:"id"`
	TenantID                       string    `db:"tenant_id"`
	RequestedByUserID              int64     `db:"requested_by_user_id"`
	IdempotencyKey                 string    `db:"idempotency_key"`
	ObservationID                  int64     `db:"observation_id"`
	ObservationBaseCurrencyUnitID  int64     `db:"observation_base_currency_unit_id"`
	ObservationQuoteCurrencyUnitID int64     `db:"observation_quote_currency_unit_id"`
	ObservationBaseCurrencyCode    string    `db:"observation_base_currency_code"`
	ObservationQuoteCurrencyCode   string    `db:"observation_quote_currency_code"`
	ObservationExpiresAt           time.Time `db:"observation_expires_at"`
	InputCurrencyUnitID            int64     `db:"input_currency_unit_id"`
	OutputCurrencyUnitID           int64     `db:"output_currency_unit_id"`
	InputCurrencyCode              string    `db:"input_currency_code"`
	OutputCurrencyCode             string    `db:"output_currency_code"`
	InputMinorUnits                int64     `db:"input_minor_units"`
	OutputMinorUnits               int64     `db:"output_minor_units"`
	Inverse                        bool      `db:"inverse"`
	RoundingMode                   string    `db:"rounding_mode"`
	ConversionAt                   time.Time `db:"conversion_at"`
	CreatedAt                      time.Time `db:"created_at"`
	ExpiresAt                      time.Time `db:"expires_at"`
}

type MoneyConversionQuoteInput struct {
	TenantID                       string
	RequestedByUserID              int64
	IdempotencyKey                 string
	MaxQuotesPerObservation        int
	ObservationID                  int64
	ObservationBaseCurrencyUnitID  int64
	ObservationQuoteCurrencyUnitID int64
	ObservationBaseCurrencyCode    string
	ObservationQuoteCurrencyCode   string
	ObservationExpiresAt           time.Time
	InputCurrencyUnitID            int64
	OutputCurrencyUnitID           int64
	InputCurrencyCode              string
	OutputCurrencyCode             string
	InputMinorUnits                int64
	OutputMinorUnits               int64
	Inverse                        bool
	RoundingMode                   string
	ConversionAt                   time.Time
	ExpiresAt                      time.Time
}

func validateFXSourceCode(code string) error {
	if code == "" {
		return ErrMissingFXSource
	}
	if len(code) > 63 || code[0] < 'a' || code[0] > 'z' {
		return ErrInvalidFXSource
	}
	previousDash := false
	for _, character := range code {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			previousDash = false
			continue
		}
		if character == '-' && !previousDash {
			previousDash = true
			continue
		}
		return ErrInvalidFXSource
	}
	if previousDash {
		return ErrInvalidFXSource
	}
	return nil
}

func validFXSide(side string) bool {
	switch side {
	case FXSideMid, FXSideBid, FXSideAsk, FXSideFixedReference:
		return true
	default:
		return false
	}
}

func validFXPurpose(purpose string) bool {
	switch purpose {
	case FXPurposeReference, FXPurposeTax, FXPurposeExecutable:
		return true
	default:
		return false
	}
}

func (s *Store) ListEnabledFXSources(ctx context.Context) ([]FXSource, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var sources []FXSource
	if err := db.SelectContext(ctx, &sources, `SELECT * FROM fx_sources WHERE is_enabled = TRUE ORDER BY code`); err != nil {
		return nil, err
	}
	return sources, nil
}

func (s *Store) GetFXSource(ctx context.Context, sourceCode string) (*FXSource, error) {
	if err := validateFXSourceCode(sourceCode); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var source FXSource
	if err := db.GetContext(ctx, &source, db.Rebind(`SELECT * FROM fx_sources WHERE code = ?`), sourceCode); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrFXSourceNotFound
		}
		return nil, err
	}
	return &source, nil
}

func (s *Store) ListEnabledFXSourcePairs(ctx context.Context, sourceID int64) ([]FXSourcePair, error) {
	if sourceID <= 0 {
		return nil, ErrMissingFXSource
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var pairs []FXSourcePair
	query := db.Rebind(`SELECT * FROM fx_source_pairs
		WHERE source_id = ? AND is_enabled = TRUE
		ORDER BY base_currency_code, quote_currency_code`)
	if err := db.SelectContext(ctx, &pairs, query, sourceID); err != nil {
		return nil, err
	}
	return pairs, nil
}

func (s *Store) ListEnabledFXSourcePairSides(ctx context.Context, sourceID int64) ([]FXSourcePairSide, error) {
	if sourceID <= 0 {
		return nil, ErrMissingFXSource
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var sides []FXSourcePairSide
	query := db.Rebind(`SELECT configured.source_pair_id, configured.side
		FROM fx_source_pair_sides configured
		JOIN fx_source_pairs pair ON pair.id = configured.source_pair_id
		WHERE pair.source_id = ? AND pair.is_enabled = TRUE
		ORDER BY configured.source_pair_id, configured.side`)
	if err := db.SelectContext(ctx, &sides, query, sourceID); err != nil {
		return nil, err
	}
	return sides, nil
}

func validateFXObservationInput(input FXObservationInput) error {
	if input.SourceID <= 0 {
		return ErrMissingFXSource
	}
	if input.SourcePairID <= 0 {
		return ErrMissingFXSourcePair
	}
	if strings.TrimSpace(input.ExternalSeries) == "" || input.ExternalSeries != strings.TrimSpace(input.ExternalSeries) {
		return ErrMissingFXSourcePair
	}
	if _, err := ValidateCurrencyCode(input.BaseCurrencyCode); err != nil {
		return err
	}
	if _, err := ValidateCurrencyCode(input.QuoteCurrencyCode); err != nil {
		return err
	}
	if input.BaseCurrencyCode == input.QuoteCurrencyCode {
		return ErrIdenticalCurrencies
	}
	if err := ValidateCurrencyUnitID(input.BaseCurrencyUnitID); err != nil {
		return err
	}
	if err := ValidateCurrencyUnitID(input.QuoteCurrencyUnitID); err != nil {
		return err
	}
	if input.Rate.Cmp(decimal.Zero) <= 0 {
		return ErrInvalidRate
	}
	if !rateFitsNumeric38Scale18(input.Rate) {
		return ErrRateNotRepresentable
	}
	if input.Side == "" {
		return ErrMissingRateSide
	}
	if !validFXSide(input.Side) {
		return ErrInvalidRateSide
	}
	if input.Purpose == "" {
		return ErrMissingRatePurpose
	}
	if !validFXPurpose(input.Purpose) {
		return ErrInvalidRatePurpose
	}
	if input.ObservationAt.IsZero() {
		return ErrMissingObservationTime
	}
	if input.RetrievedAt.IsZero() {
		return ErrMissingRetrievalTime
	}
	if input.ExpiresAt.IsZero() {
		return ErrMissingExpiryTime
	}
	if input.RetrievedAt.Before(input.ObservationAt) || !input.ExpiresAt.After(input.ObservationAt) {
		return ErrInvalidTimeRange
	}
	if !postgresTimestampExact(input.ObservationAt) ||
		!postgresTimestampExact(input.RetrievedAt) ||
		!postgresTimestampExact(input.ExpiresAt) ||
		(input.PublishedAt.Valid && !postgresTimestampExact(input.PublishedAt.Time)) {
		return ErrInvalidTimeRange
	}
	if input.PublishedAt.Valid && (input.PublishedAt.Time.Before(input.ObservationAt) || input.PublishedAt.Time.After(input.RetrievedAt)) {
		return ErrInvalidTimeRange
	}
	if input.RawPayloadSHA256 == "" {
		return ErrMissingPayloadHash
	}
	decodedHash, err := hex.DecodeString(input.RawPayloadSHA256)
	if err != nil || len(decodedHash) != 32 || input.RawPayloadSHA256 != strings.ToLower(input.RawPayloadSHA256) {
		return ErrInvalidPayloadHash
	}
	if strings.TrimSpace(input.SourceRevision) == "" || input.SourceRevision != strings.TrimSpace(input.SourceRevision) {
		return ErrMissingSourceRevision
	}
	return nil
}

func postgresTimestampExact(value time.Time) bool {
	return !value.IsZero() && value.Nanosecond()%int(time.Microsecond) == 0
}

func rateFitsNumeric38Scale18(rate decimal.Decimal) bool {
	return decimalFitsNumeric(rate, 38, 18)
}

func (s *Store) CreateFXObservation(ctx context.Context, input FXObservationInput) (*FXObservation, error) {
	if err := validateFXObservationInput(input); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	query := db.Rebind(`INSERT INTO fx_observations(
			source_id, source_pair_id, external_series, base_currency_code, quote_currency_code,
			base_currency_unit_id, quote_currency_unit_id, rate, side, purpose,
			observation_at, published_at, retrieved_at, expires_at,
			raw_payload_sha256, source_revision
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (source_id, external_series, observation_at, side, raw_payload_sha256)
		DO NOTHING
		RETURNING *`)
	var observation FXObservation
	err = db.GetContext(ctx, &observation, query,
		input.SourceID,
		input.SourcePairID,
		input.ExternalSeries,
		input.BaseCurrencyCode,
		input.QuoteCurrencyCode,
		input.BaseCurrencyUnitID,
		input.QuoteCurrencyUnitID,
		input.Rate,
		input.Side,
		input.Purpose,
		input.ObservationAt.UTC(),
		input.PublishedAt,
		input.RetrievedAt.UTC(),
		input.ExpiresAt.UTC(),
		input.RawPayloadSHA256,
		input.SourceRevision,
	)
	if errors.Is(err, sql.ErrNoRows) {
		lookup := db.Rebind(`SELECT * FROM fx_observations
			WHERE source_id = ? AND external_series = ? AND observation_at = ?
			AND side = ? AND raw_payload_sha256 = ?`)
		err = db.GetContext(ctx, &observation, lookup,
			input.SourceID,
			input.ExternalSeries,
			input.ObservationAt.UTC(),
			input.Side,
			input.RawPayloadSHA256,
		)
	}
	if err != nil {
		return nil, err
	}
	if !fxObservationMatchesInput(observation, input) {
		return nil, ErrFXObservationConflict
	}
	return &observation, nil
}

func fxObservationMatchesInput(stored FXObservation, input FXObservationInput) bool {
	// RetrievedAt is the provider-fetch completion time for the activity
	// attempt, not the publisher's observation time. A Temporal retry after a
	// partial write necessarily has a later completion time; the first
	// successfully persisted time remains authoritative while every material
	// observation/provenance field must still match.
	return stored.SourceID == input.SourceID &&
		stored.SourcePairID == input.SourcePairID &&
		stored.ExternalSeries == input.ExternalSeries &&
		stored.BaseCurrencyCode == input.BaseCurrencyCode &&
		stored.QuoteCurrencyCode == input.QuoteCurrencyCode &&
		stored.BaseCurrencyUnitID == input.BaseCurrencyUnitID &&
		stored.QuoteCurrencyUnitID == input.QuoteCurrencyUnitID &&
		stored.Rate.Equal(input.Rate) &&
		stored.Side == input.Side &&
		stored.Purpose == input.Purpose &&
		stored.ObservationAt.Equal(input.ObservationAt.UTC()) &&
		nullTimesEqual(stored.PublishedAt, input.PublishedAt) &&
		stored.ExpiresAt.Equal(input.ExpiresAt.UTC()) &&
		stored.RawPayloadSHA256 == input.RawPayloadSHA256 &&
		stored.SourceRevision == input.SourceRevision
}

func nullTimesEqual(left, right sql.NullTime) bool {
	return left.Valid == right.Valid && (!left.Valid || left.Time.Equal(right.Time))
}

func (s *Store) GetLatestFXObservation(ctx context.Context, sourceCode, baseCurrency, quoteCurrency, side string, asOf time.Time) (*FXRateObservation, error) {
	if err := validateFXSourceCode(sourceCode); err != nil {
		return nil, err
	}
	if _, err := ValidateCurrencyCode(baseCurrency); err != nil {
		return nil, err
	}
	if _, err := ValidateCurrencyCode(quoteCurrency); err != nil {
		return nil, err
	}
	if baseCurrency == quoteCurrency {
		return nil, ErrIdenticalCurrencies
	}
	if side == "" {
		return nil, ErrMissingRateSide
	}
	if !validFXSide(side) {
		return nil, ErrInvalidRateSide
	}
	reverseSide := inverseFXSide(side)
	if asOf.IsZero() {
		return nil, ErrMissingStartTime
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	query := db.Rebind(`SELECT observation.*
	FROM fx_observations observation
	JOIN fx_source_pairs pair ON pair.id = observation.source_pair_id
	JOIN fx_sources source ON source.id = observation.source_id
	WHERE source.code = ?
	  AND source.is_enabled = TRUE
	  AND pair.is_enabled = TRUE
	  AND observation.observation_at <= ?
	  AND observation.retrieved_at <= ?
	  AND observation.created_at <= ?
	  AND (
		(pair.base_currency_code = ? AND pair.quote_currency_code = ? AND observation.side = ?)
		OR
		(pair.base_currency_code = ? AND pair.quote_currency_code = ? AND observation.side = ?)
	  )
	ORDER BY
	  CASE WHEN observation.expires_at > ? THEN 0 ELSE 1 END,
	  CASE WHEN pair.base_currency_code = ? AND pair.quote_currency_code = ? THEN 0 ELSE 1 END,
	  observation.observation_at DESC,
	  observation.retrieved_at DESC,
	  observation.id DESC
	LIMIT 1`)
	var observation FXObservation
	if err := db.GetContext(ctx, &observation, query,
		sourceCode,
		asOf.UTC(),
		asOf.UTC(),
		asOf.UTC(),
		baseCurrency,
		quoteCurrency,
		side,
		quoteCurrency,
		baseCurrency,
		reverseSide,
		asOf.UTC(),
		baseCurrency,
		quoteCurrency,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrFXObservationNotFound
		}
		return nil, err
	}
	if !observation.ExpiresAt.After(asOf.UTC()) {
		return nil, ErrStaleFXObservation
	}
	baseUnit, err := s.GetCurrencyUnitByID(ctx, observation.BaseCurrencyUnitID)
	if err != nil {
		return nil, err
	}
	quoteUnit, err := s.GetCurrencyUnitByID(ctx, observation.QuoteCurrencyUnitID)
	if err != nil {
		return nil, err
	}
	return &FXRateObservation{
		Observation: &observation,
		BaseUnit:    baseUnit,
		QuoteUnit:   quoteUnit,
		Inverse:     observation.BaseCurrencyCode != baseCurrency,
	}, nil
}

func inverseFXSide(side string) string {
	switch side {
	case FXSideBid:
		return FXSideAsk
	case FXSideAsk:
		return FXSideBid
	default:
		return side
	}
}

func (s *Store) GetFXObservationByID(ctx context.Context, observationID int64) (*FXObservation, error) {
	if observationID <= 0 {
		return nil, ErrMissingFXObservation
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var observation FXObservation
	if err := db.GetContext(ctx, &observation, db.Rebind(`SELECT * FROM fx_observations WHERE id = ?`), observationID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrFXObservationNotFound
		}
		return nil, err
	}
	return &observation, nil
}

func (s *Store) GetFXSourceByID(ctx context.Context, sourceID int64) (*FXSource, error) {
	if sourceID <= 0 {
		return nil, ErrMissingFXSource
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var source FXSource
	if err := db.GetContext(ctx, &source, db.Rebind(`SELECT * FROM fx_sources WHERE id = ?`), sourceID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrFXSourceNotFound
		}
		return nil, err
	}
	return &source, nil
}

func validStoredRoundingMode(mode string) bool {
	switch mode {
	case "half_even", "half_away_from_zero", "toward_zero", "floor", "ceiling":
		return true
	default:
		return false
	}
}

func validateMoneyConversionQuoteInput(input MoneyConversionQuoteInput) error {
	if _, err := ValidateTenantID(input.TenantID); err != nil {
		return err
	}
	if input.RequestedByUserID <= 0 {
		return ErrInvalidUserID
	}
	if err := ValidateIdempotencyKey(input.IdempotencyKey); err != nil {
		return err
	}
	if input.MaxQuotesPerObservation <= 0 {
		return ErrInvalidLimit
	}
	if input.ObservationID <= 0 {
		return ErrMissingFXObservation
	}
	for _, currencyUnitID := range []int64{
		input.ObservationBaseCurrencyUnitID,
		input.ObservationQuoteCurrencyUnitID,
		input.InputCurrencyUnitID,
		input.OutputCurrencyUnitID,
	} {
		if err := ValidateCurrencyUnitID(currencyUnitID); err != nil {
			return err
		}
	}
	if input.InputCurrencyUnitID == input.OutputCurrencyUnitID {
		return ErrIdenticalCurrencies
	}
	for _, code := range []string{
		input.ObservationBaseCurrencyCode,
		input.ObservationQuoteCurrencyCode,
		input.InputCurrencyCode,
		input.OutputCurrencyCode,
	} {
		if _, err := ValidateCurrencyCode(code); err != nil {
			return err
		}
	}
	if input.ObservationBaseCurrencyCode == input.ObservationQuoteCurrencyCode || input.InputCurrencyCode == input.OutputCurrencyCode {
		return ErrIdenticalCurrencies
	}
	if (!input.Inverse && (input.InputCurrencyCode != input.ObservationBaseCurrencyCode || input.OutputCurrencyCode != input.ObservationQuoteCurrencyCode)) ||
		(input.Inverse && (input.InputCurrencyCode != input.ObservationQuoteCurrencyCode || input.OutputCurrencyCode != input.ObservationBaseCurrencyCode)) {
		return ErrCurrencyMismatch
	}
	if input.InputMinorUnits <= 0 || input.OutputMinorUnits < 0 {
		return ErrInvalidAmount
	}
	if input.RoundingMode == "" {
		return ErrMissingRoundingMode
	}
	if !validStoredRoundingMode(input.RoundingMode) {
		return ErrInvalidRoundingMode
	}
	if input.ConversionAt.IsZero() {
		return ErrMissingStartTime
	}
	if input.ExpiresAt.IsZero() {
		return ErrMissingExpiryTime
	}
	if input.ObservationExpiresAt.IsZero() || !input.ExpiresAt.Equal(input.ObservationExpiresAt) {
		return ErrInvalidTimeRange
	}
	if !postgresTimestampExact(input.ConversionAt) ||
		!postgresTimestampExact(input.ExpiresAt) ||
		!postgresTimestampExact(input.ObservationExpiresAt) {
		return ErrInvalidTimeRange
	}
	if !input.ExpiresAt.After(input.ConversionAt) {
		return ErrInvalidTimeRange
	}
	return nil
}

func (s *Store) CreateMoneyConversionQuote(ctx context.Context, input MoneyConversionQuoteInput) (*MoneyConversionQuote, error) {
	if err := validateMoneyConversionQuoteInput(input); err != nil {
		return nil, err
	}
	tenantID, err := ValidateTenantID(input.TenantID)
	if err != nil {
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

	// A request key is serialized independently of the selected observation so
	// concurrent retries cannot create different quotes when a source refresh
	// races the request. A second scope lock makes the configured per-user,
	// per-observation quota exact across all API replicas.
	idempotencyScope := "money-quote-idempotency:" + tenantID + ":" +
		strconv.FormatInt(input.RequestedByUserID, 10) + ":" + input.IdempotencyKey
	if _, err := tx.ExecContext(ctx, db.Rebind(
		`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`,
	), idempotencyScope); err != nil {
		return nil, err
	}

	lookup := db.Rebind(`SELECT * FROM money_conversion_quotes
		WHERE tenant_id = ? AND requested_by_user_id = ? AND idempotency_key = ?`)
	var existing MoneyConversionQuote
	if err := tx.GetContext(ctx, &existing, lookup,
		tenantID, input.RequestedByUserID, input.IdempotencyKey,
	); err == nil {
		return &existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	quotaScope := "money-quote-quota:" + tenantID + ":" +
		strconv.FormatInt(input.RequestedByUserID, 10) + ":" +
		strconv.FormatInt(input.ObservationID, 10)
	if _, err := tx.ExecContext(ctx, db.Rebind(
		`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`,
	), quotaScope); err != nil {
		return nil, err
	}
	var quoteCount int
	if err := tx.GetContext(ctx, &quoteCount, db.Rebind(`SELECT count(*)
		FROM money_conversion_quotes
		WHERE tenant_id = ? AND requested_by_user_id = ? AND observation_id = ?`),
		tenantID, input.RequestedByUserID, input.ObservationID,
	); err != nil {
		return nil, err
	}
	if quoteCount >= input.MaxQuotesPerObservation {
		return nil, ErrConversionQuoteLimitExceeded
	}

	query := db.Rebind(`INSERT INTO money_conversion_quotes(
		tenant_id, requested_by_user_id, idempotency_key, observation_id,
		observation_base_currency_unit_id, observation_quote_currency_unit_id,
		observation_base_currency_code, observation_quote_currency_code, observation_expires_at,
		input_currency_unit_id, output_currency_unit_id,
		input_currency_code, output_currency_code,
		input_minor_units, output_minor_units, inverse, rounding_mode, conversion_at, expires_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	RETURNING *`)
	var quote MoneyConversionQuote
	if err := tx.GetContext(ctx, &quote, query,
		tenantID,
		input.RequestedByUserID,
		input.IdempotencyKey,
		input.ObservationID,
		input.ObservationBaseCurrencyUnitID,
		input.ObservationQuoteCurrencyUnitID,
		input.ObservationBaseCurrencyCode,
		input.ObservationQuoteCurrencyCode,
		input.ObservationExpiresAt.UTC(),
		input.InputCurrencyUnitID,
		input.OutputCurrencyUnitID,
		input.InputCurrencyCode,
		input.OutputCurrencyCode,
		input.InputMinorUnits,
		input.OutputMinorUnits,
		input.Inverse,
		input.RoundingMode,
		input.ConversionAt.UTC(),
		input.ExpiresAt.UTC(),
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &quote, nil
}

func (s *Store) GetMoneyConversionQuoteByIdempotency(
	ctx context.Context,
	tenantID string,
	requestedByUserID int64,
	idempotencyKey string,
) (*MoneyConversionQuote, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if requestedByUserID <= 0 {
		return nil, ErrInvalidUserID
	}
	if err := ValidateIdempotencyKey(idempotencyKey); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	query := db.Rebind(`SELECT * FROM money_conversion_quotes
		WHERE tenant_id = ? AND requested_by_user_id = ? AND idempotency_key = ?`)
	var quote MoneyConversionQuote
	if err := db.GetContext(ctx, &quote, query, tenantID, requestedByUserID, idempotencyKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrConversionQuoteNotFound
		}
		return nil, err
	}
	return &quote, nil
}

func (s *Store) GetMoneyConversionQuote(ctx context.Context, tenantID string, requestedByUserID int64, quoteID uuid.UUID) (*MoneyConversionQuote, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if requestedByUserID <= 0 {
		return nil, ErrInvalidUserID
	}
	if quoteID == uuid.Nil {
		return nil, ErrMissingConversionQuoteID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	query := db.Rebind(`SELECT * FROM money_conversion_quotes
		WHERE tenant_id = ? AND requested_by_user_id = ? AND id = ?`)
	var quote MoneyConversionQuote
	if err := db.GetContext(ctx, &quote, query, tenantID, requestedByUserID, quoteID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrConversionQuoteNotFound
		}
		return nil, err
	}
	return &quote, nil
}
