package merchant

import (
	"context"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/eventing"
	"github.com/adonese/noebs/store"
)

func (s *Service) recordTransaction(ctx context.Context, tenantID string, res ebs_fields.EBSResponse) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	event, err := eventing.NewTransactionRecordedStoreEvent(s.NoebsConfig.KafkaTransactionTopic, tenantID, res)
	if err != nil {
		return err
	}
	if err := s.Store.CreateTransactionWithEvent(ctx, tenantID, res, event); err != nil {
		return err
	}
	return nil
}
