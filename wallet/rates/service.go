package rates

import (
	"context"
	"errors"
	"time"

	"github.com/adonese/noebs/groosh"
	walletstore "github.com/adonese/noebs/wallet/store"
)

var ErrMissingStore = errors.New("missing rate store")

type Service struct {
	Store              *walletstore.Store
	RateLookup         func(context.Context, string, string, int64, string, int64, time.Time) (*walletstore.ExchangeRate, error)
	CurrencyUnitLookup func(context.Context, int64) (*walletstore.CurrencyUnitVersion, error)
}

func (s *Service) Convert(ctx context.Context, tenantID string, amount int64, fromCurrency string, fromCurrencyUnitID int64, toCurrency string, toCurrencyUnitID int64) (int64, error) {
	if s == nil || s.Store == nil {
		return 0, ErrMissingStore
	}
	if _, err := walletstore.ValidateTenantID(tenantID); err != nil {
		return 0, err
	}
	if amount <= 0 {
		return 0, walletstore.ErrInvalidAmount
	}
	if fromCurrency == "" {
		return 0, walletstore.ErrMissingBaseCurrency
	}
	if toCurrency == "" {
		return 0, walletstore.ErrMissingQuoteCurrency
	}
	if _, err := walletstore.ValidateCurrencyCode(fromCurrency); err != nil {
		return 0, err
	}
	if _, err := walletstore.ValidateCurrencyCode(toCurrency); err != nil {
		return 0, err
	}
	if err := walletstore.ValidateCurrencyUnitID(fromCurrencyUnitID); err != nil {
		return 0, err
	}
	if err := walletstore.ValidateCurrencyUnitID(toCurrencyUnitID); err != nil {
		return 0, err
	}
	if fromCurrency == toCurrency {
		if fromCurrencyUnitID != toCurrencyUnitID {
			return 0, walletstore.ErrCurrencyMismatch
		}
		unit, err := s.getCurrencyUnit(ctx, fromCurrencyUnitID)
		if err != nil {
			return 0, err
		}
		if unit == nil {
			return 0, walletstore.ErrCurrencyNotFound
		}
		if unit.ID != fromCurrencyUnitID || unit.CurrencyCode != fromCurrency {
			return 0, walletstore.ErrCurrencyMismatch
		}
		// Identity conversion still handles operational money. Reject catalog
		// definitions that cannot give the integer an exact minor-unit meaning;
		// do not let the no-rate fast path bypass scale validation.
		if _, err := currencyUnit(unit); err != nil {
			return 0, err
		}
		return amount, nil
	}
	asOf := time.Now().UTC()
	rate, err := s.getActiveRate(ctx, tenantID, fromCurrency, fromCurrencyUnitID, toCurrency, toCurrencyUnitID, asOf)
	if err != nil {
		return 0, err
	}
	if rate == nil {
		return 0, walletstore.ErrExchangeRateNotFound
	}
	if rate.BaseCurrency != fromCurrency || rate.QuoteCurrency != toCurrency {
		return 0, walletstore.ErrCurrencyMismatch
	}
	if err := walletstore.ValidateCurrencyUnitID(rate.BaseCurrencyUnitID); err != nil {
		return 0, err
	}
	if err := walletstore.ValidateCurrencyUnitID(rate.QuoteCurrencyUnitID); err != nil {
		return 0, err
	}
	if rate.BaseCurrencyUnitID != fromCurrencyUnitID || rate.QuoteCurrencyUnitID != toCurrencyUnitID {
		return 0, walletstore.ErrCurrencyMismatch
	}
	baseUnit, err := s.getCurrencyUnit(ctx, rate.BaseCurrencyUnitID)
	if err != nil {
		return 0, err
	}
	if baseUnit == nil {
		return 0, walletstore.ErrCurrencyNotFound
	}
	if baseUnit.ID != rate.BaseCurrencyUnitID || baseUnit.CurrencyCode != fromCurrency {
		return 0, walletstore.ErrCurrencyMismatch
	}
	quoteUnit, err := s.getCurrencyUnit(ctx, rate.QuoteCurrencyUnitID)
	if err != nil {
		return 0, err
	}
	if quoteUnit == nil {
		return 0, walletstore.ErrCurrencyNotFound
	}
	if quoteUnit.ID != rate.QuoteCurrencyUnitID || quoteUnit.CurrencyCode != toCurrency {
		return 0, walletstore.ErrCurrencyMismatch
	}
	return ConvertMinorUnits(amount, rate.SellRate, baseUnit, quoteUnit, groosh.RoundHalfAwayFromZero)
}

func (s *Service) getActiveRate(ctx context.Context, tenantID, baseCurrency string, baseCurrencyUnitID int64, quoteCurrency string, quoteCurrencyUnitID int64, asOf time.Time) (*walletstore.ExchangeRate, error) {
	if s.RateLookup != nil {
		return s.RateLookup(ctx, tenantID, baseCurrency, baseCurrencyUnitID, quoteCurrency, quoteCurrencyUnitID, asOf)
	}
	return s.Store.GetActiveRateForUnitsAt(ctx, tenantID, baseCurrency, baseCurrencyUnitID, quoteCurrency, quoteCurrencyUnitID, asOf)
}

func (s *Service) getCurrencyUnit(ctx context.Context, currencyUnitID int64) (*walletstore.CurrencyUnitVersion, error) {
	if err := walletstore.ValidateCurrencyUnitID(currencyUnitID); err != nil {
		return nil, err
	}
	if s.CurrencyUnitLookup != nil {
		return s.CurrencyUnitLookup(ctx, currencyUnitID)
	}
	return s.Store.GetCurrencyUnitByID(ctx, currencyUnitID)
}
