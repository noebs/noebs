package store

import (
	"errors"
	"testing"
	"time"

	"github.com/adonese/noebs/wallet"
	"github.com/google/uuid"
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
		{"missing-tenant", "", wallet.OwnerTypeUser, "user-1", "USD", &validUserID, ErrMissingTenantID},
		{"missing-owner-type", "tenant", "", "user-1", "USD", &validUserID, ErrMissingOwnerType},
		{"missing-owner-id", "tenant", wallet.OwnerTypeUser, "", "USD", &validUserID, ErrMissingOwnerID},
		{"missing-currency", "tenant", wallet.OwnerTypeUser, "user-1", "", &validUserID, ErrMissingCurrency},
		{"missing-user-id", "tenant", wallet.OwnerTypeUser, "user-1", "USD", nil, ErrInvalidUserID},
		{"invalid-user-id", "tenant", wallet.OwnerTypeUser, "user-1", "USD", ptrInt64(0), ErrInvalidUserID},
		{"user-id-on-system", "tenant", wallet.OwnerTypeSystem, "treasury", "USD", &validUserID, ErrInvalidUserID},
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
	_, err := s.GetWalletByOwner(t.Context(), "", wallet.OwnerTypeUser, "user-1", "USD")
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.GetWalletByOwner(t.Context(), "tenant", "", "user-1", "USD")
	assertErrorIs(t, err, ErrMissingOwnerType)

	_, err = s.GetWalletByOwner(t.Context(), "tenant", wallet.OwnerTypeUser, "", "USD")
	assertErrorIs(t, err, ErrMissingOwnerID)

	_, err = s.GetWalletByOwner(t.Context(), "tenant", wallet.OwnerTypeUser, "user-1", "")
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

func assertErrorIs(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
}

func ptrInt64(v int64) *int64 {
	return &v
}
