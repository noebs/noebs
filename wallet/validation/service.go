package validation

import (
	"context"
	"strings"

	walletfees "github.com/adonese/noebs/wallet/fees"
	walletlimits "github.com/adonese/noebs/wallet/limits"
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
	FromWalletID uuid.UUID
	ToWalletID   uuid.UUID
	Currency     string
	Amount       int64
	Fee          *walletfees.FeeResult
	TotalDebit   int64
	Limits       *walletlimits.CheckResult
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
	Amount             int64
	Fee                *walletfees.FeeResult
	NetAmount          int64
	Limits             *walletlimits.CheckResult
	SupportsWithdrawal bool
}

type DepositRule func(ctx context.Context, req DepositValidationRequest, wallet *walletstore.Wallet, cfg *walletstore.PSPConfig) error

type WithdrawalValidationRequest struct {
	TenantID        string
	TransactionType string
	ProviderCode    string
	WalletID        uuid.UUID
	Currency        string
	Amount          int64
	OwnerType       string
	OwnerID         string
	Region          string
}

type WithdrawalValidationResult struct {
	WalletID          uuid.UUID
	Currency          string
	Amount            int64
	Fee               *walletfees.FeeResult
	TotalDebit        int64
	Limits            *walletlimits.CheckResult
	PayoutAmount      int64
	PayoutCurrency    string
	WalletDebitAmount int64
	WalletCurrency    string
	AppliedFXRate     decimal.NullDecimal
	AppliedFXSource   string
}

type WithdrawalRule func(ctx context.Context, req WithdrawalValidationRequest, wallet *walletstore.Wallet, cfg *walletstore.PSPConfig) error

type Service struct {
	Store           *walletstore.Store
	RateLookup      func(ctx context.Context, tenantID, baseCurrency, quoteCurrency string) (decimal.Decimal, error)
	P2PRules        []P2PRule
	DepositRules    []DepositRule
	WithdrawalRules []WithdrawalRule
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
	feeResult, err := feeEngine.Calculate(ctx, req.TenantID, req.TransactionType, req.Currency, req.Amount)
	if err != nil {
		return nil, err
	}
	totalDebit := req.Amount + feeResult.TotalFee
	if fromWallet.AvailableBalance < totalDebit {
		return nil, walletstore.ErrInsufficientFunds
	}

	limitEnforcer := walletlimits.Enforcer{Store: s.Store}
	limitResult, err := limitEnforcer.Check(ctx, req.TenantID, req.FromWalletID, req.TransactionType, req.Currency, req.Amount)
	if err != nil {
		return nil, err
	}
	if !limitResult.Allowed {
		return nil, LimitExceededError{Reason: limitResult.Reason}
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
		FromWalletID: req.FromWalletID,
		ToWalletID:   req.ToWalletID,
		Currency:     req.Currency,
		Amount:       req.Amount,
		Fee:          feeResult,
		TotalDebit:   totalDebit,
		Limits:       limitResult,
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
		Region:    req.Region,
		Currency:  req.Currency,
		Direction: "deposit",
	})
	if err != nil {
		return nil, err
	}
	if err := ValidatePSPConfig(cfg, req.Currency, "deposit"); err != nil {
		return nil, err
	}
	if err := ValidatePSPConfigAmount(cfg, req.Amount); err != nil {
		return nil, err
	}

	feeEngine := walletfees.FeeEngine{Store: s.Store}
	feeResult, err := feeEngine.Calculate(ctx, req.TenantID, req.TransactionType, req.Currency, req.Amount)
	if err != nil {
		return nil, err
	}
	netAmount := req.Amount - feeResult.TotalFee
	if netAmount < 0 {
		return nil, ErrFeeExceedsAmount
	}

	limitEnforcer := walletlimits.Enforcer{Store: s.Store}
	limitResult, err := limitEnforcer.Check(ctx, req.TenantID, req.WalletID, req.TransactionType, req.Currency, req.Amount)
	if err != nil {
		return nil, err
	}
	if !limitResult.Allowed {
		return nil, LimitExceededError{Reason: limitResult.Reason}
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
		Amount:             req.Amount,
		Fee:                feeResult,
		NetAmount:          netAmount,
		Limits:             limitResult,
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
		Region:    req.Region,
		Currency:  req.Currency,
		Direction: "withdrawal",
	})
	if err != nil {
		return nil, err
	}
	if err := ValidatePSPConfig(cfg, req.Currency, "withdrawal"); err != nil {
		return nil, err
	}
	if err := ValidatePSPConfigAmount(cfg, req.Amount); err != nil {
		return nil, err
	}

	walletDebitAmount, appliedRate, appliedSource, err := s.convertWithdrawalAmount(ctx, req.TenantID, req.Amount, req.Currency, wallet.Currency)
	if err != nil {
		return nil, err
	}

	feeEngine := walletfees.FeeEngine{Store: s.Store}
	feeResult, err := feeEngine.Calculate(ctx, req.TenantID, req.TransactionType, wallet.Currency, walletDebitAmount)
	if err != nil {
		return nil, err
	}
	totalDebit := walletDebitAmount + feeResult.TotalFee
	if wallet.AvailableBalance < totalDebit {
		return nil, walletstore.ErrInsufficientFunds
	}

	limitEnforcer := walletlimits.Enforcer{Store: s.Store}
	limitResult, err := limitEnforcer.Check(ctx, req.TenantID, req.WalletID, req.TransactionType, wallet.Currency, walletDebitAmount)
	if err != nil {
		return nil, err
	}
	if !limitResult.Allowed {
		return nil, LimitExceededError{Reason: limitResult.Reason}
	}

	for _, rule := range s.WithdrawalRules {
		if rule == nil {
			continue
		}
		if err := rule(ctx, req, wallet, cfg); err != nil {
			return nil, err
		}
	}

	return &WithdrawalValidationResult{
		WalletID:          req.WalletID,
		Currency:          wallet.Currency,
		Amount:            walletDebitAmount,
		Fee:               feeResult,
		TotalDebit:        totalDebit,
		Limits:            limitResult,
		PayoutAmount:      req.Amount,
		PayoutCurrency:    req.Currency,
		WalletDebitAmount: walletDebitAmount,
		WalletCurrency:    wallet.Currency,
		AppliedFXRate:     appliedRate,
		AppliedFXSource:   appliedSource,
	}, nil
}

func (s *Service) convertWithdrawalAmount(ctx context.Context, tenantID string, payoutAmount int64, payoutCurrency, walletCurrency string) (int64, decimal.NullDecimal, string, error) {
	if payoutAmount <= 0 {
		return 0, decimal.NullDecimal{}, "", walletstore.ErrInvalidAmount
	}
	if payoutCurrency == walletCurrency {
		return payoutAmount, decimal.NullDecimal{}, "", nil
	}
	if s.RateLookup != nil {
		rate, err := s.RateLookup(ctx, tenantID, payoutCurrency, walletCurrency)
		if err != nil {
			return 0, decimal.NullDecimal{}, "", err
		}
		converted, err := convertPositiveAmount(payoutAmount, rate)
		if err != nil {
			return 0, decimal.NullDecimal{}, "", err
		}
		return converted, decimal.NullDecimal{Decimal: rate, Valid: true}, "rates", nil
	}
	if s.Store == nil {
		return 0, decimal.NullDecimal{}, "", ErrMissingStore
	}
	rate, err := s.Store.GetActiveRate(ctx, tenantID, payoutCurrency, walletCurrency)
	if err != nil {
		return 0, decimal.NullDecimal{}, "", err
	}
	converted, err := convertPositiveAmount(payoutAmount, rate.SellRate)
	if err != nil {
		return 0, decimal.NullDecimal{}, "", err
	}
	return converted, decimal.NullDecimal{Decimal: rate.SellRate, Valid: true}, "rates", nil
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
	return nil
}

func ValidatePSPConfigAmount(cfg *walletstore.PSPConfig, amount int64) error {
	if cfg == nil {
		return walletstore.ErrPSPConfigNotFound
	}
	if amount <= 0 {
		return walletstore.ErrInvalidAmount
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
