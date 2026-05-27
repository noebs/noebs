package consumer

import (
	"context"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

type transactionProjectionCommand struct {
	Transaction *ebs_fields.EBSResponse `json:"transaction"`
}

func (s *Service) requireTransactionProjectionTarget() error {
	if s == nil {
		return ErrMissingService
	}
	if s.Store == nil {
		return ErrMissingStore
	}
	if s.HTTPClient == nil {
		return ErrMissingHTTPClient
	}
	if _, err := s.serviceDiscoveryEndpoint(adminReportingCommandTarget); err != nil {
		return err
	}
	return nil
}

func (s *Service) StoreTransactionProjectionInAdminReporting(ctx context.Context, tenantID string, transaction ebs_fields.EBSResponse) error {
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	return s.doAdminServiceCommand(
		ctx,
		tenantID,
		adminReportingCommandTarget,
		"/internal/admin-reporting/transactions",
		transactionProjectionCommand{Transaction: &transaction},
		nil,
	)
}
