package consumer

import (
	"context"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func (s *Service) GetTransactionsForUserID(ctx context.Context, tenantID string, userID int64) ([]ebs_fields.EBSResponse, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, store.ErrInvalidUserID
	}

	return s.Store.GetTransactionsByParticipantUserID(ctx, tenantID, userID)
}
