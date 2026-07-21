package store

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestRequiredCurrencyUnitBoundariesDistinguishMissingFromInvalid(t *testing.T) {
	store := &Store{}
	asOf := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)

	boundaries := []struct {
		name string
		call func(int64) error
	}{
		{
			name: "ensure wallet",
			call: func(currencyUnitID int64) error {
				_, err := store.EnsureWallet(t.Context(), EnsureWalletParams{
					TenantID:       "tenant",
					OwnerType:      OwnerTypeUser,
					OwnerID:        "42",
					UserID:         42,
					Currency:       "USD",
					CurrencyUnitID: currencyUnitID,
					KYCTier:        KYCTierUnverified,
				})
				return err
			},
		},
		{
			name: "ensure system wallets",
			call: func(currencyUnitID int64) error {
				_, err := store.EnsureSystemWallets(t.Context(), "tenant", "USD", currencyUnitID, KYCTierUnverified)
				return err
			},
		},
		{
			name: "active exchange rate lookup",
			call: func(currencyUnitID int64) error {
				_, err := store.GetActiveRateForUnitsAt(t.Context(), "tenant", "USD", currencyUnitID, "EUR", 2, asOf)
				return err
			},
		},
		{
			name: "create exchange rate",
			call: func(currencyUnitID int64) error {
				_, err := store.CreateExchangeRate(t.Context(), ExchangeRate{
					TenantID:            "tenant",
					BaseCurrency:        "USD",
					BaseCurrencyUnitID:  currencyUnitID,
					QuoteCurrency:       "EUR",
					QuoteCurrencyUnitID: 2,
					BuyRate:             decimal.NewFromInt(1),
					SellRate:            decimal.NewFromInt(1),
					SetByOperatorID:     42,
					EffectiveFrom:       asOf,
				})
				return err
			},
		},
		{
			name: "fee lookup",
			call: func(currencyUnitID int64) error {
				_, err := store.GetFeeConfigForAmount(t.Context(), "tenant", "deposit", "USD", currencyUnitID, 100)
				return err
			},
		},
		{
			name: "limit lookup",
			call: func(currencyUnitID int64) error {
				_, err := store.GetLimits(t.Context(), "tenant", KYCTierUnverified, "deposit", "USD", currencyUnitID)
				return err
			},
		},
		{
			name: "fx observation",
			call: func(currencyUnitID int64) error {
				input := validFXObservationTestInput(asOf, asOf.Add(time.Minute))
				input.BaseCurrencyUnitID = currencyUnitID
				return validateFXObservationInput(input)
			},
		},
		{
			name: "conversion quote",
			call: func(currencyUnitID int64) error {
				input := validMoneyConversionQuoteUnitTestInput(asOf)
				input.InputCurrencyUnitID = currencyUnitID
				return validateMoneyConversionQuoteInput(input)
			},
		},
		{
			name: "psp amount policy",
			call: func(currencyUnitID int64) error {
				_, err := validatePSPAmountPolicyScope(PSPConfigScope{
					Currency:       "USD",
					CurrencyUnitID: currencyUnitID,
					Direction:      "deposit",
				})
				return err
			},
		},
		{
			name: "psp transaction amount",
			call: func(currencyUnitID int64) error {
				_, err := validatePSPTransactionAmount(PSPTransactionAmount{
					TenantID:         "tenant",
					PSPTransactionID: 1,
					AmountKind:       PSPAmountRequested,
					Amount:           100,
					Currency:         "USD",
					CurrencyUnitID:   currencyUnitID,
				})
				return err
			},
		},
		{
			name: "psp transaction",
			call: func(currencyUnitID int64) error {
				_, err := store.CreatePSPTransaction(t.Context(), PSPTransaction{
					TenantID:        "tenant",
					PSPProvider:     "provider",
					IdempotencyKey:  "psp-idempotency",
					ClientReference: "psp-reference",
					Direction:       "inbound",
					Amount:          100,
					Currency:        "USD",
					CurrencyUnitID:  currencyUnitID,
					Status:          PSPStatusInitiated,
				})
				return err
			},
		},
		{
			name: "manual transfer",
			call: func(currencyUnitID int64) error {
				_, err := store.CreateManualTransfer(t.Context(), ManualTransfer{
					TenantID:       "tenant",
					WorkflowID:     "manual-workflow",
					IdempotencyKey: "manual-idempotency",
					TransferType:   ManualTransferTypeDebit,
					Amount:         100,
					Currency:       "USD",
					CurrencyUnitID: currencyUnitID,
				})
				return err
			},
		},
		{
			name: "deposit intent",
			call: func(currencyUnitID int64) error {
				_, err := validateDepositIntent(DepositIntent{
					TenantID:        "tenant",
					IntentReference: "deposit-reference",
					ProviderCode:    "provider",
					WalletID:        uuid.New(),
					OwnerType:       OwnerTypeUser,
					OwnerID:         "42",
					Amount:          100,
					Currency:        "USD",
					CurrencyUnitID:  currencyUnitID,
				})
				return err
			},
		},
		{
			name: "psp method filter",
			call: func(currencyUnitID int64) error {
				_, err := store.ListAvailablePSPMethods(t.Context(), PSPMethodFilter{
					TenantID:       "tenant",
					Direction:      "deposit",
					Currency:       "USD",
					CurrencyUnitID: currencyUnitID,
					Limit:          10,
				})
				return err
			},
		},
	}

	unitCases := []struct {
		name string
		id   int64
		want error
	}{
		{name: "zero is missing", id: 0, want: ErrMissingCurrencyUnitID},
		{name: "negative is invalid", id: -1, want: ErrInvalidCurrencyUnitID},
	}

	for _, boundary := range boundaries {
		t.Run(boundary.name, func(t *testing.T) {
			for _, unitCase := range unitCases {
				t.Run(unitCase.name, func(t *testing.T) {
					if err := boundary.call(unitCase.id); !errors.Is(err, unitCase.want) {
						t.Fatalf("error = %v, want %v", err, unitCase.want)
					}
				})
			}
		})
	}
}

func validMoneyConversionQuoteUnitTestInput(at time.Time) MoneyConversionQuoteInput {
	expiresAt := at.Add(time.Hour)
	return MoneyConversionQuoteInput{
		TenantID:                       "tenant",
		RequestedByUserID:              42,
		IdempotencyKey:                 "quote-idempotency",
		MaxQuotesPerObservation:        100,
		ObservationID:                  1,
		ObservationBaseCurrencyUnitID:  1,
		ObservationQuoteCurrencyUnitID: 2,
		ObservationBaseCurrencyCode:    "USD",
		ObservationQuoteCurrencyCode:   "EUR",
		ObservationExpiresAt:           expiresAt,
		InputCurrencyUnitID:            1,
		OutputCurrencyUnitID:           2,
		InputCurrencyCode:              "USD",
		OutputCurrencyCode:             "EUR",
		InputMinorUnits:                100,
		OutputMinorUnits:               90,
		RoundingMode:                   "half_even",
		ConversionAt:                   at,
		ExpiresAt:                      expiresAt,
	}
}
