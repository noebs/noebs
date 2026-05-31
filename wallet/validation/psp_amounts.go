package validation

import (
	"context"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/shopspring/decimal"
)

type PSPAmountResolutionRequest struct {
	TenantID           string
	RequestedAmount    int64
	RequestedCurrency  string
	SettlementAmount   int64
	SettlementCurrency string
	WalletCurrency     string
	FXRate             decimal.NullDecimal
	FXBaseCurrency     string
	FXQuoteCurrency    string
}

type PSPAmountResolutionResult struct {
	WalletCreditAmount int64
	WalletCurrency     string
	RequestedAmount    int64
	RequestedCurrency  string
	SettlementAmount   int64
	SettlementCurrency string
	AppliedFXRate      decimal.NullDecimal
	AppliedFXSource    string
	VarianceAmount     int64
	VarianceKind       walletstore.PSPAmountKind
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
	if req.RequestedCurrency == "" {
		return nil, ErrMissingRequestedCurrency
	}
	if req.SettlementCurrency == "" {
		return nil, ErrMissingSettlementCurrency
	}
	if req.WalletCurrency == "" {
		return nil, ErrMissingWalletCurrency
	}

	walletCredit, appliedRate, source, err := s.convertAmount(ctx, req.TenantID, req.SettlementAmount, req.SettlementCurrency, req.WalletCurrency, req.FXRate, req.FXBaseCurrency, req.FXQuoteCurrency)
	if err != nil {
		return nil, err
	}

	requestedInWallet, _, _, err := s.convertAmount(ctx, req.TenantID, req.RequestedAmount, req.RequestedCurrency, req.WalletCurrency, decimal.NullDecimal{}, "", "")
	if err != nil {
		return nil, err
	}

	variance := walletCredit - requestedInWallet
	varianceKind := walletstore.PSPAmountKind("")
	if variance > 0 {
		varianceKind = walletstore.PSPAmountOverpayment
	} else if variance < 0 {
		varianceKind = walletstore.PSPAmountUnderpayment
	}

	return &PSPAmountResolutionResult{
		WalletCreditAmount: walletCredit,
		WalletCurrency:     req.WalletCurrency,
		RequestedAmount:    req.RequestedAmount,
		RequestedCurrency:  req.RequestedCurrency,
		SettlementAmount:   req.SettlementAmount,
		SettlementCurrency: req.SettlementCurrency,
		AppliedFXRate:      appliedRate,
		AppliedFXSource:    source,
		VarianceAmount:     variance,
		VarianceKind:       varianceKind,
	}, nil
}

func (s *Service) convertAmount(ctx context.Context, tenantID string, amount int64, fromCurrency, toCurrency string, fxRate decimal.NullDecimal, fxBase, fxQuote string) (int64, decimal.NullDecimal, string, error) {
	if fromCurrency == toCurrency {
		return amount, decimal.NullDecimal{}, "", nil
	}
	if fxRate.Valid {
		if fxBase != "" && fxBase != fromCurrency {
			return 0, decimal.NullDecimal{}, "", ErrFXCurrencyMismatch
		}
		if fxQuote != "" && fxQuote != toCurrency {
			return 0, decimal.NullDecimal{}, "", ErrFXCurrencyMismatch
		}
		converted := decimal.NewFromInt(amount).Mul(fxRate.Decimal).Round(0).IntPart()
		return converted, fxRate, "psp", nil
	}

	rate, err := s.lookupRate(ctx, tenantID, fromCurrency, toCurrency)
	if err != nil {
		return 0, decimal.NullDecimal{}, "", err
	}
	converted := decimal.NewFromInt(amount).Mul(rate).Round(0).IntPart()
	return converted, decimal.NullDecimal{Decimal: rate, Valid: true}, "rates", nil
}

func (s *Service) lookupRate(ctx context.Context, tenantID, fromCurrency, toCurrency string) (decimal.Decimal, error) {
	if s.RateLookup != nil {
		return s.RateLookup(ctx, tenantID, fromCurrency, toCurrency)
	}
	rate, err := s.Store.GetActiveRate(ctx, tenantID, fromCurrency, toCurrency)
	if err != nil {
		return decimal.Zero, err
	}
	return rate.SellRate, nil
}
