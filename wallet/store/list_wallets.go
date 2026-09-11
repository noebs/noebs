package store

import (
	"context"
	"strconv"
)

// ListUserWallets restores the customer's accounts without creating a wallet or
// selecting a currency on their behalf. Both owner fields must identify them.
func (s *Store) ListUserWallets(ctx context.Context, tenantID string, userID int64) ([]Wallet, error) {
	if _, err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, ErrInvalidUserID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	wallets := make([]Wallet, 0)
	err = db.SelectContext(ctx, &wallets, db.Rebind(`SELECT * FROM wallets
		WHERE tenant_id = ? AND owner_type = ? AND owner_id = ?
		AND (user_id IS NULL OR user_id = ?) ORDER BY currency ASC, id ASC`),
		tenantID, OwnerTypeUser, strconv.FormatInt(userID, 10), userID)
	return wallets, err
}

func (s *Store) ListWallets(ctx context.Context, tenantID string, limit, offset int) ([]Wallet, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, ErrInvalidLimit
	}
	if offset < 0 {
		return nil, ErrInvalidOffset
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`SELECT * FROM wallets
		WHERE tenant_id = ?
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?`)
	var wallets []Wallet
	if err := db.SelectContext(ctx, &wallets, stmt, tenantID, limit, offset); err != nil {
		return nil, err
	}
	return wallets, nil
}
