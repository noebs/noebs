package validation

import (
	"database/sql"
	"errors"
	"testing"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

func TestPSPAmountResolutionDistinguishesMissingAndInvalidUnitIDs(t *testing.T) {
	service := &Service{Store: &walletstore.Store{}}
	valid := PSPAmountResolutionRequest{
		TenantID:                 "tenant",
		RequestedAmount:          100,
		RequestedCurrency:        "USD",
		RequestedCurrencyUnitID:  1,
		SettlementAmount:         100,
		SettlementCurrency:       "USD",
		SettlementCurrencyUnitID: 1,
		WalletCurrency:           "USD",
		WalletCurrencyUnitID:     1,
	}

	fields := []struct {
		name   string
		mutate func(*PSPAmountResolutionRequest, int64)
	}{
		{name: "requested", mutate: func(req *PSPAmountResolutionRequest, id int64) { req.RequestedCurrencyUnitID = id }},
		{name: "settlement", mutate: func(req *PSPAmountResolutionRequest, id int64) { req.SettlementCurrencyUnitID = id }},
		{name: "wallet", mutate: func(req *PSPAmountResolutionRequest, id int64) { req.WalletCurrencyUnitID = id }},
	}
	unitCases := []struct {
		name string
		id   int64
		want error
	}{
		{name: "zero is missing", want: walletstore.ErrMissingCurrencyUnitID},
		{name: "negative is invalid", id: -1, want: walletstore.ErrInvalidCurrencyUnitID},
	}

	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			for _, unitCase := range unitCases {
				t.Run(unitCase.name, func(t *testing.T) {
					req := valid
					field.mutate(&req, unitCase.id)
					_, err := service.ResolvePSPDepositAmounts(t.Context(), req)
					if !errors.Is(err, unitCase.want) {
						t.Fatalf("ResolvePSPDepositAmounts() error = %v, want %v", err, unitCase.want)
					}
				})
			}
		})
	}
}

func TestPSPConfigAmountDistinguishesMissingAndInvalidUnitIDs(t *testing.T) {
	valid := &walletstore.PSPConfig{
		MinAmount:            sql.NullInt64{Int64: 1, Valid: true},
		AmountCurrencyUnitID: 1,
	}
	tests := []struct {
		name           string
		currencyUnitID int64
		configUnitID   int64
		want           error
	}{
		{name: "missing requested unit", configUnitID: 1, want: walletstore.ErrMissingCurrencyUnitID},
		{name: "invalid requested unit", currencyUnitID: -1, configUnitID: 1, want: walletstore.ErrInvalidCurrencyUnitID},
		{name: "missing configured unit", currencyUnitID: 1, want: walletstore.ErrMissingCurrencyUnitID},
		{name: "invalid configured unit", currencyUnitID: 1, configUnitID: -1, want: walletstore.ErrInvalidCurrencyUnitID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := *valid
			cfg.AmountCurrencyUnitID = tt.configUnitID
			if err := ValidatePSPConfigAmount(&cfg, tt.currencyUnitID, 100); !errors.Is(err, tt.want) {
				t.Fatalf("ValidatePSPConfigAmount() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestWithdrawalRequestRejectsNegativeCurrencyUnitIDAsInvalid(t *testing.T) {
	err := ValidateWithdrawalRequest(WithdrawalValidationRequest{
		TenantID:        "tenant",
		TransactionType: "withdrawal",
		ProviderCode:    "provider",
		WalletID:        uuid.New(),
		Currency:        "USD",
		CurrencyUnitID:  -1,
		Amount:          100,
	})
	if !errors.Is(err, walletstore.ErrInvalidCurrencyUnitID) {
		t.Fatalf("ValidateWithdrawalRequest() error = %v, want %v", err, walletstore.ErrInvalidCurrencyUnitID)
	}
}
