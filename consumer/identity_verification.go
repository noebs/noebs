package consumer

import (
	"context"

	"github.com/adonese/noebs/store"
)

func (s *Service) GetIdentityVerification(ctx context.Context, owner store.IdentityOwner) (store.IdentityVerification, error) {
	return s.Store.GetIdentityVerification(ctx, owner)
}
