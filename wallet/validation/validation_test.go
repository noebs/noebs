package validation

import (
	"context"
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
		{"missing-tx-type", func(req *P2PValidationRequest) { req.TransactionType = "" }, walletstore.ErrMissingTransactionType},
		{"missing-currency", func(req *P2PValidationRequest) { req.Currency = "" }, walletstore.ErrMissingCurrency},
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
		{"missing-tx-type", func(req *DepositValidationRequest) { req.TransactionType = "" }, walletstore.ErrMissingTransactionType},
		{"missing-provider", func(req *DepositValidationRequest) { req.ProviderCode = "" }, walletstore.ErrMissingProviderCode},
		{"missing-currency", func(req *DepositValidationRequest) { req.Currency = "" }, walletstore.ErrMissingCurrency},
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
		{"missing-tx-type", func(req *WithdrawalValidationRequest) { req.TransactionType = "" }, walletstore.ErrMissingTransactionType},
		{"missing-provider", func(req *WithdrawalValidationRequest) { req.ProviderCode = "" }, walletstore.ErrMissingProviderCode},
		{"missing-currency", func(req *WithdrawalValidationRequest) { req.Currency = "" }, walletstore.ErrMissingCurrency},
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
