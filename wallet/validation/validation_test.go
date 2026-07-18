package validation

import (
	"context"
	"database/sql"
	"testing"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestValidateP2PRequest(t *testing.T) {
	base := P2PValidationRequest{
		TenantID:        "tenant",
		TransactionType: "p2p",
		FromWalletID:    uuid.New(),
		ToWalletID:      uuid.New(),
		Currency:        "USD",
		Amount:          100,
	}

	cases := []struct {
		name    string
		mutate  func(req *P2PValidationRequest)
		wantErr error
	}{
		{"missing-tenant", func(req *P2PValidationRequest) { req.TenantID = "" }, walletstore.ErrMissingTenantID},
		{"reserved-tenant", func(req *P2PValidationRequest) { req.TenantID = "default" }, walletstore.ErrInvalidTenantID},
		{"missing-tx-type", func(req *P2PValidationRequest) { req.TransactionType = "" }, walletstore.ErrMissingTransactionType},
		{"blank-tx-type", func(req *P2PValidationRequest) { req.TransactionType = " \t " }, walletstore.ErrMissingTransactionType},
		{"missing-currency", func(req *P2PValidationRequest) { req.Currency = "" }, walletstore.ErrMissingCurrency},
		{"blank-currency", func(req *P2PValidationRequest) { req.Currency = " \t " }, walletstore.ErrMissingCurrency},
		{"missing-wallet", func(req *P2PValidationRequest) { req.FromWalletID = uuid.Nil }, walletstore.ErrMissingWalletID},
		{"same-wallet", func(req *P2PValidationRequest) { req.ToWalletID = req.FromWalletID }, walletstore.ErrInvalidWalletPair},
		{"invalid-amount", func(req *P2PValidationRequest) { req.Amount = 0 }, walletstore.ErrInvalidAmount},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := base
			tc.mutate(&req)
			err := ValidateP2PRequest(req)
			if err != tc.wantErr {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidateDepositRequest(t *testing.T) {
	base := DepositValidationRequest{
		TenantID:        "tenant",
		TransactionType: "deposit",
		ProviderCode:    "coinsbuy",
		TransactionID:   "tx-1",
		WalletID:        uuid.New(),
		Currency:        "USD",
		Amount:          100,
	}

	cases := []struct {
		name    string
		mutate  func(req *DepositValidationRequest)
		wantErr error
	}{
		{"missing-tenant", func(req *DepositValidationRequest) { req.TenantID = "" }, walletstore.ErrMissingTenantID},
		{"reserved-tenant", func(req *DepositValidationRequest) { req.TenantID = "default" }, walletstore.ErrInvalidTenantID},
		{"missing-tx-type", func(req *DepositValidationRequest) { req.TransactionType = "" }, walletstore.ErrMissingTransactionType},
		{"blank-tx-type", func(req *DepositValidationRequest) { req.TransactionType = " \t " }, walletstore.ErrMissingTransactionType},
		{"missing-provider", func(req *DepositValidationRequest) { req.ProviderCode = "" }, walletstore.ErrMissingProviderCode},
		{"blank-provider", func(req *DepositValidationRequest) { req.ProviderCode = " \t " }, walletstore.ErrMissingProviderCode},
		{"missing-currency", func(req *DepositValidationRequest) { req.Currency = "" }, walletstore.ErrMissingCurrency},
		{"blank-currency", func(req *DepositValidationRequest) { req.Currency = " \t " }, walletstore.ErrMissingCurrency},
		{"missing-wallet", func(req *DepositValidationRequest) { req.WalletID = uuid.Nil }, walletstore.ErrMissingWalletID},
		{"invalid-amount", func(req *DepositValidationRequest) { req.Amount = 0 }, walletstore.ErrInvalidAmount},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := base
			tc.mutate(&req)
			err := ValidateDepositRequest(req)
			if err != tc.wantErr {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidateDepositRequestAllowsMissingProviderTransactionID(t *testing.T) {
	req := DepositValidationRequest{
		TenantID:        "tenant",
		TransactionType: "deposit",
		ProviderCode:    "coinsbuy",
		WalletID:        uuid.New(),
		Currency:        "USD",
		Amount:          100,
	}

	if err := ValidateDepositRequest(req); err != nil {
		t.Fatalf("expected missing provider transaction id to be allowed, got %v", err)
	}
}

func TestValidateDepositWalletRequiresCurrencyMatch(t *testing.T) {
	wallet := &walletstore.Wallet{
		TenantID:  "tenant",
		Currency:  "USD",
		Status:    "active",
		OwnerType: walletstore.OwnerTypeUser,
		OwnerID:   "user-1",
	}
	req := DepositValidationRequest{
		Currency:  "AED",
		OwnerType: walletstore.OwnerTypeUser,
		OwnerID:   "user-1",
	}

	if err := validateDepositWallet(wallet, req); err != walletstore.ErrCurrencyMismatch {
		t.Fatalf("validateDepositWallet() error = %v, want %v", err, walletstore.ErrCurrencyMismatch)
	}

	req.Currency = "USD"
	if err := validateDepositWallet(wallet, req); err != nil {
		t.Fatalf("validateDepositWallet() error = %v, want nil", err)
	}
}

func TestValidateWithdrawalRequest(t *testing.T) {
	base := WithdrawalValidationRequest{
		TenantID:        "tenant",
		TransactionType: "withdrawal",
		ProviderCode:    "coinsbuy",
		WalletID:        uuid.New(),
		Currency:        "USD",
		Amount:          100,
	}

	cases := []struct {
		name    string
		mutate  func(req *WithdrawalValidationRequest)
		wantErr error
	}{
		{"missing-tenant", func(req *WithdrawalValidationRequest) { req.TenantID = "" }, walletstore.ErrMissingTenantID},
		{"reserved-tenant", func(req *WithdrawalValidationRequest) { req.TenantID = "default" }, walletstore.ErrInvalidTenantID},
		{"missing-tx-type", func(req *WithdrawalValidationRequest) { req.TransactionType = "" }, walletstore.ErrMissingTransactionType},
		{"blank-tx-type", func(req *WithdrawalValidationRequest) { req.TransactionType = " \t " }, walletstore.ErrMissingTransactionType},
		{"missing-provider", func(req *WithdrawalValidationRequest) { req.ProviderCode = "" }, walletstore.ErrMissingProviderCode},
		{"blank-provider", func(req *WithdrawalValidationRequest) { req.ProviderCode = " \t " }, walletstore.ErrMissingProviderCode},
		{"missing-currency", func(req *WithdrawalValidationRequest) { req.Currency = "" }, walletstore.ErrMissingCurrency},
		{"blank-currency", func(req *WithdrawalValidationRequest) { req.Currency = " \t " }, walletstore.ErrMissingCurrency},
		{"missing-wallet", func(req *WithdrawalValidationRequest) { req.WalletID = uuid.Nil }, walletstore.ErrMissingWalletID},
		{"invalid-amount", func(req *WithdrawalValidationRequest) { req.Amount = 0 }, walletstore.ErrInvalidAmount},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := base
			tc.mutate(&req)
			err := ValidateWithdrawalRequest(req)
			if err != tc.wantErr {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidateWithdrawalWalletAllowsPayoutCurrencyMismatch(t *testing.T) {
	wallet := &walletstore.Wallet{
		TenantID:  "tenant",
		Currency:  "USD",
		Status:    "active",
		OwnerType: walletstore.OwnerTypeUser,
		OwnerID:   "user-1",
	}
	req := WithdrawalValidationRequest{
		Currency:  "AED",
		OwnerType: walletstore.OwnerTypeUser,
		OwnerID:   "user-1",
	}

	if err := validateWithdrawalWallet(wallet, req); err != nil {
		t.Fatalf("validateWithdrawalWallet() error = %v, want nil", err)
	}

	req.OwnerID = "user-2"
	if err := validateWithdrawalWallet(wallet, req); err != ErrWalletOwnerMismatch {
		t.Fatalf("validateWithdrawalWallet(owner mismatch) error = %v, want %v", err, ErrWalletOwnerMismatch)
	}
}

func TestValidatePSPConfigRequiresExplicitCurrency(t *testing.T) {
	cfg := &walletstore.PSPConfig{
		IsActive:          true,
		SupportsDeposit:   true,
		EnabledCurrencies: walletstore.StringArray{"USD"},
	}

	if err := ValidatePSPConfig(cfg, " ", "deposit"); err != walletstore.ErrMissingCurrency {
		t.Fatalf("ValidatePSPConfig() error = %v, want %v", err, walletstore.ErrMissingCurrency)
	}
}

func TestValidatePSPConfigBaseDoesNotRequireRequestCurrency(t *testing.T) {
	cfg := &walletstore.PSPConfig{
		IsActive:          true,
		EnabledCurrencies: walletstore.StringArray{"USD"},
	}
	if err := ValidatePSPConfigBase(cfg); err != nil {
		t.Fatalf("ValidatePSPConfigBase() error = %v", err)
	}
}

func TestValidatePSPConfigRequiresConfiguredCurrencies(t *testing.T) {
	for _, currencies := range []walletstore.StringArray{nil, walletstore.StringArray{}} {
		cfg := &walletstore.PSPConfig{
			IsActive:          true,
			SupportsDeposit:   true,
			EnabledCurrencies: currencies,
		}

		if err := ValidatePSPConfig(cfg, "USD", "deposit"); err != ErrPSPConfigMissingCurrencies {
			t.Fatalf("ValidatePSPConfig() error = %v, want %v", err, ErrPSPConfigMissingCurrencies)
		}
	}
}

func TestValidatePSPConfigMatchesTrimmedCurrency(t *testing.T) {
	cfg := &walletstore.PSPConfig{
		IsActive:          true,
		SupportsDeposit:   true,
		EnabledCurrencies: walletstore.StringArray{" USD "},
	}

	if err := ValidatePSPConfig(cfg, "usd", "deposit"); err != nil {
		t.Fatalf("ValidatePSPConfig() error = %v, want nil", err)
	}
}

func TestValidatePSPConfigAmountBounds(t *testing.T) {
	cfg := &walletstore.PSPConfig{
		MinAmount: sql.NullInt64{Int64: 100, Valid: true},
		MaxAmount: sql.NullInt64{Int64: 1000, Valid: true},
	}

	cases := []struct {
		name    string
		cfg     *walletstore.PSPConfig
		amount  int64
		wantErr error
	}{
		{"missing-config", nil, 100, walletstore.ErrPSPConfigNotFound},
		{"zero", cfg, 0, walletstore.ErrInvalidAmount},
		{"below-min", cfg, 99, walletstore.ErrInvalidAmount},
		{"at-min", cfg, 100, nil},
		{"within-bounds", cfg, 500, nil},
		{"at-max", cfg, 1000, nil},
		{"above-max", cfg, 1001, walletstore.ErrInvalidAmount},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePSPConfigAmount(tc.cfg, tc.amount); err != tc.wantErr {
				t.Fatalf("ValidatePSPConfigAmount() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestConvertWithdrawalAmountUsesRateLookup(t *testing.T) {
	service := &Service{
		RateLookup: func(ctx context.Context, tenantID, baseCurrency, quoteCurrency string) (decimal.Decimal, error) {
			if tenantID != "tenant" || baseCurrency != "USD" || quoteCurrency != "AED" {
				t.Fatalf("unexpected rate lookup: tenant=%s base=%s quote=%s", tenantID, baseCurrency, quoteCurrency)
			}
			return decimal.RequireFromString("3.67"), nil
		},
	}

	amount, rate, source, err := service.convertWithdrawalAmount(t.Context(), "tenant", 100, "USD", "AED")
	if err != nil {
		t.Fatalf("convert withdrawal amount: %v", err)
	}
	if amount != 367 {
		t.Fatalf("expected converted amount 367, got %d", amount)
	}
	if !rate.Valid || !rate.Decimal.Equal(decimal.RequireFromString("3.67")) {
		t.Fatalf("unexpected applied rate: %+v", rate)
	}
	if source != "rates" {
		t.Fatalf("expected rates source, got %q", source)
	}
}

func TestConvertWithdrawalAmountSameCurrencySkipsRateLookup(t *testing.T) {
	service := &Service{
		RateLookup: func(ctx context.Context, tenantID, baseCurrency, quoteCurrency string) (decimal.Decimal, error) {
			t.Fatal("rate lookup should not be called for same-currency withdrawals")
			return decimal.Zero, nil
		},
	}

	amount, rate, source, err := service.convertWithdrawalAmount(t.Context(), "tenant", 100, "AED", "AED")
	if err != nil {
		t.Fatalf("convert withdrawal amount: %v", err)
	}
	if amount != 100 {
		t.Fatalf("expected same-currency amount 100, got %d", amount)
	}
	if rate.Valid || source != "" {
		t.Fatalf("expected no fx metadata, got rate=%+v source=%q", rate, source)
	}
}

func TestConvertWithdrawalAmountRejectsInvalidFX(t *testing.T) {
	tests := []struct {
		name    string
		amount  int64
		rate    decimal.Decimal
		wantErr error
	}{
		{name: "zero rate", amount: 100, rate: decimal.Zero, wantErr: walletstore.ErrInvalidRate},
		{name: "negative rate", amount: 100, rate: decimal.NewFromInt(-1), wantErr: walletstore.ErrInvalidRate},
		{name: "rounded to zero", amount: 1, rate: decimal.RequireFromString("0.01"), wantErr: walletstore.ErrInvalidAmount},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &Service{
				RateLookup: func(ctx context.Context, tenantID, baseCurrency, quoteCurrency string) (decimal.Decimal, error) {
					return tt.rate, nil
				},
			}
			_, _, _, err := service.convertWithdrawalAmount(t.Context(), "tenant", tt.amount, "USD", "AED")
			if err != tt.wantErr {
				t.Fatalf("convertWithdrawalAmount() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestResolvePSPDepositAmountsSameCurrency(t *testing.T) {
	service := &Service{
		Store: &walletstore.Store{},
		RateLookup: func(ctx context.Context, tenantID, baseCurrency, quoteCurrency string) (decimal.Decimal, error) {
			return decimal.NewFromInt(2), nil
		},
	}
	req := PSPAmountResolutionRequest{
		TenantID:           "tenant",
		RequestedAmount:    100,
		RequestedCurrency:  "USD",
		SettlementAmount:   120,
		SettlementCurrency: "USD",
		WalletCurrency:     "USD",
	}
	result, err := service.ResolvePSPDepositAmounts(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.WalletCreditAmount != 120 {
		t.Fatalf("expected credit 120, got %d", result.WalletCreditAmount)
	}
	if result.VarianceKind != walletstore.PSPAmountOverpayment {
		t.Fatalf("expected overpayment, got %s", result.VarianceKind)
	}
}

func TestResolvePSPDepositAmountsFXRate(t *testing.T) {
	service := &Service{
		Store: &walletstore.Store{},
	}
	req := PSPAmountResolutionRequest{
		TenantID:           "tenant",
		RequestedAmount:    100,
		RequestedCurrency:  "USD",
		SettlementAmount:   100,
		SettlementCurrency: "EUR",
		WalletCurrency:     "USD",
		FXRate:             decimal.NullDecimal{Decimal: decimal.RequireFromString("1.2"), Valid: true},
		FXBaseCurrency:     "EUR",
		FXQuoteCurrency:    "USD",
	}
	result, err := service.ResolvePSPDepositAmounts(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.WalletCreditAmount != 120 {
		t.Fatalf("expected credit 120, got %d", result.WalletCreditAmount)
	}
	if !result.AppliedFXRate.Valid {
		t.Fatalf("expected fx rate")
	}
}

func TestResolvePSPDepositAmountsRateLookup(t *testing.T) {
	service := &Service{
		Store: &walletstore.Store{},
		RateLookup: func(ctx context.Context, tenantID, baseCurrency, quoteCurrency string) (decimal.Decimal, error) {
			return decimal.RequireFromString("1.5"), nil
		},
	}
	req := PSPAmountResolutionRequest{
		TenantID:           "tenant",
		RequestedAmount:    100,
		RequestedCurrency:  "USD",
		SettlementAmount:   100,
		SettlementCurrency: "EUR",
		WalletCurrency:     "USD",
	}
	result, err := service.ResolvePSPDepositAmounts(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.WalletCreditAmount != 150 {
		t.Fatalf("expected credit 150, got %d", result.WalletCreditAmount)
	}
	if result.AppliedFXSource != "rates" {
		t.Fatalf("expected fx source rates, got %s", result.AppliedFXSource)
	}
}

func TestResolvePSPDepositAmountsRejectsInvalidFX(t *testing.T) {
	tests := []struct {
		name    string
		rate    decimal.Decimal
		wantErr error
	}{
		{name: "zero rate", rate: decimal.Zero, wantErr: walletstore.ErrInvalidRate},
		{name: "negative rate", rate: decimal.NewFromInt(-1), wantErr: walletstore.ErrInvalidRate},
		{name: "rounded to zero", rate: decimal.RequireFromString("0.01"), wantErr: walletstore.ErrInvalidAmount},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &Service{Store: &walletstore.Store{}}
			_, err := service.ResolvePSPDepositAmounts(t.Context(), PSPAmountResolutionRequest{
				TenantID:           "tenant",
				RequestedAmount:    1,
				RequestedCurrency:  "EUR",
				SettlementAmount:   1,
				SettlementCurrency: "EUR",
				WalletCurrency:     "USD",
				FXRate:             decimal.NullDecimal{Decimal: tt.rate, Valid: true},
				FXBaseCurrency:     "EUR",
				FXQuoteCurrency:    "USD",
			})
			if err != tt.wantErr {
				t.Fatalf("ResolvePSPDepositAmounts() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestResolvePSPDepositAmountsRejectsBlankCurrenciesBeforeRateLookup(t *testing.T) {
	base := PSPAmountResolutionRequest{
		TenantID:           "tenant",
		RequestedAmount:    100,
		RequestedCurrency:  "USD",
		SettlementAmount:   100,
		SettlementCurrency: "EUR",
		WalletCurrency:     "USD",
	}
	cases := []struct {
		name    string
		mutate  func(req *PSPAmountResolutionRequest)
		wantErr error
	}{
		{
			name:    "requested-currency",
			mutate:  func(req *PSPAmountResolutionRequest) { req.RequestedCurrency = " \t " },
			wantErr: ErrMissingRequestedCurrency,
		},
		{
			name:    "settlement-currency",
			mutate:  func(req *PSPAmountResolutionRequest) { req.SettlementCurrency = " \t " },
			wantErr: ErrMissingSettlementCurrency,
		},
		{
			name:    "wallet-currency",
			mutate:  func(req *PSPAmountResolutionRequest) { req.WalletCurrency = " \t " },
			wantErr: ErrMissingWalletCurrency,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			req := base
			tt.mutate(&req)
			service := &Service{
				Store: &walletstore.Store{},
				RateLookup: func(ctx context.Context, tenantID, baseCurrency, quoteCurrency string) (decimal.Decimal, error) {
					t.Fatal("rate lookup should not run for missing currencies")
					return decimal.Zero, nil
				},
			}

			_, err := service.ResolvePSPDepositAmounts(t.Context(), req)
			if err != tt.wantErr {
				t.Fatalf("ResolvePSPDepositAmounts() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestResolvePSPDepositAmountsRejectsReservedTenant(t *testing.T) {
	service := &Service{Store: &walletstore.Store{}}

	_, err := service.ResolvePSPDepositAmounts(t.Context(), PSPAmountResolutionRequest{
		TenantID:           "default",
		RequestedAmount:    100,
		RequestedCurrency:  "USD",
		SettlementAmount:   100,
		SettlementCurrency: "USD",
		WalletCurrency:     "USD",
	})
	if err != walletstore.ErrInvalidTenantID {
		t.Fatalf("ResolvePSPDepositAmounts() error = %v, want %v", err, walletstore.ErrInvalidTenantID)
	}
}
