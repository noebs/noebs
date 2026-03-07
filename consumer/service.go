package consumer

import (
	"context"
	"errors"

	"github.com/adonese/noebs/ebs_fields"
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
}

var fees = ebs_fields.NewDynamicFeesWithDefaults()

func (s *Service) recordTransaction(ctx context.Context, tenantID string, res ebs_fields.EBSResponse) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	return s.Store.CreateTransaction(ctx, tenantID, res)
}
