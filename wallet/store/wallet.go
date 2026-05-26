package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

func (s *Store) EnsureWallet(ctx context.Context, tenantID, ownerType, ownerID, currency string, userID *int64) (*Wallet, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if ownerType == "" {
		return nil, ErrMissingOwnerType
	}
	if ownerID == "" {
		return nil, ErrMissingOwnerID
	}
	if currency == "" {
		return nil, ErrMissingCurrency
	}
	if ownerType == OwnerTypeUser {
		if userID == nil || *userID <= 0 {
			return nil, ErrInvalidUserID
		}
	} else if userID != nil {
		return nil, ErrInvalidUserID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var uid sql.NullInt64
	if userID != nil {
		uid = sql.NullInt64{Int64: *userID, Valid: true}
	}
	now := time.Now().UTC()
	stmt := s.DB.Rebind(`INSERT INTO wallets(
		tenant_id, owner_type, owner_id, user_id, currency,
		balance, available_balance, status, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, 0, 0, 'active', ?, ?)
	ON CONFLICT(tenant_id, owner_type, owner_id, currency) DO NOTHING`)
	if _, err := db.ExecContext(ctx, stmt, tenantID, ownerType, ownerID, uid, currency, now, now); err != nil {
		return nil, err
	}
	return s.GetWalletByOwner(ctx, tenantID, ownerType, ownerID, currency)
}

func (s *Store) EnsureSystemWallets(ctx context.Context, tenantID, currency string) (map[string]*Wallet, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if currency == "" {
		return nil, ErrMissingCurrency
	}
	systemWallets := make(map[string]*Wallet, len(SystemWalletCodes()))
	for _, code := range SystemWalletCodes() {
		w, err := s.EnsureWallet(ctx, tenantID, OwnerTypeSystem, code, currency, nil)
		if err != nil {
			return nil, err
		}
		systemWallets[code] = w
	}
	return systemWallets, nil
}

func (s *Store) GetWallet(ctx context.Context, tenantID string, walletID uuid.UUID) (*Wallet, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
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
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if ownerType == "" {
		return nil, ErrMissingOwnerType
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

func (s *Store) UpdateWalletPIN(ctx context.Context, tenantID string, walletID uuid.UUID, pinHash string, updatedAt time.Time) error {
	if tenantID == "" {
		return ErrMissingTenantID
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
