package consumer

import (
	"context"
	"strings"
	"unicode"

	"github.com/adonese/noebs/store"
)

type IdentityUserByMobileCommand struct {
	Mobile string `json:"mobile"`
}

type IdentityUserByMobileResult struct {
	UserID int64  `json:"user_id"`
	Mobile string `json:"mobile"`
}

type IdentityUsersBatchCommand struct {
	Mobiles []string `json:"mobiles"`
}

type IdentityUsersBatchResult struct {
	Users []IdentityUserByMobileResult `json:"users"`
}

const maxIdentityUserBatch = 50

func (s *Service) ResolveIdentityUsersBatch(ctx context.Context, tenantID string, cmd IdentityUsersBatchCommand) (IdentityUsersBatchResult, error) {
	if s == nil || s.Store == nil {
		return IdentityUsersBatchResult{}, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return IdentityUsersBatchResult{}, err
	}
	mobiles, err := normalizeIdentityMobiles(cmd.Mobiles)
	if err != nil {
		return IdentityUsersBatchResult{}, err
	}
	users, err := s.Store.ListIdentityUsersByMobile(ctx, tenantID, mobiles)
	if err != nil {
		return IdentityUsersBatchResult{}, err
	}
	result := IdentityUsersBatchResult{Users: make([]IdentityUserByMobileResult, 0, len(users))}
	for _, user := range users {
		if user.UserID <= 0 {
			return IdentityUsersBatchResult{}, store.ErrInvalidUserID
		}
		result.Users = append(result.Users, IdentityUserByMobileResult{UserID: user.UserID, Mobile: user.Mobile})
	}
	return result, nil
}

func normalizeIdentityMobiles(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > maxIdentityUserBatch {
		return nil, store.ErrInvalidMobile
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		mobile := strings.TrimSpace(value)
		if len(mobile) != 10 || strings.IndexFunc(mobile, func(r rune) bool { return !unicode.IsDigit(r) || r > unicode.MaxASCII }) >= 0 {
			return nil, store.ErrInvalidMobile
		}
		if _, exists := seen[mobile]; exists {
			continue
		}
		seen[mobile] = struct{}{}
		result = append(result, mobile)
	}
	return result, nil
}

func (s *Service) ResolveIdentityUserByMobile(ctx context.Context, tenantID string, cmd IdentityUserByMobileCommand) (IdentityUserByMobileResult, error) {
	if s == nil || s.Store == nil {
		return IdentityUserByMobileResult{}, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return IdentityUserByMobileResult{}, err
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
