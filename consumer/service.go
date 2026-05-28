package consumer

import (
	"context"
	"errors"
	"net/http"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/eventing"
	"github.com/adonese/noebs/store"
	"github.com/sirupsen/logrus"
)

// we use a simple string to store the ipin key and reuse it across noebs.
var ebsIpinEncryptionKey string

var ErrMissingStore = errors.New("missing consumer store")

// Service consumer for utils.Service struct.
type Service struct {
	Store       *store.Store
	NoebsConfig ebs_fields.NoebsConfig
	Logger      *logrus.Logger
	Auth        Auther
	HTTPClient  *http.Client
}

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
