package store

import (
	"context"
	"database/sql"
	"time"

	basestore "github.com/adonese/noebs/store"
	"github.com/adonese/noebs/wallet"
	"github.com/google/uuid"
)

func (s *Store) EnsureWallet(ctx context.Context, tenantID, ownerType, ownerID, currency string, userID *int64) (*wallet.Wallet, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	if tenantID == "" {
		tenantID = basestore.DefaultTenantID
	}
	if currency == "" {
		currency = "USD"
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

func (s *Store) EnsureSystemWallets(ctx context.Context, tenantID, currency string) (map[string]*wallet.Wallet, error) {
	systemWallets := make(map[string]*wallet.Wallet, len(wallet.SystemWalletCodes()))
	for _, code := range wallet.SystemWalletCodes() {
		w, err := s.EnsureWallet(ctx, tenantID, wallet.OwnerTypeSystem, code, currency, nil)
		if err != nil {
			return nil, err
		}
		systemWallets[code] = w
	}
	return systemWallets, nil
}

func (s *Store) GetWallet(ctx context.Context, tenantID string, walletID uuid.UUID) (*wallet.Wallet, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	if tenantID == "" {
		tenantID = basestore.DefaultTenantID
	}
	stmt := s.DB.Rebind("SELECT * FROM wallets WHERE tenant_id = ? AND id = ?")
	var w wallet.Wallet
	if err := db.GetContext(ctx, &w, stmt, tenantID, walletID); err != nil {
		return nil, err
	}
	return &w, nil
}

func (s *Store) GetWalletByOwner(ctx context.Context, tenantID, ownerType, ownerID, currency string) (*wallet.Wallet, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	if tenantID == "" {
		tenantID = basestore.DefaultTenantID
	}
	if currency == "" {
		currency = "USD"
	}
	stmt := s.DB.Rebind("SELECT * FROM wallets WHERE tenant_id = ? AND owner_type = ? AND owner_id = ? AND currency = ?")
	var w wallet.Wallet
	if err := db.GetContext(ctx, &w, stmt, tenantID, ownerType, ownerID, currency); err != nil {
		return nil, err
	}
	return &w, nil
}
