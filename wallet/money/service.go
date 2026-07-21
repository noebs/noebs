package money

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/adonese/noebs/groosh"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

// Repository is the explicit persistence contract needed by the money
// service. The wallet store implements it; the interface keeps arithmetic
// policy testable without a database.
type Repository interface {
	GetCurrencyUnit(context.Context, string, time.Time) (*walletstore.CurrencyUnitVersion, error)
	GetCurrencyUnitByID(context.Context, int64) (*walletstore.CurrencyUnitVersion, error)
	ListCurrencyUnits(context.Context, time.Time, bool) ([]walletstore.CurrencyUnitVersion, error)
	GetLatestFXObservation(context.Context, string, string, string, string, time.Time) (*walletstore.FXRateObservation, error)
	GetFXObservationByID(context.Context, int64) (*walletstore.FXObservation, error)
	GetFXSourceByID(context.Context, int64) (*walletstore.FXSource, error)
	CreateMoneyConversionQuote(context.Context, walletstore.MoneyConversionQuoteInput) (*walletstore.MoneyConversionQuote, error)
	GetMoneyConversionQuote(context.Context, string, int64, uuid.UUID) (*walletstore.MoneyConversionQuote, error)
	GetMoneyConversionQuoteByIdempotency(context.Context, string, int64, string) (*walletstore.MoneyConversionQuote, error)
}

type Service struct {
	repository Repository
}

type Currency struct {
	Definition walletstore.CurrencyUnitVersion
	Unit       groosh.CurrencyUnit
}

type ConversionQuote struct {
	Quote       walletstore.MoneyConversionQuote
	Input       groosh.Money
	Output      groosh.Money
	Observation walletstore.FXObservation
	Source      walletstore.FXSource
	AppliedRate *big.Rat
}

type QuoteParams struct {
	TenantID                string
	RequestedBy             int64
	IdempotencyKey          string
	MaxQuotesPerObservation int
	SourceCode              string
	BaseCurrency            string
	BaseCurrencyUnitID      int64
	QuoteCurrency           string
	QuoteCurrencyUnitID     int64
	InputMinor              int64
	Side                    string
	RoundingMode            groosh.RoundingMode
	ConversionTime          time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) requireRepository() (Repository, error) {
	if s == nil || s.repository == nil {
		return nil, ErrMissingRepository
	}
	return s.repository, nil
}

func validateQuoteParams(params QuoteParams) error {
	if _, err := walletstore.ValidateTenantID(params.TenantID); err != nil {
		return err
	}
	if params.RequestedBy <= 0 {
		return walletstore.ErrInvalidUserID
	}
	if err := walletstore.ValidateIdempotencyKey(params.IdempotencyKey); err != nil {
		return err
	}
	if params.MaxQuotesPerObservation <= 0 {
		return walletstore.ErrInvalidLimit
	}
	if params.SourceCode == "" {
		return walletstore.ErrMissingFXSource
	}
	if _, err := walletstore.ValidateCurrencyCode(params.BaseCurrency); err != nil {
		return err
	}
	if _, err := walletstore.ValidateCurrencyCode(params.QuoteCurrency); err != nil {
		return err
	}
	if params.BaseCurrency == params.QuoteCurrency {
		return walletstore.ErrIdenticalCurrencies
	}
	if params.BaseCurrencyUnitID == 0 || params.QuoteCurrencyUnitID == 0 {
		return walletstore.ErrMissingCurrencyUnitID
	}
	if params.BaseCurrencyUnitID < 0 || params.QuoteCurrencyUnitID < 0 {
		return walletstore.ErrInvalidCurrencyUnitID
	}
	if params.BaseCurrencyUnitID == params.QuoteCurrencyUnitID {
		return walletstore.ErrCurrencyMismatch
	}
	if params.InputMinor <= 0 {
		return walletstore.ErrInvalidAmount
	}
	switch params.Side {
	case walletstore.FXSideMid, walletstore.FXSideBid, walletstore.FXSideAsk, walletstore.FXSideFixedReference:
	default:
		if params.Side == "" {
			return walletstore.ErrMissingRateSide
		}
		return walletstore.ErrInvalidRateSide
	}
	if _, err := groosh.ParseRoundingMode(params.RoundingMode.String()); err != nil {
		return err
	}
	if params.ConversionTime.IsZero() {
		return walletstore.ErrMissingStartTime
	}
	return nil
}

func (s *Service) ListCurrencies(ctx context.Context, asOf time.Time, activeOnly bool) ([]Currency, error) {
	repository, err := s.requireRepository()
	if err != nil {
		return nil, err
	}
	definitions, err := repository.ListCurrencyUnits(ctx, asOf, activeOnly)
	if err != nil {
		return nil, err
	}
	result := make([]Currency, 0, len(definitions))
	for index := range definitions {
		currency, err := currencyFromDefinition(definitions[index])
		if err != nil {
			return nil, err
		}
		result = append(result, currency)
	}
	return result, nil
}

func (s *Service) GetCurrency(ctx context.Context, currencyCode string, asOf time.Time, requireActive bool) (Currency, error) {
	repository, err := s.requireRepository()
	if err != nil {
		return Currency{}, err
	}
	definition, err := repository.GetCurrencyUnit(ctx, currencyCode, asOf)
	if err != nil {
		return Currency{}, err
	}
	if definition == nil {
		return Currency{}, ErrInvalidCurrencyUnitData
	}
	if requireActive && !definition.IsActive {
		return Currency{}, ErrInactiveCurrency
	}
	return currencyFromDefinition(*definition)
}

// GetCurrencyByUnitID loads an exact, version-pinned currency definition.
// Historical wallet and ledger values must remain renderable after a currency
// is disabled or a newer unit version becomes effective.
func (s *Service) GetCurrencyByUnitID(ctx context.Context, currencyUnitID int64) (Currency, error) {
	repository, err := s.requireRepository()
	if err != nil {
		return Currency{}, err
	}
	definition, err := repository.GetCurrencyUnitByID(ctx, currencyUnitID)
	if err != nil {
		return Currency{}, err
	}
	if definition == nil {
		return Currency{}, ErrInvalidCurrencyUnitData
	}
	return currencyFromDefinition(*definition)
}

func (s *Service) ParseMajor(ctx context.Context, currencyCode string, currencyUnitID int64, major string, asOf time.Time) (groosh.Money, error) {
	currency, err := s.getPinnedCurrency(ctx, currencyCode, currencyUnitID, &asOf, true)
	if err != nil {
		return groosh.Money{}, err
	}
	return groosh.ParseMajor(major, currency.Unit)
}

func (s *Service) FormatMinor(ctx context.Context, currencyCode string, currencyUnitID, minor int64, mode groosh.RoundingMode) (groosh.Money, string, error) {
	// Rendering is intentionally historical: an immutable unit remains the
	// correct interpretation of stored minor units after it becomes inactive or
	// its validity interval ends.
	currency, err := s.getPinnedCurrency(ctx, currencyCode, currencyUnitID, nil, false)
	if err != nil {
		return groosh.Money{}, "", err
	}
	amount, err := groosh.NewMoney(minor, currency.Unit)
	if err != nil {
		return groosh.Money{}, "", err
	}
	display, err := amount.DisplayRounded(mode)
	if err != nil {
		return groosh.Money{}, "", err
	}
	return amount, display, nil
}

func (s *Service) QuoteConversion(ctx context.Context, params QuoteParams) (ConversionQuote, error) {
	repository, err := s.requireRepository()
	if err != nil {
		return ConversionQuote{}, err
	}
	if err := validateQuoteParams(params); err != nil {
		return ConversionQuote{}, err
	}
	if existing, lookupErr := repository.GetMoneyConversionQuoteByIdempotency(
		ctx, params.TenantID, params.RequestedBy, params.IdempotencyKey,
	); lookupErr == nil {
		quote, err := s.conversionQuoteFromStored(ctx, existing)
		if err != nil {
			return ConversionQuote{}, err
		}
		if !conversionQuoteMatchesParams(quote, params) {
			return ConversionQuote{}, ErrQuoteIdempotencyConflict
		}
		return quote, nil
	} else if !errors.Is(lookupErr, walletstore.ErrConversionQuoteNotFound) {
		return ConversionQuote{}, lookupErr
	}
	// PostgreSQL timestamptz has microsecond resolution. Normalize once before
	// selecting any versioned state so the persisted audit timestamp is exactly
	// the timestamp used for all validation and arithmetic.
	params.ConversionTime = params.ConversionTime.UTC().Truncate(time.Microsecond)
	inputCurrency, err := s.getPinnedCurrency(
		ctx, params.BaseCurrency, params.BaseCurrencyUnitID, &params.ConversionTime, true,
	)
	if err != nil {
		return ConversionQuote{}, err
	}
	outputCurrency, err := s.getPinnedCurrency(
		ctx, params.QuoteCurrency, params.QuoteCurrencyUnitID, &params.ConversionTime, true,
	)
	if err != nil {
		return ConversionQuote{}, err
	}
	observation, err := repository.GetLatestFXObservation(
		ctx,
		params.SourceCode,
		params.BaseCurrency,
		params.QuoteCurrency,
		params.Side,
		params.ConversionTime,
	)
	if err != nil {
		return ConversionQuote{}, err
	}
	if observation == nil || observation.Observation == nil || observation.BaseUnit == nil || observation.QuoteUnit == nil {
		return ConversionQuote{}, ErrObservationPairMismatch
	}
	if observation.BaseUnit.ID != observation.Observation.BaseCurrencyUnitID ||
		observation.BaseUnit.CurrencyCode != observation.Observation.BaseCurrencyCode ||
		observation.QuoteUnit.ID != observation.Observation.QuoteCurrencyUnitID ||
		observation.QuoteUnit.CurrencyCode != observation.Observation.QuoteCurrencyCode {
		return ConversionQuote{}, ErrObservationPairMismatch
	}
	if err := validateObservationForRequest(observation, params); err != nil {
		return ConversionQuote{}, err
	}
	observationBase, err := currencyFromDefinition(*observation.BaseUnit)
	if err != nil {
		return ConversionQuote{}, err
	}
	observationQuote, err := currencyFromDefinition(*observation.QuoteUnit)
	if err != nil {
		return ConversionQuote{}, err
	}
	if err := requireUnitEffectiveAt(observationBase.Unit, observation.Observation.ObservationAt); err != nil {
		return ConversionQuote{}, err
	}
	if err := requireUnitEffectiveAt(observationQuote.Unit, observation.Observation.ObservationAt); err != nil {
		return ConversionQuote{}, err
	}
	source, err := repository.GetFXSourceByID(ctx, observation.Observation.SourceID)
	if err != nil {
		return ConversionQuote{}, err
	}
	if source == nil || source.ID != observation.Observation.SourceID || source.Code != params.SourceCode ||
		source.Purpose != observation.Observation.Purpose || !source.IsEnabled {
		return ConversionQuote{}, ErrObservationPairMismatch
	}

	inputUnit, outputUnit := inputCurrency.Unit, outputCurrency.Unit
	if inputUnit.Code() != params.BaseCurrency || outputUnit.Code() != params.QuoteCurrency {
		return ConversionQuote{}, ErrObservationPairMismatch
	}

	input, err := groosh.NewMoney(params.InputMinor, inputUnit)
	if err != nil {
		return ConversionQuote{}, err
	}
	appliedRate := observation.Observation.Rate.Rat()
	if observation.Inverse {
		appliedRate.Inv(appliedRate)
	}
	output, err := groosh.Convert(input, outputUnit, appliedRate, params.RoundingMode)
	if err != nil {
		return ConversionQuote{}, err
	}
	if output.MinorUnits() < 0 {
		return ConversionQuote{}, walletstore.ErrInvalidAmount
	}
	quoteInput := walletstore.MoneyConversionQuoteInput{
		TenantID:                       params.TenantID,
		RequestedByUserID:              params.RequestedBy,
		IdempotencyKey:                 params.IdempotencyKey,
		MaxQuotesPerObservation:        params.MaxQuotesPerObservation,
		ObservationID:                  observation.Observation.ID,
		ObservationBaseCurrencyUnitID:  observation.Observation.BaseCurrencyUnitID,
		ObservationQuoteCurrencyUnitID: observation.Observation.QuoteCurrencyUnitID,
		ObservationBaseCurrencyCode:    observation.Observation.BaseCurrencyCode,
		ObservationQuoteCurrencyCode:   observation.Observation.QuoteCurrencyCode,
		ObservationExpiresAt:           observation.Observation.ExpiresAt,
		InputCurrencyUnitID:            inputUnit.VersionID(),
		OutputCurrencyUnitID:           outputUnit.VersionID(),
		InputCurrencyCode:              inputUnit.Code(),
		OutputCurrencyCode:             outputUnit.Code(),
		InputMinorUnits:                input.MinorUnits(),
		OutputMinorUnits:               output.MinorUnits(),
		Inverse:                        observation.Inverse,
		RoundingMode:                   params.RoundingMode.String(),
		ConversionAt:                   params.ConversionTime,
		ExpiresAt:                      observation.Observation.ExpiresAt,
	}
	persisted, err := repository.CreateMoneyConversionQuote(ctx, quoteInput)
	if err != nil {
		return ConversionQuote{}, err
	}
	if !moneyConversionQuoteMatchesInput(persisted, quoteInput) {
		quote, hydrateErr := s.conversionQuoteFromStored(ctx, persisted)
		if hydrateErr != nil {
			return ConversionQuote{}, hydrateErr
		}
		if !conversionQuoteMatchesParams(quote, params) {
			return ConversionQuote{}, ErrQuoteIdempotencyConflict
		}
		return quote, nil
	}
	return ConversionQuote{
		Quote:       *persisted,
		Input:       input,
		Output:      output,
		Observation: *observation.Observation,
		Source:      *source,
		AppliedRate: new(big.Rat).Set(appliedRate),
	}, nil
}

func (s *Service) GetConversionQuote(ctx context.Context, tenantID string, requestedBy int64, quoteID uuid.UUID) (ConversionQuote, error) {
	repository, err := s.requireRepository()
	if err != nil {
		return ConversionQuote{}, err
	}
	quote, err := repository.GetMoneyConversionQuote(ctx, tenantID, requestedBy, quoteID)
	if err != nil {
		return ConversionQuote{}, err
	}
	return s.conversionQuoteFromStored(ctx, quote)
}

func (s *Service) conversionQuoteFromStored(ctx context.Context, quote *walletstore.MoneyConversionQuote) (ConversionQuote, error) {
	if quote == nil {
		return ConversionQuote{}, ErrQuoteIntegrity
	}
	repository, err := s.requireRepository()
	if err != nil {
		return ConversionQuote{}, err
	}
	inputDefinition, err := repository.GetCurrencyUnitByID(ctx, quote.InputCurrencyUnitID)
	if err != nil {
		return ConversionQuote{}, err
	}
	outputDefinition, err := repository.GetCurrencyUnitByID(ctx, quote.OutputCurrencyUnitID)
	if err != nil {
		return ConversionQuote{}, err
	}
	if inputDefinition == nil || outputDefinition == nil {
		return ConversionQuote{}, ErrQuoteIntegrity
	}
	if inputDefinition.ID != quote.InputCurrencyUnitID || inputDefinition.CurrencyCode != quote.InputCurrencyCode ||
		outputDefinition.ID != quote.OutputCurrencyUnitID || outputDefinition.CurrencyCode != quote.OutputCurrencyCode {
		return ConversionQuote{}, ErrQuoteIntegrity
	}
	inputCurrency, err := currencyFromDefinition(*inputDefinition)
	if err != nil {
		return ConversionQuote{}, err
	}
	outputCurrency, err := currencyFromDefinition(*outputDefinition)
	if err != nil {
		return ConversionQuote{}, err
	}
	if quote.ConversionAt.IsZero() || quote.CreatedAt.Before(quote.ConversionAt) || !quote.ExpiresAt.After(quote.ConversionAt) {
		return ConversionQuote{}, ErrQuoteIntegrity
	}
	if err := requireUnitEffectiveAt(inputCurrency.Unit, quote.ConversionAt); err != nil {
		return ConversionQuote{}, errors.Join(ErrQuoteIntegrity, err)
	}
	if err := requireUnitEffectiveAt(outputCurrency.Unit, quote.ConversionAt); err != nil {
		return ConversionQuote{}, errors.Join(ErrQuoteIntegrity, err)
	}
	input, err := groosh.NewMoney(quote.InputMinorUnits, inputCurrency.Unit)
	if err != nil {
		return ConversionQuote{}, err
	}
	output, err := groosh.NewMoney(quote.OutputMinorUnits, outputCurrency.Unit)
	if err != nil {
		return ConversionQuote{}, err
	}
	observation, err := repository.GetFXObservationByID(ctx, quote.ObservationID)
	if err != nil {
		return ConversionQuote{}, err
	}
	if err := validatePersistedQuoteObservation(quote, observation); err != nil {
		return ConversionQuote{}, err
	}
	source, err := repository.GetFXSourceByID(ctx, observation.SourceID)
	if err != nil {
		return ConversionQuote{}, err
	}
	if source == nil || source.ID != observation.SourceID || source.Purpose != observation.Purpose {
		return ConversionQuote{}, ErrQuoteIntegrity
	}
	appliedRate := observation.Rate.Rat()
	if quote.Inverse {
		appliedRate.Inv(appliedRate)
	}
	mode, err := groosh.ParseRoundingMode(quote.RoundingMode)
	if err != nil {
		return ConversionQuote{}, errors.Join(ErrQuoteIntegrity, err)
	}
	recomputed, err := groosh.Convert(input, outputCurrency.Unit, appliedRate, mode)
	if err != nil {
		return ConversionQuote{}, errors.Join(ErrQuoteIntegrity, err)
	}
	comparison, err := recomputed.Cmp(output)
	if err != nil || comparison != 0 {
		return ConversionQuote{}, errors.Join(ErrQuoteIntegrity, err)
	}
	return ConversionQuote{
		Quote:       *quote,
		Input:       input,
		Output:      output,
		Observation: *observation,
		Source:      *source,
		AppliedRate: appliedRate,
	}, nil
}

func validateObservationForRequest(observation *walletstore.FXRateObservation, params QuoteParams) error {
	if observation == nil || observation.Observation == nil {
		return ErrObservationPairMismatch
	}
	stored := observation.Observation
	direct := stored.BaseCurrencyCode == params.BaseCurrency && stored.QuoteCurrencyCode == params.QuoteCurrency
	inverse := stored.BaseCurrencyCode == params.QuoteCurrency && stored.QuoteCurrencyCode == params.BaseCurrency
	if !direct && !inverse || observation.Inverse != inverse {
		return ErrObservationPairMismatch
	}
	expectedSide := params.Side
	if inverse {
		expectedSide = inverseRateSide(expectedSide)
	}
	if stored.Rate.Sign() <= 0 || stored.Side != expectedSide || stored.ObservationAt.After(params.ConversionTime) ||
		stored.RetrievedAt.After(params.ConversionTime) || stored.CreatedAt.IsZero() ||
		stored.CreatedAt.After(params.ConversionTime) || !stored.ExpiresAt.After(params.ConversionTime) {
		return ErrObservationPairMismatch
	}
	return nil
}

func inverseRateSide(side string) string {
	switch side {
	case walletstore.FXSideBid:
		return walletstore.FXSideAsk
	case walletstore.FXSideAsk:
		return walletstore.FXSideBid
	default:
		return side
	}
}

func requireUnitEffectiveAt(unit groosh.CurrencyUnit, at time.Time) error {
	effective, err := unit.IsEffectiveAt(at)
	if err != nil {
		return errors.Join(ErrInvalidCurrencyUnitData, err)
	}
	if !effective {
		return ErrInvalidCurrencyUnitData
	}
	return nil
}

func (s *Service) getPinnedCurrency(
	ctx context.Context,
	currencyCode string,
	currencyUnitID int64,
	effectiveAt *time.Time,
	requireActive bool,
) (Currency, error) {
	repository, err := s.requireRepository()
	if err != nil {
		return Currency{}, err
	}
	if currencyUnitID == 0 {
		return Currency{}, walletstore.ErrMissingCurrencyUnitID
	}
	if currencyUnitID < 0 {
		return Currency{}, walletstore.ErrInvalidCurrencyUnitID
	}
	if _, err := walletstore.ValidateCurrencyCode(currencyCode); err != nil {
		return Currency{}, err
	}
	definition, err := repository.GetCurrencyUnitByID(ctx, currencyUnitID)
	if err != nil {
		return Currency{}, err
	}
	if definition == nil || definition.ID != currencyUnitID {
		return Currency{}, ErrInvalidCurrencyUnitData
	}
	if definition.CurrencyCode != currencyCode {
		return Currency{}, walletstore.ErrCurrencyMismatch
	}
	if requireActive && !definition.IsActive {
		return Currency{}, ErrInactiveCurrency
	}
	currency, err := currencyFromDefinition(*definition)
	if err != nil {
		return Currency{}, err
	}
	if effectiveAt != nil {
		if effectiveAt.IsZero() {
			return Currency{}, walletstore.ErrMissingStartTime
		}
		effective, err := currency.Unit.IsEffectiveAt(effectiveAt.UTC())
		if err != nil {
			return Currency{}, errors.Join(ErrInvalidCurrencyUnitData, err)
		}
		if !effective {
			return Currency{}, walletstore.ErrInvalidUsageTime
		}
	}
	return currency, nil
}

func moneyConversionQuoteMatchesInput(stored *walletstore.MoneyConversionQuote, input walletstore.MoneyConversionQuoteInput) bool {
	return stored != nil && stored.ID != uuid.Nil &&
		stored.TenantID == input.TenantID &&
		stored.RequestedByUserID == input.RequestedByUserID &&
		stored.IdempotencyKey == input.IdempotencyKey &&
		stored.ObservationID == input.ObservationID &&
		stored.ObservationBaseCurrencyUnitID == input.ObservationBaseCurrencyUnitID &&
		stored.ObservationQuoteCurrencyUnitID == input.ObservationQuoteCurrencyUnitID &&
		stored.ObservationBaseCurrencyCode == input.ObservationBaseCurrencyCode &&
		stored.ObservationQuoteCurrencyCode == input.ObservationQuoteCurrencyCode &&
		stored.ObservationExpiresAt.Equal(input.ObservationExpiresAt) &&
		stored.InputCurrencyUnitID == input.InputCurrencyUnitID &&
		stored.OutputCurrencyUnitID == input.OutputCurrencyUnitID &&
		stored.InputCurrencyCode == input.InputCurrencyCode &&
		stored.OutputCurrencyCode == input.OutputCurrencyCode &&
		stored.InputMinorUnits == input.InputMinorUnits &&
		stored.OutputMinorUnits == input.OutputMinorUnits &&
		stored.Inverse == input.Inverse &&
		stored.RoundingMode == input.RoundingMode &&
		stored.ConversionAt.Equal(input.ConversionAt) &&
		stored.ExpiresAt.Equal(input.ExpiresAt)
}

func conversionQuoteMatchesParams(quote ConversionQuote, params QuoteParams) bool {
	requestedSide := quote.Observation.Side
	if quote.Quote.Inverse {
		requestedSide = inverseRateSide(requestedSide)
	}
	return quote.Quote.ID != uuid.Nil &&
		quote.Quote.TenantID == params.TenantID &&
		quote.Quote.RequestedByUserID == params.RequestedBy &&
		quote.Quote.IdempotencyKey == params.IdempotencyKey &&
		quote.Source.Code == params.SourceCode &&
		quote.Input.CurrencyCode() == params.BaseCurrency &&
		quote.Input.UnitVersionID() == params.BaseCurrencyUnitID &&
		quote.Input.MinorUnits() == params.InputMinor &&
		quote.Output.CurrencyCode() == params.QuoteCurrency &&
		quote.Output.UnitVersionID() == params.QuoteCurrencyUnitID &&
		requestedSide == params.Side &&
		quote.Quote.RoundingMode == params.RoundingMode.String()
}

func validatePersistedQuoteObservation(quote *walletstore.MoneyConversionQuote, observation *walletstore.FXObservation) error {
	if quote == nil || observation == nil || observation.ID != quote.ObservationID ||
		observation.BaseCurrencyUnitID != quote.ObservationBaseCurrencyUnitID ||
		observation.QuoteCurrencyUnitID != quote.ObservationQuoteCurrencyUnitID ||
		observation.BaseCurrencyCode != quote.ObservationBaseCurrencyCode ||
		observation.QuoteCurrencyCode != quote.ObservationQuoteCurrencyCode ||
		!observation.ExpiresAt.Equal(quote.ObservationExpiresAt) ||
		!quote.ExpiresAt.Equal(quote.ObservationExpiresAt) || observation.Rate.Sign() <= 0 ||
		quote.ConversionAt.IsZero() || observation.ObservationAt.After(quote.ConversionAt) ||
		observation.RetrievedAt.After(quote.ConversionAt) || observation.CreatedAt.IsZero() ||
		observation.CreatedAt.After(quote.ConversionAt) || !observation.ExpiresAt.After(quote.ConversionAt) {
		return ErrQuoteIntegrity
	}
	if (!quote.Inverse && (quote.InputCurrencyCode != observation.BaseCurrencyCode || quote.OutputCurrencyCode != observation.QuoteCurrencyCode)) ||
		(quote.Inverse && (quote.InputCurrencyCode != observation.QuoteCurrencyCode || quote.OutputCurrencyCode != observation.BaseCurrencyCode)) {
		return ErrQuoteIntegrity
	}
	return nil
}

func currencyFromDefinition(definition walletstore.CurrencyUnitVersion) (Currency, error) {
	if definition.ID <= 0 || definition.ValidFrom.IsZero() {
		return Currency{}, fmt.Errorf("%w: missing identity or effective date", ErrInvalidCurrencyUnitData)
	}
	display, err := exponent(definition.DisplayExponent)
	if err != nil {
		return Currency{}, err
	}
	cash, err := exponent(definition.CashExponent)
	if err != nil {
		return Currency{}, err
	}
	var iso *uint8
	if definition.ISOMinorExponent.Valid {
		value, err := exponent(definition.ISOMinorExponent.Int16)
		if err != nil {
			return Currency{}, err
		}
		iso = &value
	}
	var validTo *time.Time
	if definition.ValidTo.Valid {
		value := definition.ValidTo.Time.UTC()
		validTo = &value
	}
	unit, err := groosh.NewCurrencyUnit(groosh.CurrencyUnitSpec{
		VersionID:        definition.ID,
		Code:             definition.CurrencyCode,
		ISOMinorExponent: iso,
		DisplayExponent:  &display,
		CashExponent:     &cash,
		CashIncrement:    definition.CashRoundingIncrement,
		EffectiveFrom:    definition.ValidFrom.UTC(),
		EffectiveUntil:   validTo,
	})
	if err != nil {
		return Currency{}, errors.Join(ErrInvalidCurrencyUnitData, err)
	}
	return Currency{Definition: definition, Unit: unit}, nil
}

func exponent(value int16) (uint8, error) {
	if value < 0 || value > 255 {
		return 0, fmt.Errorf("%w: exponent %d", ErrInvalidCurrencyUnitData, value)
	}
	return uint8(value), nil
}
