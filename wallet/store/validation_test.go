package store

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestEnsureWalletValidation(t *testing.T) {
	validUserID := int64(42)
	cases := []struct {
		name      string
		tenantID  string
		ownerType string
		ownerID   string
		currency  string
		userID    *int64
		wantErr   error
	}{
		{"missing-tenant", "", OwnerTypeUser, "user-1", "USD", &validUserID, ErrMissingTenantID},
		{"missing-owner-type", "tenant", "", "user-1", "USD", &validUserID, ErrMissingOwnerType},
		{"missing-owner-id", "tenant", OwnerTypeUser, "", "USD", &validUserID, ErrMissingOwnerID},
		{"missing-currency", "tenant", OwnerTypeUser, "user-1", "", &validUserID, ErrMissingCurrency},
		{"missing-user-id", "tenant", OwnerTypeUser, "user-1", "USD", nil, ErrInvalidUserID},
		{"invalid-user-id", "tenant", OwnerTypeUser, "user-1", "USD", ptrInt64(0), ErrInvalidUserID},
		{"user-id-on-system", "tenant", OwnerTypeSystem, "treasury", "USD", &validUserID, ErrInvalidUserID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Store{}
			_, err := s.EnsureWallet(t.Context(), tc.tenantID, tc.ownerType, tc.ownerID, tc.currency, tc.userID)
			assertErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestGetWalletValidation(t *testing.T) {
	s := &Store{}
	_, err := s.GetWallet(t.Context(), "", uuid.New())
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.GetWallet(t.Context(), "tenant", uuid.Nil)
	assertErrorIs(t, err, ErrMissingWalletID)
}

func TestGetWalletByOwnerValidation(t *testing.T) {
	s := &Store{}
	_, err := s.GetWalletByOwner(t.Context(), "", OwnerTypeUser, "user-1", "USD")
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.GetWalletByOwner(t.Context(), "tenant", "", "user-1", "USD")
	assertErrorIs(t, err, ErrMissingOwnerType)

	_, err = s.GetWalletByOwner(t.Context(), "tenant", OwnerTypeUser, "", "USD")
	assertErrorIs(t, err, ErrMissingOwnerID)

	_, err = s.GetWalletByOwner(t.Context(), "tenant", OwnerTypeUser, "user-1", "")
	assertErrorIs(t, err, ErrMissingCurrency)
}

func TestPostDoubleEntryValidation(t *testing.T) {
	s := &Store{}
	params := DoubleEntryParams{
		TenantID:       "tenant",
		IdempotencyKey: "idempo-1",
		Currency:       "USD",
		ReferenceType:  "p2p",
		DebitWalletID:  uuid.New(),
		CreditWalletID: uuid.New(),
		Amount:         100,
	}

	cases := []struct {
		name    string
		mutate  func(p *DoubleEntryParams)
		wantErr error
	}{
		{"missing-tenant", func(p *DoubleEntryParams) { p.TenantID = "" }, ErrMissingTenantID},
		{"missing-currency", func(p *DoubleEntryParams) { p.Currency = "" }, ErrMissingCurrency},
		{"missing-idempotency", func(p *DoubleEntryParams) { p.IdempotencyKey = "" }, ErrMissingIdempotencyKey},
		{"missing-reference-type", func(p *DoubleEntryParams) { p.ReferenceType = "" }, ErrMissingReferenceType},
		{"missing-wallet", func(p *DoubleEntryParams) { p.DebitWalletID = uuid.Nil }, ErrMissingWalletID},
		{"same-wallet", func(p *DoubleEntryParams) { p.CreditWalletID = p.DebitWalletID }, ErrInvalidWalletPair},
		{"invalid-amount", func(p *DoubleEntryParams) { p.Amount = 0 }, ErrInvalidAmount},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := params
			tc.mutate(&p)
			_, err := s.PostDoubleEntry(t.Context(), p)
			assertErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestHoldValidation(t *testing.T) {
	s := &Store{}
	params := HoldParams{
		TenantID:       "tenant",
		WalletID:       uuid.New(),
		Amount:         100,
		Reason:         "withdrawal",
		ReferenceType:  "withdrawal",
		ReferenceID:    "ref-1",
		IdempotencyKey: "idem-1",
		ExpiresAt:      time.Now().UTC().Add(time.Hour),
	}

	cases := []struct {
		name    string
		mutate  func(p *HoldParams)
		wantErr error
	}{
		{"missing-tenant", func(p *HoldParams) { p.TenantID = "" }, ErrMissingTenantID},
		{"missing-wallet", func(p *HoldParams) { p.WalletID = uuid.Nil }, ErrMissingWalletID},
		{"missing-reference-type", func(p *HoldParams) { p.ReferenceType = "" }, ErrMissingReferenceType},
		{"missing-reference-id", func(p *HoldParams) { p.ReferenceID = "" }, ErrMissingReferenceID},
		{"missing-idempotency", func(p *HoldParams) { p.IdempotencyKey = "" }, ErrMissingIdempotencyKey},
		{"missing-reason", func(p *HoldParams) { p.Reason = "" }, ErrMissingHoldReason},
		{"missing-expiry", func(p *HoldParams) { p.ExpiresAt = time.Time{} }, ErrMissingHoldExpiry},
		{"invalid-amount", func(p *HoldParams) { p.Amount = 0 }, ErrInvalidAmount},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := params
			tc.mutate(&p)
			_, err := s.CreateHold(t.Context(), p)
			assertErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestReleaseHoldValidation(t *testing.T) {
	s := &Store{}
	err := s.ReleaseHold(t.Context(), "", 1)
	assertErrorIs(t, err, ErrMissingTenantID)

	err = s.ReleaseHold(t.Context(), "tenant", 0)
	assertErrorIs(t, err, ErrInvalidHoldID)
}

func TestCreatePSPTransactionValidation(t *testing.T) {
	s := &Store{}
	base := PSPTransaction{
		TenantID:        "tenant",
		PSPProvider:     "coinsbuy",
		IdempotencyKey:  "idem-1",
		ClientReference: "ref-1",
		Direction:       "inbound",
		Amount:          100,
		Currency:        "USD",
		Status:          "initiated",
	}

	cases := []struct {
		name    string
		mutate  func(txn *PSPTransaction)
		wantErr error
	}{
		{"missing-tenant", func(txn *PSPTransaction) { txn.TenantID = "" }, ErrMissingTenantID},
		{"missing-provider", func(txn *PSPTransaction) { txn.PSPProvider = "" }, ErrMissingProviderCode},
		{"missing-idempotency", func(txn *PSPTransaction) { txn.IdempotencyKey = "" }, ErrMissingIdempotencyKey},
		{"missing-reference", func(txn *PSPTransaction) { txn.ClientReference = "" }, ErrMissingClientReference},
		{"missing-direction", func(txn *PSPTransaction) { txn.Direction = "" }, ErrMissingDirection},
		{"invalid-amount", func(txn *PSPTransaction) { txn.Amount = 0 }, ErrInvalidAmount},
		{"missing-currency", func(txn *PSPTransaction) { txn.Currency = "" }, ErrMissingCurrency},
		{"missing-status", func(txn *PSPTransaction) { txn.Status = "" }, ErrMissingStatus},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			txn := base
			tc.mutate(&txn)
			_, err := s.CreatePSPTransaction(t.Context(), txn)
			assertErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestGetPSPTransactionByReferenceValidation(t *testing.T) {
	s := &Store{}
	_, err := s.GetPSPTransactionByReference(t.Context(), "", "ref-1")
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.GetPSPTransactionByReference(t.Context(), "tenant", "")
	assertErrorIs(t, err, ErrMissingClientReference)
}

func TestUpdatePSPTransactionStatusValidation(t *testing.T) {
	s := &Store{}
	update := PSPStatusUpdate{Status: "success"}

	err := s.UpdatePSPTransactionStatus(t.Context(), "", "ref-1", update)
	assertErrorIs(t, err, ErrMissingTenantID)

	err = s.UpdatePSPTransactionStatus(t.Context(), "tenant", "", update)
	assertErrorIs(t, err, ErrMissingClientReference)

	update.Status = ""
	err = s.UpdatePSPTransactionStatus(t.Context(), "tenant", "ref-1", update)
	assertErrorIs(t, err, ErrMissingStatus)
}

func TestListPSPTransactionsForPollingValidation(t *testing.T) {
	s := &Store{}
	_, err := s.ListPSPTransactionsForPolling(t.Context(), "", 1)
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.ListPSPTransactionsForPolling(t.Context(), "tenant", 0)
	assertErrorIs(t, err, ErrInvalidLimit)
}

func TestAddPSPTransactionAmountValidation(t *testing.T) {
	s := &Store{}
	base := PSPTransactionAmount{
		TenantID:         "tenant",
		PSPTransactionID: 1,
		AmountKind:       PSPAmountReported,
		Amount:           100,
		Currency:         "USD",
	}

	cases := []struct {
		name    string
		mutate  func(a *PSPTransactionAmount)
		wantErr error
	}{
		{"missing-tenant", func(a *PSPTransactionAmount) { a.TenantID = "" }, ErrMissingTenantID},
		{"missing-psp-tx", func(a *PSPTransactionAmount) { a.PSPTransactionID = 0 }, ErrMissingPSPTransactionID},
		{"missing-kind", func(a *PSPTransactionAmount) { a.AmountKind = "" }, ErrMissingAmountKind},
		{"invalid-kind", func(a *PSPTransactionAmount) { a.AmountKind = PSPAmountKind("bogus") }, ErrInvalidAmountKind},
		{"invalid-amount", func(a *PSPTransactionAmount) { a.Amount = 0 }, ErrInvalidAmount},
		{"missing-currency", func(a *PSPTransactionAmount) { a.Currency = "" }, ErrMissingCurrency},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			amount := base
			tc.mutate(&amount)
			_, err := s.AddPSPTransactionAmount(t.Context(), amount)
			assertErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestAddPSPTransactionAmountFXValidation(t *testing.T) {
	s := &Store{}
	amount := PSPTransactionAmount{
		TenantID:         "tenant",
		PSPTransactionID: 1,
		AmountKind:       PSPAmountReported,
		Amount:           100,
		Currency:         "USD",
	}

	amount.FxRate = decimal.NullDecimal{Valid: true}
	_, err := s.AddPSPTransactionAmount(t.Context(), amount)
	assertErrorIs(t, err, ErrMissingFXCurrency)

	amount = PSPTransactionAmount{
		TenantID:         "tenant",
		PSPTransactionID: 1,
		AmountKind:       PSPAmountReported,
		Amount:           100,
		Currency:         "USD",
		FxBaseCurrency:   sql.NullString{String: "USD", Valid: true},
		FxQuoteCurrency:  sql.NullString{String: "EUR", Valid: true},
	}
	_, err = s.AddPSPTransactionAmount(t.Context(), amount)
	assertErrorIs(t, err, ErrMissingFXRate)
}

func TestAddPSPTransactionAmountsValidation(t *testing.T) {
	s := &Store{}
	inputs := []PSPTransactionAmountInput{
		{
			AmountKind: PSPAmountReported,
			Amount:     100,
			Currency:   "USD",
		},
	}

	_, err := s.AddPSPTransactionAmounts(t.Context(), "", 1, inputs)
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.AddPSPTransactionAmounts(t.Context(), "tenant", 0, inputs)
	assertErrorIs(t, err, ErrMissingPSPTransactionID)

	_, err = s.AddPSPTransactionAmounts(t.Context(), "tenant", 1, nil)
	assertErrorIs(t, err, ErrMissingAmounts)

	bad := []PSPTransactionAmountInput{
		{
			AmountKind: PSPAmountKind("bogus"),
			Amount:     100,
			Currency:   "USD",
		},
	}
	_, err = s.AddPSPTransactionAmounts(t.Context(), "tenant", 1, bad)
	assertErrorIs(t, err, ErrInvalidAmountKind)
}

func TestListPSPTransactionAmountsValidation(t *testing.T) {
	s := &Store{}
	_, err := s.ListPSPTransactionAmounts(t.Context(), "", 1)
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.ListPSPTransactionAmounts(t.Context(), "tenant", 0)
	assertErrorIs(t, err, ErrMissingPSPTransactionID)
}

func TestListPSPTransactionAmountsByKindValidation(t *testing.T) {
	s := &Store{}
	_, err := s.ListPSPTransactionAmountsByKind(t.Context(), "", 1, PSPAmountReported)
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.ListPSPTransactionAmountsByKind(t.Context(), "tenant", 0, PSPAmountReported)
	assertErrorIs(t, err, ErrMissingPSPTransactionID)

	_, err = s.ListPSPTransactionAmountsByKind(t.Context(), "tenant", 1, "")
	assertErrorIs(t, err, ErrMissingAmountKind)

	_, err = s.ListPSPTransactionAmountsByKind(t.Context(), "tenant", 1, PSPAmountKind("bogus"))
	assertErrorIs(t, err, ErrInvalidAmountKind)
}

func assertErrorIs(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
}

func ptrInt64(v int64) *int64 {
	return &v
}
