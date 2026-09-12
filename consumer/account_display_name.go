package consumer

import (
	"context"
	"errors"

	"github.com/adonese/noebs/store"
)

var ErrAccountDisplayNameActor = errors.New("account_display_name_actor_mismatch")

type AccountDisplayNameRequest struct {
	UserID int64 `json:"user_id"`
}

type AccountDisplayNameResult struct {
	DisplayName string `json:"display_name"`
}

// ResolveAccountDisplayName accepts the original authenticated actor, not an
// impersonated recipient principal. TargetUserID is a ledger-validated owner in
// the same tenant, supplied only by the signed gateway integration.
func (s *Service) ResolveAccountDisplayName(ctx context.Context, tenantID string, actor PrincipalProjectionReference, actorUserID, targetUserID int64) (AccountDisplayNameResult, error) {
	if s == nil || s.Store == nil {
		return AccountDisplayNameResult{}, ErrMissingStore
	}
	if actorUserID <= 0 || targetUserID <= 0 {
		return AccountDisplayNameResult{}, store.ErrInvalidUserID
	}
	profile, err := s.ResolveProfileProjection(ctx, tenantID, actor)
	if err != nil {
		return AccountDisplayNameResult{}, err
	}
	if profile.UserID != actorUserID {
		return AccountDisplayNameResult{}, ErrAccountDisplayNameActor
	}
	name, err := s.Store.AccountDisplayName(ctx, tenantID, targetUserID)
	if err != nil {
		return AccountDisplayNameResult{}, err
	}
	return AccountDisplayNameResult{DisplayName: name}, nil
}
