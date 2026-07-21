package validation

import (
	"context"
	"errors"
	"testing"
	"time"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/shopspring/decimal"
)

type sameCurrencyConversion func(*Service, int64, int64) (int64, error)

func TestSameCurrencyShortcutsValidateBothUnitIdentities(t *testing.T) {
	operations := map[string]sameCurrencyConversion{
		"psp amount": func(service *Service, fromUnitID, toUnitID int64) (int64, error) {
			amount, _, _, err := service.convertAmountAt(
				t.Context(), "tenant", 125, "USD", fromUnitID, "USD", toUnitID,
				decimal.NullDecimal{}, "", "", time.Now().UTC(),
			)
			return amount, err
		},
		"withdrawal": func(service *Service, fromUnitID, toUnitID int64) (int64, error) {
			amount, _, _, err := service.convertWithdrawalAmountAt(
				t.Context(), "tenant", 125, "USD", fromUnitID, "USD", toUnitID, time.Now().UTC(),
			)
			return amount, err
		},
	}

	for name, convert := range operations {
		t.Run(name+" valid", func(t *testing.T) {
			calls := 0
			service := &Service{CurrencyUnitLookup: func(_ context.Context, currencyUnitID int64) (*walletstore.CurrencyUnitVersion, error) {
				calls++
				return &walletstore.CurrencyUnitVersion{ID: currencyUnitID, CurrencyCode: "USD"}, nil
			}}
			amount, err := convert(service, 11, 11)
			if err != nil || amount != 125 {
				t.Fatalf("same-currency conversion = %d, %v; want 125, nil", amount, err)
			}
			if calls != 2 {
				t.Fatalf("currency-unit lookups = %d, want 2", calls)
			}
		})

		t.Run(name+" forged returned id", func(t *testing.T) {
			service := &Service{CurrencyUnitLookup: func(_ context.Context, currencyUnitID int64) (*walletstore.CurrencyUnitVersion, error) {
				return &walletstore.CurrencyUnitVersion{ID: currencyUnitID + 1, CurrencyCode: "USD"}, nil
			}}
			_, err := convert(service, 11, 11)
			if !errors.Is(err, walletstore.ErrCurrencyMismatch) {
				t.Fatalf("forged unit id error = %v, want %v", err, walletstore.ErrCurrencyMismatch)
			}
		})

		t.Run(name+" forged returned currency", func(t *testing.T) {
			service := &Service{CurrencyUnitLookup: func(_ context.Context, currencyUnitID int64) (*walletstore.CurrencyUnitVersion, error) {
				return &walletstore.CurrencyUnitVersion{ID: currencyUnitID, CurrencyCode: "AED"}, nil
			}}
			_, err := convert(service, 11, 11)
			if !errors.Is(err, walletstore.ErrCurrencyMismatch) {
				t.Fatalf("forged unit currency error = %v, want %v", err, walletstore.ErrCurrencyMismatch)
			}
		})

		t.Run(name+" validates second returned identity", func(t *testing.T) {
			calls := 0
			service := &Service{CurrencyUnitLookup: func(_ context.Context, currencyUnitID int64) (*walletstore.CurrencyUnitVersion, error) {
				calls++
				if calls == 2 {
					return &walletstore.CurrencyUnitVersion{ID: currencyUnitID + 1, CurrencyCode: "USD"}, nil
				}
				return &walletstore.CurrencyUnitVersion{ID: currencyUnitID, CurrencyCode: "USD"}, nil
			}}
			_, err := convert(service, 11, 11)
			if !errors.Is(err, walletstore.ErrCurrencyMismatch) {
				t.Fatalf("forged second unit error = %v, want %v", err, walletstore.ErrCurrencyMismatch)
			}
			if calls != 2 {
				t.Fatalf("currency-unit lookups = %d, want 2", calls)
			}
		})

		t.Run(name+" mismatched supplied ids", func(t *testing.T) {
			calls := 0
			service := &Service{CurrencyUnitLookup: func(_ context.Context, currencyUnitID int64) (*walletstore.CurrencyUnitVersion, error) {
				calls++
				return &walletstore.CurrencyUnitVersion{ID: currencyUnitID, CurrencyCode: "USD"}, nil
			}}
			_, err := convert(service, 11, 12)
			if !errors.Is(err, walletstore.ErrCurrencyMismatch) {
				t.Fatalf("mismatched supplied ids error = %v, want %v", err, walletstore.ErrCurrencyMismatch)
			}
			if calls != 2 {
				t.Fatalf("currency-unit lookups = %d, want 2", calls)
			}
		})
	}
}
