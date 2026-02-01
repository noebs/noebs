package activity

import (
	"context"

	basestore "github.com/adonese/noebs/store"
	walletsecurity "github.com/adonese/noebs/wallet/security"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

type SecurityActivities struct {
	Store     *walletstore.Store
	UserStore *basestore.Store
}

func NewSecurityActivities(store *walletstore.Store, userStore *basestore.Store) *SecurityActivities {
	return &SecurityActivities{Store: store, UserStore: userStore}
}

func (a *SecurityActivities) VerifyWalletPIN(ctx context.Context, tenantID string, walletID uuid.UUID, pin string) (bool, error) {
	if a == nil || a.Store == nil {
		return false, ErrMissingStore
	}
	if pin == "" {
		return false, walletstore.ErrMissingWalletPIN
	}
	walletRow, err := a.Store.GetWallet(ctx, tenantID, walletID)
	if err != nil {
		return false, err
	}
	hash := ""
	if walletRow.WalletPinHash.Valid {
		hash = walletRow.WalletPinHash.String
	}
	return walletsecurity.VerifyPIN(hash, pin), nil
}

func (a *SecurityActivities) VerifyUserTOTP(ctx context.Context, tenantID string, userID int64, code string) (bool, error) {
	if a == nil || a.UserStore == nil {
		return false, ErrMissingStore
	}
	if tenantID == "" {
		return false, walletstore.ErrMissingTenantID
	}
	if userID <= 0 {
		return false, walletstore.ErrInvalidUserID
	}
	if code == "" {
		return false, walletstore.ErrMissingTwoFACode
	}
	user, err := a.UserStore.FindUserByID(ctx, tenantID, userID)
	if err != nil {
		return false, err
	}
	return user.VerifyOtp(code), nil
}
