package store

import "context"

// AccountDisplayName returns only mutable account display metadata. The caller
// must already have resolved the recipient's canonical owner through the ledger;
// this method neither discovers wallets nor asserts legal identity verification.
func (s *Store) AccountDisplayName(ctx context.Context, tenantID string, userID int64) (string, error) {
	if _, err := ValidateTenantID(tenantID); err != nil {
		return "", err
	}
	if userID <= 0 {
		return "", ErrInvalidUserID
	}
	db, err := s.ensureDB()
	if err != nil {
		return "", err
	}
	var name string
	err = db.GetContext(ctx, &name, db.Rebind(`SELECT fullname FROM users WHERE tenant_id=? AND id=?`), tenantID, userID)
	return name, err
}
