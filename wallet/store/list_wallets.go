package store

import "context"

func (s *Store) ListWallets(ctx context.Context, tenantID string, limit, offset int) ([]Wallet, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if limit <= 0 {
		return nil, ErrInvalidLimit
	}
	if offset < 0 {
		return nil, ErrInvalidOffset
	}
	if _, err := s.ensureDB(); err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind(`SELECT * FROM wallets
		WHERE tenant_id = ?
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?`)
	var wallets []Wallet
	if err := s.DB.SelectContext(ctx, &wallets, stmt, tenantID, limit, offset); err != nil {
		return nil, err
	}
	return wallets, nil
}
