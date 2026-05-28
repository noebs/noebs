package store

import "context"

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
