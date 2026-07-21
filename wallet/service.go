package wallet

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	basestore "github.com/adonese/noebs/store"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

var ErrMissingStore = errors.New("missing wallet store")

type Service struct {
	Store  *walletstore.Store
	Config ebs_fields.NoebsConfig
}

type EnsureUserWalletParams struct {
	TenantID       string
	UserID         int64
	Currency       string
	CurrencyUnitID int64
}

type EnsureSystemWalletsParams struct {
	TenantID       string
	Currency       string
	CurrencyUnitID int64
}

func NewService(db *basestore.DB, cfg ebs_fields.NoebsConfig) *Service {
	return &Service{Store: walletstore.New(db), Config: cfg}
}

func (s *Service) EnsureUserWallet(ctx context.Context, params EnsureUserWalletParams) (*walletstore.Wallet, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	if err := s.validateCurrentOperationalCurrencyUnit(ctx, params.Currency, params.CurrencyUnitID, time.Now().UTC()); err != nil {
		return nil, err
	}
	ownerID := fmt.Sprintf("%d", params.UserID)
	return s.Store.EnsureWallet(ctx, walletstore.EnsureWalletParams{
		TenantID:       params.TenantID,
		OwnerType:      walletstore.OwnerTypeUser,
		OwnerID:        ownerID,
		UserID:         params.UserID,
		Currency:       params.Currency,
		CurrencyUnitID: params.CurrencyUnitID,
		KYCTier:        walletstore.KYCTierUnverified,
	})
}

// GetUserWalletByCurrency supports idempotent boundary handling across unit
// transitions. Existing wallets retain the immutable unit that originally
// gave their balances meaning; only creation resolves and supplies a current
// unit version.
func (s *Service) GetUserWalletByCurrency(ctx context.Context, tenantID string, userID int64, currency string) (*walletstore.Wallet, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	if userID <= 0 {
		return nil, walletstore.ErrInvalidUserID
	}
	w, err := s.Store.GetWalletByOwner(ctx, tenantID, walletstore.OwnerTypeUser, fmt.Sprintf("%d", userID), currency)
	if err != nil {
		return nil, err
	}
	if w.UserID.Valid && w.UserID.Int64 != userID {
		return nil, walletstore.ErrWalletNotFound
	}
	return w, nil
}

func (s *Service) EnsureSystemWallets(ctx context.Context, params EnsureSystemWalletsParams) (map[string]*walletstore.Wallet, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	if err := s.validateCurrentOperationalCurrencyUnit(ctx, params.Currency, params.CurrencyUnitID, time.Now().UTC()); err != nil {
		return nil, err
	}
	return s.Store.EnsureSystemWallets(ctx, params.TenantID, params.Currency, params.CurrencyUnitID, walletstore.KYCTierUnverified)
}

func (s *Service) validateCurrentOperationalCurrencyUnit(ctx context.Context, currency string, currencyUnitID int64, at time.Time) error {
	if currencyUnitID == 0 {
		return walletstore.ErrMissingCurrencyUnitID
	}
	if currencyUnitID < 0 {
		return walletstore.ErrInvalidCurrencyUnitID
	}
	unit, err := s.Store.GetCurrencyUnit(ctx, currency, at)
	if err != nil {
		return err
	}
	if unit.ID != currencyUnitID {
		return walletstore.ErrInvalidUsageTime
	}
	if !unit.IsActive {
		return walletstore.ErrInactiveCurrency
	}
	if !unit.ISOMinorExponent.Valid {
		return walletstore.ErrCurrencyScaleUnavailable
	}
	return nil
}

func (s *Service) GetWallet(ctx context.Context, tenantID string, walletID uuid.UUID) (*walletstore.Wallet, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	return s.Store.GetWallet(ctx, tenantID, walletID)
}
