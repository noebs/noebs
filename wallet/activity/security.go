package activity

import (
	"context"
	"time"

	walletsecurity "github.com/adonese/noebs/wallet/security"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

type SecurityActivities struct {
	Store *walletstore.Store
}

func NewSecurityActivities(store *walletstore.Store) *SecurityActivities {
	return &SecurityActivities{Store: store}
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
	if a == nil || a.Store == nil {
		return false, ErrMissingStore
	}
	tenantID, err := walletstore.ValidateTenantID(tenantID)
	if err != nil {
		return false, err
	}
	if userID <= 0 {
		return false, walletstore.ErrInvalidUserID
	}
	if code == "" {
		return false, walletstore.ErrMissingTwoFACode
	}
	record, err := a.Store.GetUserTwoFA(ctx, tenantID, userID)
	if err != nil {
		return false, err
	}
	if !record.Enabled {
		return false, walletstore.ErrMissingTwoFACode
	}
	ok := walletsecurity.VerifyTOTP(record.Secret, code)
	if ok {
		if err := a.Store.TouchUserTwoFALastUsed(ctx, tenantID, userID, time.Now().UTC()); err != nil {
			return false, err
		}
	}
	return ok, nil
}
