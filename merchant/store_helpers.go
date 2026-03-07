package merchant

import (
	"context"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func (s *Service) recordTransaction(ctx context.Context, tenantID string, res ebs_fields.EBSResponse) error {
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	return s.Store.CreateTransaction(ctx, tenantID, res)
}
