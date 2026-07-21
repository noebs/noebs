package store

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestDepositIntentRequiresAndReplaysExactCurrencyUnit(t *testing.T) {
	walletID := uuid.New()
	requested := DepositIntent{
		TenantID:        "tenant",
		IntentReference: "deposit-1",
		ProviderCode:    "provider",
		WalletID:        walletID,
		OwnerType:       OwnerTypeUser,
		OwnerID:         "42",
		Amount:          100,
		Currency:        "USD",
		CurrencyUnitID:  11,
		IdempotencyKey:  "deposit-key",
		WorkflowID:      "deposit-workflow",
		Metadata:        RawJSON(`{}`),
		RawRequest:      RawJSON(`{}`),
	}

	missing := requested
	missing.CurrencyUnitID = 0
	if _, err := validateDepositIntent(missing); !errors.Is(err, ErrMissingCurrencyUnitID) {
		t.Fatalf("validateDepositIntent() error = %v, want %v", err, ErrMissingCurrencyUnitID)
	}

	existing := requested
	replayed := requested
	replayed.CurrencyUnitID++
	if err := ValidateDepositIntentReplay(&existing, replayed); !errors.Is(err, ErrDuplicateDepositIntent) {
		t.Fatalf("ValidateDepositIntentReplay() error = %v, want %v", err, ErrDuplicateDepositIntent)
	}

	wallet := &Wallet{
		ID:             walletID,
		TenantID:       requested.TenantID,
		OwnerType:      requested.OwnerType,
		OwnerID:        requested.OwnerID,
		Currency:       requested.Currency,
		CurrencyUnitID: requested.CurrencyUnitID,
	}
	if err := ValidateDepositIntentWallet(wallet, requested); err != nil {
		t.Fatalf("ValidateDepositIntentWallet() error = %v", err)
	}
	wallet.CurrencyUnitID++
	if err := ValidateDepositIntentWallet(wallet, requested); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("ValidateDepositIntentWallet() mismatch = %v, want %v", err, ErrCurrencyMismatch)
	}
}

func TestDepositIntentTransactionRequiresExactCurrencyUnit(t *testing.T) {
	intent := &DepositIntent{ID: 7, TenantID: "tenant", ProviderCode: "provider", IntentReference: "deposit-1", Amount: 100, Currency: "USD", CurrencyUnitID: 11, IdempotencyKey: "key", WorkflowID: "workflow"}
	transaction := &PSPTransaction{TenantID: intent.TenantID, PSPProvider: intent.ProviderCode, ClientReference: intent.IntentReference, Direction: "inbound", Amount: intent.Amount, Currency: intent.Currency, CurrencyUnitID: intent.CurrencyUnitID, IdempotencyKey: intent.IdempotencyKey, WorkflowID: nullString(intent.WorkflowID)}
	transaction.DepositIntentID = nullNonZeroInt64(intent.ID)
	if err := ValidateDepositIntentTransaction(intent, transaction); err != nil {
		t.Fatalf("ValidateDepositIntentTransaction() error = %v", err)
	}
	transaction.CurrencyUnitID++
	if err := ValidateDepositIntentTransaction(intent, transaction); !errors.Is(err, ErrInvalidDepositIntent) {
		t.Fatalf("ValidateDepositIntentTransaction() mismatch = %v, want %v", err, ErrInvalidDepositIntent)
	}
}
