package merchant

import (
	"context"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func (s *Service) recordTransaction(ctx context.Context, tenantID string, res ebs_fields.EBSResponse) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	if err := s.Store.CreateTransaction(ctx, tenantID, res); err != nil {
		return err
	}
	return s.StoreTransactionProjectionInAdminReporting(ctx, tenantID, res)
}
