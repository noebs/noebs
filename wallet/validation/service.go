package validation

import (
	"context"
	"strings"
	"time"

	walletfees "github.com/adonese/noebs/wallet/fees"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type P2PValidationRequest struct {
	TenantID        string
	TransactionType string
	FromWalletID    uuid.UUID
	ToWalletID      uuid.UUID
	Currency        string
	Amount          int64
	FromOwnerType   string
	FromOwnerID     string
	ToOwnerType     string
	ToOwnerID       string
}

type P2PValidationResult struct {
	FromWalletID   uuid.UUID
	ToWalletID     uuid.UUID
	Currency       string
	CurrencyUnitID int64
	Amount         int64
	Fee            *walletfees.FeeResult
	TotalDebit     int64
}

type P2PRule func(ctx context.Context, req P2PValidationRequest, fromWallet, toWallet *walletstore.Wallet) error

type DepositValidationRequest struct {
	TenantID        string
	TransactionType string
	ProviderCode    string
	TransactionID   string
	WalletID        uuid.UUID
	Currency        string
	Amount          int64
	OwnerType       string
	OwnerID         string
	Region          string
}

type DepositValidationResult struct {
	WalletID           uuid.UUID
	Currency           string
	CurrencyUnitID     int64
	Amount             int64
	Fee                *walletfees.FeeResult
	NetAmount          int64
	SupportsWithdrawal bool
}

type DepositRule func(ctx context.Context, req DepositValidationRequest, wallet *walletstore.Wallet, cfg *walletstore.PSPConfig) error

type WithdrawalValidationRequest struct {
	TenantID        string
	TransactionType string
	ProviderCode    string
	WalletID        uuid.UUID
	Currency        string
	CurrencyUnitID  int64
	Amount          int64
	OwnerType       string
	OwnerID         string
	Region          string
}

type WithdrawalValidationResult struct {
	WalletID              uuid.UUID
	Currency              string
	Amount                int64
	Fee                   *walletfees.FeeResult
	TotalDebit            int64
	PayoutAmount          int64
	PayoutCurrency        string
	PayoutCurrencyUnitID  int64
	WalletDebitAmount     int64
	WalletCurrency        string
	WalletCurrencyUnitID  int64
	AppliedFXRate         decimal.NullDecimal
	AppliedFXSource       string
	AppliedFXConversionAt time.Time
}

type WithdrawalRule func(ctx context.Context, req WithdrawalValidationRequest, wallet *walletstore.Wallet, cfg *walletstore.PSPConfig) error

type Service struct {
	Store              *walletstore.Store
	RateLookup         func(ctx context.Context, tenantID, baseCurrency string, baseCurrencyUnitID int64, quoteCurrency string, quoteCurrencyUnitID int64, asOf time.Time) (*walletstore.ExchangeRate, error)
	CurrencyUnitLookup func(ctx context.Context, currencyUnitID int64) (*walletstore.CurrencyUnitVersion, error)
	P2PRules           []P2PRule
	DepositRules       []DepositRule
	WithdrawalRules    []WithdrawalRule
}

func ValidateP2PRequest(req P2PValidationRequest) error {
	if _, err := walletstore.ValidateTenantID(req.TenantID); err != nil {
		return err
	}
	if missingRequiredText(req.TransactionType) {
		return walletstore.ErrMissingTransactionType
	}
	if missingRequiredText(req.Currency) {
		return walletstore.ErrMissingCurrency
	}
	if req.FromWalletID == uuid.Nil || req.ToWalletID == uuid.Nil {
		return walletstore.ErrMissingWalletID
	}
	if req.FromWalletID == req.ToWalletID {
		return walletstore.ErrInvalidWalletPair
	}
	if req.Amount <= 0 {
		return walletstore.ErrInvalidAmount
	}
	return nil
}

func ValidateDepositRequest(req DepositValidationRequest) error {
	if _, err := walletstore.ValidateTenantID(req.TenantID); err != nil {
		return err
	}
	if missingRequiredText(req.TransactionType) {
		return walletstore.ErrMissingTransactionType
	}
	if missingRequiredText(req.ProviderCode) {
		return walletstore.ErrMissingProviderCode
	}
	if missingRequiredText(req.Currency) {
		return walletstore.ErrMissingCurrency
	}
	if req.WalletID == uuid.Nil {
		return walletstore.ErrMissingWalletID
	}
	if req.Amount <= 0 {
		return walletstore.ErrInvalidAmount
	}
	return nil
}

func ValidateWithdrawalRequest(req WithdrawalValidationRequest) error {
	if _, err := walletstore.ValidateTenantID(req.TenantID); err != nil {
		return err
	}
	if missingRequiredText(req.TransactionType) {
		return walletstore.ErrMissingTransactionType
	}
	if missingRequiredText(req.ProviderCode) {
		return walletstore.ErrMissingProviderCode
	}
	if missingRequiredText(req.Currency) {
		return walletstore.ErrMissingCurrency
	}
	if err := walletstore.ValidateCurrencyUnitID(req.CurrencyUnitID); err != nil {
		return err
	}
	if req.WalletID == uuid.Nil {
		return walletstore.ErrMissingWalletID
	}
	if req.Amount <= 0 {
		return walletstore.ErrInvalidAmount
	}
	return nil
}

func (s *Service) ValidateP2P(ctx context.Context, req P2PValidationRequest) (*P2PValidationResult, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	if err := ValidateP2PRequest(req); err != nil {
		return nil, err
	}

	fromWallet, err := s.Store.GetWallet(ctx, req.TenantID, req.FromWalletID)
	if err != nil {
		return nil, err
	}
	toWallet, err := s.Store.GetWallet(ctx, req.TenantID, req.ToWalletID)
	if err != nil {
		return nil, err
	}
	if fromWallet.Status != walletstore.WalletStatusActive || toWallet.Status != walletstore.WalletStatusActive {
		return nil, ErrWalletInactive
	}
	if fromWallet.Currency != req.Currency || toWallet.Currency != req.Currency {
		return nil, walletstore.ErrCurrencyMismatch
	}
	if err := walletstore.ValidateCurrencyUnitID(fromWallet.CurrencyUnitID); err != nil {
		return nil, err
	}
	if err := walletstore.ValidateCurrencyUnitID(toWallet.CurrencyUnitID); err != nil {
		return nil, err
	}
	if fromWallet.CurrencyUnitID != toWallet.CurrencyUnitID {
		return nil, walletstore.ErrCurrencyMismatch
	}
	if req.FromOwnerType != "" && fromWallet.OwnerType != req.FromOwnerType {
		return nil, ErrWalletOwnerMismatch
	}
	if req.FromOwnerID != "" && fromWallet.OwnerID != req.FromOwnerID {
		return nil, ErrWalletOwnerMismatch
	}
	if req.ToOwnerType != "" && toWallet.OwnerType != req.ToOwnerType {
		return nil, ErrWalletOwnerMismatch
	}
	if req.ToOwnerID != "" && toWallet.OwnerID != req.ToOwnerID {
		return nil, ErrWalletOwnerMismatch
	}

	feeEngine := walletfees.FeeEngine{Store: s.Store}
	feeResult, err := feeEngine.Calculate(ctx, req.TenantID, req.TransactionType, req.Currency, fromWallet.CurrencyUnitID, req.Amount)
	if err != nil {
		return nil, err
	}
	totalDebit, err := checkedAddInt64(req.Amount, feeResult.TotalFee)
	if err != nil {
		return nil, err
	}
	if fromWallet.AvailableBalance < totalDebit {
		return nil, walletstore.ErrInsufficientFunds
	}

	for _, rule := range s.P2PRules {
		if rule == nil {
			continue
		}
		if err := rule(ctx, req, fromWallet, toWallet); err != nil {
			return nil, err
		}
	}

	return &P2PValidationResult{
		FromWalletID:   req.FromWalletID,
		ToWalletID:     req.ToWalletID,
		Currency:       req.Currency,
		CurrencyUnitID: fromWallet.CurrencyUnitID,
		Amount:         req.Amount,
		Fee:            feeResult,
		TotalDebit:     totalDebit,
	}, nil
}

func (s *Service) ValidateDeposit(ctx context.Context, req DepositValidationRequest) (*DepositValidationResult, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	if err := ValidateDepositRequest(req); err != nil {
		return nil, err
	}

	wallet, err := s.Store.GetWallet(ctx, req.TenantID, req.WalletID)
	if err != nil {
		return nil, err
	}
	if err := validateDepositWallet(wallet, req); err != nil {
		return nil, err
	}

	cfg, _, err := s.Store.ResolvePSPConfig(ctx, req.TenantID, req.ProviderCode, walletstore.PSPConfigScope{
		Region:         req.Region,
		Currency:       req.Currency,
		CurrencyUnitID: wallet.CurrencyUnitID,
		Direction:      "deposit",
	})
	if err != nil {
		return nil, err
	}
	if err := ValidatePSPConfig(cfg, req.Currency, "deposit"); err != nil {
		return nil, err
	}
	if err := ValidatePSPConfigAmount(cfg, wallet.CurrencyUnitID, req.Amount); err != nil {
		return nil, err
	}

	feeEngine := walletfees.FeeEngine{Store: s.Store}
	feeResult, err := feeEngine.Calculate(ctx, req.TenantID, req.TransactionType, req.Currency, wallet.CurrencyUnitID, req.Amount)
	if err != nil {
		return nil, err
	}
	netAmount, err := checkedSubtractInt64(req.Amount, feeResult.TotalFee)
	if err != nil {
		return nil, err
	}
	if netAmount < 0 {
		return nil, ErrFeeExceedsAmount
	}

	for _, rule := range s.DepositRules {
		if rule == nil {
			continue
		}
		if err := rule(ctx, req, wallet, cfg); err != nil {
			return nil, err
		}
	}

	return &DepositValidationResult{
		WalletID:           req.WalletID,
		Currency:           req.Currency,
		CurrencyUnitID:     wallet.CurrencyUnitID,
		Amount:             req.Amount,
		Fee:                feeResult,
		NetAmount:          netAmount,
		SupportsWithdrawal: cfg.SupportsWithdrawal,
	}, nil
}

func (s *Service) ValidateWithdrawal(ctx context.Context, req WithdrawalValidationRequest) (*WithdrawalValidationResult, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	if err := ValidateWithdrawalRequest(req); err != nil {
		return nil, err
	}

	wallet, err := s.Store.GetWallet(ctx, req.TenantID, req.WalletID)
	if err != nil {
		return nil, err
	}
	if err := validateWithdrawalWallet(wallet, req); err != nil {
		return nil, err
	}

	cfg, _, err := s.Store.ResolvePSPConfig(ctx, req.TenantID, req.ProviderCode, walletstore.PSPConfigScope{
		Region:         req.Region,
		Currency:       req.Currency,
		CurrencyUnitID: req.CurrencyUnitID,
		Direction:      "withdrawal",
	})
	if err != nil {
		return nil, err
	}
	if err := ValidatePSPConfig(cfg, req.Currency, "withdrawal"); err != nil {
		return nil, err
	}
	if err := ValidatePSPConfigAmount(cfg, req.CurrencyUnitID, req.Amount); err != nil {
		return nil, err
	}

	conversionAt := time.Now().UTC().Truncate(time.Microsecond)
	walletDebitAmount, appliedRate, appliedSource, err := s.convertWithdrawalAmountAt(
		ctx,
		req.TenantID,
		req.Amount,
		req.Currency,
		req.CurrencyUnitID,
		wallet.Currency,
		wallet.CurrencyUnitID,
		conversionAt,
	)
	if err != nil {
		return nil, err
	}

	feeEngine := walletfees.FeeEngine{Store: s.Store}
	feeResult, err := feeEngine.Calculate(ctx, req.TenantID, req.TransactionType, wallet.Currency, wallet.CurrencyUnitID, walletDebitAmount)
	if err != nil {
		return nil, err
	}
	totalDebit, err := checkedAddInt64(walletDebitAmount, feeResult.TotalFee)
	if err != nil {
		return nil, err
	}
	if wallet.AvailableBalance < totalDebit {
		return nil, walletstore.ErrInsufficientFunds
	}

	for _, rule := range s.WithdrawalRules {
		if rule == nil {
			continue
		}
		if err := rule(ctx, req, wallet, cfg); err != nil {
			return nil, err
		}
	}

	if !appliedRate.Valid {
		conversionAt = time.Time{}
	}
	return &WithdrawalValidationResult{
		WalletID:              req.WalletID,
		Currency:              wallet.Currency,
		Amount:                walletDebitAmount,
		Fee:                   feeResult,
		TotalDebit:            totalDebit,
		PayoutAmount:          req.Amount,
		PayoutCurrency:        req.Currency,
		PayoutCurrencyUnitID:  req.CurrencyUnitID,
		WalletDebitAmount:     walletDebitAmount,
		WalletCurrency:        wallet.Currency,
		WalletCurrencyUnitID:  wallet.CurrencyUnitID,
		AppliedFXRate:         appliedRate,
		AppliedFXSource:       appliedSource,
		AppliedFXConversionAt: conversionAt,
	}, nil
}

func (s *Service) convertWithdrawalAmount(ctx context.Context, tenantID string, payoutAmount int64, payoutCurrency string, payoutCurrencyUnitID int64, walletCurrency string, walletCurrencyUnitID int64) (int64, decimal.NullDecimal, string, error) {
	if err := walletstore.ValidateCurrencyUnitID(payoutCurrencyUnitID); err != nil {
		return 0, decimal.NullDecimal{}, "", err
	}
	if err := walletstore.ValidateCurrencyUnitID(walletCurrencyUnitID); err != nil {
		return 0, decimal.NullDecimal{}, "", err
	}
	return s.convertWithdrawalAmountAt(ctx, tenantID, payoutAmount, payoutCurrency, payoutCurrencyUnitID, walletCurrency, walletCurrencyUnitID, time.Now().UTC().Truncate(time.Microsecond))
}

func (s *Service) convertWithdrawalAmountAt(ctx context.Context, tenantID string, payoutAmount int64, payoutCurrency string, payoutCurrencyUnitID int64, walletCurrency string, walletCurrencyUnitID int64, asOf time.Time) (int64, decimal.NullDecimal, string, error) {
	if payoutAmount <= 0 {
		return 0, decimal.NullDecimal{}, "", walletstore.ErrInvalidAmount
	}
	if err := walletstore.ValidateCurrencyUnitID(payoutCurrencyUnitID); err != nil {
		return 0, decimal.NullDecimal{}, "", err
	}
	if err := walletstore.ValidateCurrencyUnitID(walletCurrencyUnitID); err != nil {
		return 0, decimal.NullDecimal{}, "", err
	}
	if payoutCurrency == walletCurrency {
		if _, err := s.requireCurrencyUnitIdentity(ctx, payoutCurrency, payoutCurrencyUnitID); err != nil {
			return 0, decimal.NullDecimal{}, "", err
		}
		if _, err := s.requireCurrencyUnitIdentity(ctx, walletCurrency, walletCurrencyUnitID); err != nil {
			return 0, decimal.NullDecimal{}, "", err
		}
		if payoutCurrencyUnitID != walletCurrencyUnitID {
			return 0, decimal.NullDecimal{}, "", walletstore.ErrCurrencyMismatch
		}
		return payoutAmount, decimal.NullDecimal{}, "", nil
	}
	if s.RateLookup != nil {
		rate, err := s.RateLookup(ctx, tenantID, payoutCurrency, payoutCurrencyUnitID, walletCurrency, walletCurrencyUnitID, asOf)
		if err != nil {
			return 0, decimal.NullDecimal{}, "", err
		}
		converted, err := s.convertUsingExchangeRate(ctx, payoutAmount, payoutCurrency, walletCurrency, payoutCurrencyUnitID, walletCurrencyUnitID, rate)
		if err != nil {
			return 0, decimal.NullDecimal{}, "", err
		}
		return converted, decimal.NullDecimal{Decimal: rate.SellRate, Valid: true}, "rates", nil
	}
	if s.Store == nil {
		return 0, decimal.NullDecimal{}, "", ErrMissingStore
	}
	rate, err := s.Store.GetActiveRateForUnitsAt(
		ctx,
		tenantID,
		payoutCurrency,
		payoutCurrencyUnitID,
		walletCurrency,
		walletCurrencyUnitID,
		asOf,
	)
	if err != nil {
		return 0, decimal.NullDecimal{}, "", err
	}
	converted, err := s.convertUsingExchangeRate(ctx, payoutAmount, payoutCurrency, walletCurrency, payoutCurrencyUnitID, walletCurrencyUnitID, rate)
	if err != nil {
		return 0, decimal.NullDecimal{}, "", err
	}
	return converted, decimal.NullDecimal{Decimal: rate.SellRate, Valid: true}, "rates", nil
}

func (s *Service) getCurrencyUnit(ctx context.Context, currencyUnitID int64) (*walletstore.CurrencyUnitVersion, error) {
	if err := walletstore.ValidateCurrencyUnitID(currencyUnitID); err != nil {
		return nil, err
	}
	if s.CurrencyUnitLookup != nil {
		return s.CurrencyUnitLookup(ctx, currencyUnitID)
	}
	if s.Store == nil {
		return nil, ErrMissingStore
	}
	return s.Store.GetCurrencyUnitByID(ctx, currencyUnitID)
}

func (s *Service) requireCurrencyUnitIdentity(ctx context.Context, currency string, currencyUnitID int64) (*walletstore.CurrencyUnitVersion, error) {
	unit, err := s.getCurrencyUnit(ctx, currencyUnitID)
	if err != nil {
		return nil, err
	}
	if unit == nil {
		return nil, walletstore.ErrCurrencyNotFound
	}
	if unit.ID != currencyUnitID || unit.CurrencyCode != currency {
		return nil, walletstore.ErrCurrencyMismatch
	}
	return unit, nil
}

func (s *Service) convertUsingExchangeRate(ctx context.Context, amount int64, fromCurrency, toCurrency string, expectedBaseUnitID, expectedQuoteUnitID int64, rate *walletstore.ExchangeRate) (int64, error) {
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
	if expectedBaseUnitID > 0 && rate.BaseCurrencyUnitID != expectedBaseUnitID {
		return 0, walletstore.ErrCurrencyMismatch
	}
	if expectedQuoteUnitID > 0 && rate.QuoteCurrencyUnitID != expectedQuoteUnitID {
		return 0, walletstore.ErrCurrencyMismatch
	}
	baseUnit, err := s.getCurrencyUnit(ctx, rate.BaseCurrencyUnitID)
	if err != nil {
		return 0, err
	}
	if baseUnit == nil {
		return 0, walletstore.ErrCurrencyNotFound
	}
	if baseUnit.CurrencyCode != fromCurrency {
		return 0, walletstore.ErrCurrencyMismatch
	}
	quoteUnit, err := s.getCurrencyUnit(ctx, rate.QuoteCurrencyUnitID)
	if err != nil {
		return 0, err
	}
	if quoteUnit == nil {
		return 0, walletstore.ErrCurrencyNotFound
	}
	if quoteUnit.CurrencyCode != toCurrency {
		return 0, walletstore.ErrCurrencyMismatch
	}
	return convertPositiveAmountWithUnits(amount, rate.SellRate, baseUnit, quoteUnit)
}

func validateWallet(wallet *walletstore.Wallet, currency, ownerType, ownerID string) error {
	if wallet.Status != walletstore.WalletStatusActive {
		return ErrWalletInactive
	}
	if currency != "" && wallet.Currency != currency {
		return walletstore.ErrCurrencyMismatch
	}
	if ownerType != "" && wallet.OwnerType != ownerType {
		return ErrWalletOwnerMismatch
	}
	if ownerID != "" && wallet.OwnerID != ownerID {
		return ErrWalletOwnerMismatch
	}
	return nil
}

func validateDepositWallet(wallet *walletstore.Wallet, req DepositValidationRequest) error {
	return validateWallet(wallet, req.Currency, req.OwnerType, req.OwnerID)
}

func validateWithdrawalWallet(wallet *walletstore.Wallet, req WithdrawalValidationRequest) error {
	return validateWallet(wallet, "", req.OwnerType, req.OwnerID)
}

func ValidatePSPConfig(cfg *walletstore.PSPConfig, currency, direction string) error {
	if err := ValidatePSPConfigBase(cfg); err != nil {
		return err
	}
	switch direction {
	case "deposit":
		if !cfg.SupportsDeposit {
			return ErrPSPDirectionInvalid
		}
	case "withdrawal":
		if !cfg.SupportsWithdrawal {
			return ErrPSPDirectionInvalid
		}
	}
	currency = strings.TrimSpace(currency)
	if currency == "" {
		return walletstore.ErrMissingCurrency
	}
	for _, allowed := range cfg.EnabledCurrencies {
		if strings.EqualFold(currency, strings.TrimSpace(allowed)) {
			return nil
		}
	}
	return ErrPSPCurrencyInvalid
}

func ValidatePSPConfigBase(cfg *walletstore.PSPConfig) error {
	if cfg == nil {
		return walletstore.ErrPSPConfigNotFound
	}
	if !cfg.IsActive {
		return ErrPSPConfigDisabled
	}
	if len(cfg.EnabledCurrencies) == 0 {
		return ErrPSPConfigMissingCurrencies
	}
	if !cfg.IdempotencyHeaderName.Valid ||
		strings.TrimSpace(cfg.IdempotencyHeaderName.String) == "" ||
		strings.TrimSpace(cfg.IdempotencyHeaderName.String) != cfg.IdempotencyHeaderName.String {
		return ErrPSPConfigMissingIdempotency
	}
	return nil
}

func ValidatePSPConfigAmount(cfg *walletstore.PSPConfig, currencyUnitID, amount int64) error {
	if cfg == nil {
		return walletstore.ErrPSPConfigNotFound
	}
	if amount <= 0 {
		return walletstore.ErrInvalidAmount
	}
	if cfg.MinAmount.Valid || cfg.MaxAmount.Valid {
		if err := walletstore.ValidateCurrencyUnitID(currencyUnitID); err != nil {
			return err
		}
		if err := walletstore.ValidateCurrencyUnitID(cfg.AmountCurrencyUnitID); err != nil {
			return err
		}
		if cfg.AmountCurrencyUnitID != currencyUnitID {
			return walletstore.ErrCurrencyMismatch
		}
	}
	if cfg.MinAmount.Valid && amount < cfg.MinAmount.Int64 {
		return walletstore.ErrInvalidAmount
	}
	if cfg.MaxAmount.Valid && amount > cfg.MaxAmount.Int64 {
		return walletstore.ErrInvalidAmount
	}
	return nil
}

func missingRequiredText(value string) bool {
	return strings.TrimSpace(value) == ""
}
