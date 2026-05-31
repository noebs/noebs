package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

type EnsureWalletParams struct {
	TenantID  string
	OwnerType string
	OwnerID   string
	UserID    int64
	Currency  string
	KYCTier   string
}

func (s *Store) EnsureWallet(ctx context.Context, params EnsureWalletParams) (*Wallet, error) {
	tenantID, err := ValidateTenantID(params.TenantID)
	if err != nil {
		return nil, err
	}
	if params.OwnerType == "" {
		return nil, ErrMissingOwnerType
	}
	if !OwnerTypeValid(params.OwnerType) {
		return nil, ErrInvalidOwnerType
	}
	if params.OwnerID == "" {
		return nil, ErrMissingOwnerID
	}
	if params.Currency == "" {
		return nil, ErrMissingCurrency
	}
	if params.KYCTier == "" {
		return nil, ErrMissingKYCTier
	}
	if params.OwnerType == OwnerTypeUser {
		if params.UserID <= 0 {
			return nil, ErrInvalidUserID
		}
	} else if params.UserID != 0 {
		return nil, ErrInvalidUserID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var uid sql.NullInt64
	if params.OwnerType == OwnerTypeUser {
		uid = sql.NullInt64{Int64: params.UserID, Valid: true}
	}
	now := time.Now().UTC()
	stmt := s.DB.Rebind(`INSERT INTO wallets(
		tenant_id, owner_type, owner_id, user_id, currency,
		kyc_tier, balance, available_balance, status, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, 0, 0, 'active', ?, ?)
	ON CONFLICT DO NOTHING`)
	if _, err := db.ExecContext(ctx, stmt, tenantID, params.OwnerType, params.OwnerID, uid, params.Currency, params.KYCTier, now, now); err != nil {
		return nil, err
	}
	params.TenantID = tenantID
	wallet, err := s.GetWalletByOwner(ctx, tenantID, params.OwnerType, params.OwnerID, params.Currency)
	if err != nil {
		if !errors.Is(err, ErrWalletNotFound) || params.OwnerType != OwnerTypeUser {
			return nil, err
		}
		wallet, err = s.getWalletByUser(ctx, tenantID, params.UserID, params.Currency)
		if err != nil {
			return nil, err
		}
	}
	if err := ValidateEnsureWalletReplay(wallet, params); err != nil {
		return nil, err
	}
	return wallet, nil
}

func (s *Store) EnsureSystemWallets(ctx context.Context, tenantID, currency, kycTier string) (map[string]*Wallet, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if currency == "" {
		return nil, ErrMissingCurrency
	}
	if kycTier == "" {
		return nil, ErrMissingKYCTier
	}
	systemWallets := make(map[string]*Wallet, len(SystemWalletCodes()))
	for _, code := range SystemWalletCodes() {
		w, err := s.EnsureWallet(ctx, EnsureWalletParams{
			TenantID:  tenantID,
			OwnerType: OwnerTypeSystem,
			OwnerID:   code,
			Currency:  currency,
			KYCTier:   kycTier,
		})
		if err != nil {
			return nil, err
		}
		systemWallets[code] = w
	}
	return systemWallets, nil
}

func (s *Store) GetWallet(ctx context.Context, tenantID string, walletID uuid.UUID) (*Wallet, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if walletID == uuid.Nil {
		return nil, ErrMissingWalletID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM wallets WHERE tenant_id = ? AND id = ?")
	var w Wallet
	if err := db.GetContext(ctx, &w, stmt, tenantID, walletID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrWalletNotFound
		}
		return nil, err
	}
	return &w, nil
}

func (s *Store) GetWalletByOwner(ctx context.Context, tenantID, ownerType, ownerID, currency string) (*Wallet, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if ownerType == "" {
		return nil, ErrMissingOwnerType
	}
	if !OwnerTypeValid(ownerType) {
		return nil, ErrInvalidOwnerType
	}
	if ownerID == "" {
		return nil, ErrMissingOwnerID
	}
	if currency == "" {
		return nil, ErrMissingCurrency
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM wallets WHERE tenant_id = ? AND owner_type = ? AND owner_id = ? AND currency = ?")
	var w Wallet
	if err := db.GetContext(ctx, &w, stmt, tenantID, ownerType, ownerID, currency); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrWalletNotFound
		}
		return nil, err
	}
	return &w, nil
}

func (s *Store) getWalletByUser(ctx context.Context, tenantID string, userID int64, currency string) (*Wallet, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind(`SELECT * FROM wallets
		WHERE tenant_id = ? AND owner_type = ? AND user_id = ? AND currency = ?`)
	var w Wallet
	if err := db.GetContext(ctx, &w, stmt, tenantID, OwnerTypeUser, userID, currency); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrWalletNotFound
		}
		return nil, err
	}
	return &w, nil
}

func ValidateEnsureWalletReplay(existing *Wallet, params EnsureWalletParams) error {
	if existing == nil ||
		existing.TenantID != params.TenantID ||
		existing.OwnerType != params.OwnerType ||
		existing.OwnerID != params.OwnerID ||
		existing.Currency != params.Currency ||
		existing.KYCTier != params.KYCTier {
		return ErrDuplicateWallet
	}
	if params.OwnerType == OwnerTypeUser {
		if !existing.UserID.Valid || existing.UserID.Int64 != params.UserID {
			return ErrDuplicateWallet
		}
		return nil
	}
	if existing.UserID.Valid || params.UserID != 0 {
		return ErrDuplicateWallet
	}
	return nil
}

func (s *Store) UpdateWalletPIN(ctx context.Context, tenantID string, walletID uuid.UUID, pinHash string, updatedAt time.Time) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if walletID == uuid.Nil {
		return ErrMissingWalletID
	}
	if pinHash == "" {
		return ErrMissingWalletPIN
	}
	if updatedAt.IsZero() {
		return ErrMissingUpdatedAt
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind(`UPDATE wallets SET wallet_pin_hash = ?, updated_at = ? WHERE tenant_id = ? AND id = ?`)
	result, err := db.ExecContext(ctx, stmt, pinHash, updatedAt, tenantID, walletID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrWalletNotFound
	}
	return nil
}
