package rates

import (
	"context"
	"errors"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/shopspring/decimal"
)

var ErrMissingStore = errors.New("missing rate store")

type Service struct {
	Store *walletstore.Store
}

func (s *Service) Convert(ctx context.Context, tenantID string, amount int64, fromCurrency, toCurrency string) (int64, error) {
	if s == nil || s.Store == nil {
		return 0, ErrMissingStore
	}
	if fromCurrency == toCurrency {
		return amount, nil
	}
	rate, err := s.Store.GetActiveRate(ctx, tenantID, fromCurrency, toCurrency)
	if err != nil {
		return 0, err
	}
	converted := decimal.NewFromInt(amount).
		Mul(rate.SellRate).
		Round(0).
		IntPart()
	return converted, nil
}
