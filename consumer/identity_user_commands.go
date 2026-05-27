package consumer

import (
	"context"
	"strings"

	"github.com/adonese/noebs/store"
)

type IdentityUserByMobileCommand struct {
	Mobile string `json:"mobile"`
}

type IdentityUserByMobileResult struct {
	UserID int64  `json:"user_id"`
	Mobile string `json:"mobile"`
}

func (s *Service) ResolveIdentityUserByMobile(ctx context.Context, tenantID string, cmd IdentityUserByMobileCommand) (IdentityUserByMobileResult, error) {
	if s == nil || s.Store == nil {
		return IdentityUserByMobileResult{}, ErrMissingStore
	}
	if tenantID == "" {
		return IdentityUserByMobileResult{}, store.ErrMissingTenantID
	}
	mobile := strings.TrimSpace(cmd.Mobile)
	if mobile == "" {
		return IdentityUserByMobileResult{}, ErrMissingMobile
	}
	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return IdentityUserByMobileResult{}, err
	}
	return IdentityUserByMobileResult{UserID: user.ID, Mobile: user.Mobile}, nil
}

func (s *Service) ResolveIdentityUserByMobileInIdentityAuth(ctx context.Context, tenantID, mobile string) (IdentityUserByMobileResult, error) {
	var result IdentityUserByMobileResult
	if err := s.doAdminServiceCommand(ctx, tenantID, identityAuthCommandTarget, "/internal/identity-auth/users/by-mobile", IdentityUserByMobileCommand{Mobile: mobile}, &result); err != nil {
		return IdentityUserByMobileResult{}, err
	}
	if result.UserID <= 0 {
		return IdentityUserByMobileResult{}, store.ErrInvalidUserID
	}
	return result, nil
}
