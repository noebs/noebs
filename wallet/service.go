package wallet

import (
	"context"
	"errors"
	"fmt"

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

func NewService(db *basestore.DB, cfg ebs_fields.NoebsConfig) *Service {
	return &Service{Store: walletstore.New(db), Config: cfg}
}

func (s *Service) EnsureUserWallet(ctx context.Context, tenantID string, userID int64, currency string) (*walletstore.Wallet, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	ownerID := fmt.Sprintf("%d", userID)
	return s.Store.EnsureWallet(ctx, walletstore.EnsureWalletParams{
		TenantID:  tenantID,
		OwnerType: walletstore.OwnerTypeUser,
		OwnerID:   ownerID,
		UserID:    userID,
		Currency:  currency,
		KYCTier:   walletstore.KYCTierUnverified,
	})
}

func (s *Service) EnsureSystemWallets(ctx context.Context, tenantID, currency string) (map[string]*walletstore.Wallet, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	return s.Store.EnsureSystemWallets(ctx, tenantID, currency, walletstore.KYCTierUnverified)
}

func (s *Service) GetWallet(ctx context.Context, tenantID string, walletID uuid.UUID) (*walletstore.Wallet, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	return s.Store.GetWallet(ctx, tenantID, walletID)
}
