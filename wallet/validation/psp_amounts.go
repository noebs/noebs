package validation

import (
	"context"
	"time"

	"github.com/adonese/noebs/groosh"
	walletrates "github.com/adonese/noebs/wallet/rates"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/shopspring/decimal"
)

type PSPAmountResolutionRequest struct {
	TenantID                 string
	RequestedAmount          int64
	RequestedCurrency        string
	RequestedCurrencyUnitID  int64
	SettlementAmount         int64
	SettlementCurrency       string
	SettlementCurrencyUnitID int64
	WalletCurrency           string
	WalletCurrencyUnitID     int64
	FXRate                   decimal.NullDecimal
	FXBaseCurrency           string
	FXQuoteCurrency          string
}

type PSPAmountResolutionResult struct {
	WalletCreditAmount       int64
	WalletCurrency           string
	WalletCurrencyUnitID     int64
	RequestedAmount          int64
	RequestedCurrency        string
	RequestedCurrencyUnitID  int64
	SettlementAmount         int64
	SettlementCurrency       string
	SettlementCurrencyUnitID int64
	AppliedFXRate            decimal.NullDecimal
	AppliedFXSource          string
	AppliedFXConversionAt    time.Time
	VarianceAmount           int64
	VarianceKind             walletstore.PSPAmountKind
}

func (s *Service) ResolvePSPDepositAmounts(ctx context.Context, req PSPAmountResolutionRequest) (*PSPAmountResolutionResult, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	if _, err := walletstore.ValidateTenantID(req.TenantID); err != nil {
		return nil, err
	}
	if req.RequestedAmount <= 0 {
		return nil, walletstore.ErrInvalidAmount
	}
	if req.SettlementAmount <= 0 {
		return nil, walletstore.ErrInvalidAmount
	}
	if missingRequiredText(req.RequestedCurrency) {
		return nil, ErrMissingRequestedCurrency
	}
	if missingRequiredText(req.SettlementCurrency) {
		return nil, ErrMissingSettlementCurrency
	}
	if missingRequiredText(req.WalletCurrency) {
		return nil, ErrMissingWalletCurrency
	}
	for _, currencyUnitID := range []int64{
		req.RequestedCurrencyUnitID,
		req.SettlementCurrencyUnitID,
		req.WalletCurrencyUnitID,
	} {
		if err := walletstore.ValidateCurrencyUnitID(currencyUnitID); err != nil {
			return nil, err
		}
	}

	asOf := time.Now().UTC().Truncate(time.Microsecond)
	walletCredit, appliedRate, source, err := s.convertAmountAt(
		ctx,
		req.TenantID,
		req.SettlementAmount,
		req.SettlementCurrency,
		req.SettlementCurrencyUnitID,
		req.WalletCurrency,
		req.WalletCurrencyUnitID,
		req.FXRate,
		req.FXBaseCurrency,
		req.FXQuoteCurrency,
		asOf,
	)
	if err != nil {
		return nil, err
	}

	requestedInWallet, _, _, err := s.convertAmountAt(
		ctx,
		req.TenantID,
		req.RequestedAmount,
		req.RequestedCurrency,
		req.RequestedCurrencyUnitID,
		req.WalletCurrency,
		req.WalletCurrencyUnitID,
		decimal.NullDecimal{},
		"",
		"",
		asOf,
	)
	if err != nil {
		return nil, err
	}

	variance, err := checkedSubtractInt64(walletCredit, requestedInWallet)
	if err != nil {
		return nil, err
	}
	varianceKind := walletstore.PSPAmountKind("")
	if variance > 0 {
		varianceKind = walletstore.PSPAmountOverpayment
	} else if variance < 0 {
		varianceKind = walletstore.PSPAmountUnderpayment
	}

	conversionAt := asOf
	if !appliedRate.Valid {
		conversionAt = time.Time{}
	}
	return &PSPAmountResolutionResult{
		WalletCreditAmount:       walletCredit,
		WalletCurrency:           req.WalletCurrency,
		WalletCurrencyUnitID:     req.WalletCurrencyUnitID,
		RequestedAmount:          req.RequestedAmount,
		RequestedCurrency:        req.RequestedCurrency,
		RequestedCurrencyUnitID:  req.RequestedCurrencyUnitID,
		SettlementAmount:         req.SettlementAmount,
		SettlementCurrency:       req.SettlementCurrency,
		SettlementCurrencyUnitID: req.SettlementCurrencyUnitID,
		AppliedFXRate:            appliedRate,
		AppliedFXSource:          source,
		AppliedFXConversionAt:    conversionAt,
		VarianceAmount:           variance,
		VarianceKind:             varianceKind,
	}, nil
}

func (s *Service) convertAmount(ctx context.Context, tenantID string, amount int64, fromCurrency string, fromCurrencyUnitID int64, toCurrency string, toCurrencyUnitID int64, fxRate decimal.NullDecimal, fxBase, fxQuote string) (int64, decimal.NullDecimal, string, error) {
	return s.convertAmountAt(ctx, tenantID, amount, fromCurrency, fromCurrencyUnitID, toCurrency, toCurrencyUnitID, fxRate, fxBase, fxQuote, time.Now().UTC())
}

func (s *Service) convertAmountAt(ctx context.Context, tenantID string, amount int64, fromCurrency string, fromCurrencyUnitID int64, toCurrency string, toCurrencyUnitID int64, fxRate decimal.NullDecimal, fxBase, fxQuote string, asOf time.Time) (int64, decimal.NullDecimal, string, error) {
	if amount <= 0 {
		return 0, decimal.NullDecimal{}, "", walletstore.ErrInvalidAmount
	}
	if err := walletstore.ValidateCurrencyUnitID(fromCurrencyUnitID); err != nil {
		return 0, decimal.NullDecimal{}, "", err
	}
	if err := walletstore.ValidateCurrencyUnitID(toCurrencyUnitID); err != nil {
		return 0, decimal.NullDecimal{}, "", err
	}
	if fromCurrency == toCurrency {
		if _, err := s.requireCurrencyUnitIdentity(ctx, fromCurrency, fromCurrencyUnitID); err != nil {
			return 0, decimal.NullDecimal{}, "", err
		}
		if _, err := s.requireCurrencyUnitIdentity(ctx, toCurrency, toCurrencyUnitID); err != nil {
			return 0, decimal.NullDecimal{}, "", err
		}
		if fromCurrencyUnitID != toCurrencyUnitID {
			return 0, decimal.NullDecimal{}, "", walletstore.ErrCurrencyMismatch
		}
		return amount, decimal.NullDecimal{}, "", nil
	}
	if fxRate.Valid {
		if missingRequiredText(fxBase) || missingRequiredText(fxQuote) {
			return 0, decimal.NullDecimal{}, "", walletstore.ErrMissingFXCurrency
		}
		if fxBase != fromCurrency {
			return 0, decimal.NullDecimal{}, "", ErrFXCurrencyMismatch
		}
		if fxQuote != toCurrency {
			return 0, decimal.NullDecimal{}, "", ErrFXCurrencyMismatch
		}
		converted, err := s.convertPositiveAmountByID(ctx, amount, fxRate.Decimal, fromCurrency, fromCurrencyUnitID, toCurrency, toCurrencyUnitID)
		if err != nil {
			return 0, decimal.NullDecimal{}, "", err
		}
		return converted, fxRate, "psp", nil
	}

	rate, err := s.lookupRateAt(ctx, tenantID, fromCurrency, fromCurrencyUnitID, toCurrency, toCurrencyUnitID, asOf)
	if err != nil {
		return 0, decimal.NullDecimal{}, "", err
	}
	converted, err := s.convertUsingExchangeRate(ctx, amount, fromCurrency, toCurrency, fromCurrencyUnitID, toCurrencyUnitID, rate)
	if err != nil {
		return 0, decimal.NullDecimal{}, "", err
	}
	return converted, decimal.NullDecimal{Decimal: rate.SellRate, Valid: true}, "rates", nil
}

func (s *Service) lookupRateAt(ctx context.Context, tenantID, fromCurrency string, fromCurrencyUnitID int64, toCurrency string, toCurrencyUnitID int64, asOf time.Time) (*walletstore.ExchangeRate, error) {
	if s.RateLookup != nil {
		return s.RateLookup(ctx, tenantID, fromCurrency, fromCurrencyUnitID, toCurrency, toCurrencyUnitID, asOf)
	}
	return s.Store.GetActiveRateForUnitsAt(ctx, tenantID, fromCurrency, fromCurrencyUnitID, toCurrency, toCurrencyUnitID, asOf)
}

func (s *Service) convertPositiveAmountByID(ctx context.Context, amount int64, rate decimal.Decimal, fromCurrency string, fromCurrencyUnitID int64, toCurrency string, toCurrencyUnitID int64) (int64, error) {
	if amount <= 0 {
		return 0, walletstore.ErrInvalidAmount
	}
	if rate.Cmp(decimal.Zero) <= 0 {
		return 0, walletstore.ErrInvalidRate
	}
	baseUnit, err := s.getCurrencyUnit(ctx, fromCurrencyUnitID)
	if err != nil {
		return 0, err
	}
	if baseUnit == nil {
		return 0, walletstore.ErrCurrencyNotFound
	}
	if baseUnit != nil && baseUnit.CurrencyCode != fromCurrency {
		return 0, walletstore.ErrCurrencyMismatch
	}
	quoteUnit, err := s.getCurrencyUnit(ctx, toCurrencyUnitID)
	if err != nil {
		return 0, err
	}
	if quoteUnit == nil {
		return 0, walletstore.ErrCurrencyNotFound
	}
	if quoteUnit != nil && quoteUnit.CurrencyCode != toCurrency {
		return 0, walletstore.ErrCurrencyMismatch
	}
	return convertPositiveAmountWithUnits(amount, rate, baseUnit, quoteUnit)
}

func convertPositiveAmountWithUnits(amount int64, rate decimal.Decimal, baseUnit, quoteUnit *walletstore.CurrencyUnitVersion) (int64, error) {
	return walletrates.ConvertMinorUnits(amount, rate, baseUnit, quoteUnit, groosh.RoundHalfAwayFromZero)
}
