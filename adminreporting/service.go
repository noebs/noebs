package adminreporting

import (
	"context"
	"errors"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

var (
	ErrMissingStore                 = errors.New("missing_admin_reporting_store")
	ErrMissingTransactionProjection = errors.New("missing_transaction_projection")
)

type Service struct {
	Store *store.Store
}

type TransactionProjectionCommand struct {
	Transaction *ebs_fields.EBSResponse `json:"transaction"`
}

func (s *Service) StoreTransactionProjection(ctx context.Context, tenantID string, cmd TransactionProjectionCommand) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if cmd.Transaction == nil {
		return ErrMissingTransactionProjection
	}
	return s.Store.CreateTransaction(ctx, tenantID, *cmd.Transaction)
}
