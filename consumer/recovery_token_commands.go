package consumer

import (
	"context"
	"strings"

	"github.com/adonese/noebs/store"
)

type RecoveryJWTCommand struct {
	UserID int64  `json:"user_id"`
	Mobile string `json:"mobile"`
}

type RecoveryJWTResult struct {
	Token string `json:"token"`
}

func (s *Service) IssueRecoveryJWT(ctx context.Context, tenantID string, cmd RecoveryJWTCommand) (RecoveryJWTResult, error) {
	if s == nil || s.Store == nil {
		return RecoveryJWTResult{}, ErrMissingStore
	}
	if s.Auth == nil {
		return RecoveryJWTResult{}, ErrMissingAuth
	}
	if tenantID == "" {
		return RecoveryJWTResult{}, store.ErrMissingTenantID
	}
	if cmd.UserID <= 0 {
		return RecoveryJWTResult{}, store.ErrInvalidUserID
	}
	mobile := strings.TrimSpace(cmd.Mobile)
	if mobile == "" {
		return RecoveryJWTResult{}, ErrMissingMobile
	}
	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return RecoveryJWTResult{}, err
	}
	if user.ID != cmd.UserID {
		return RecoveryJWTResult{}, store.ErrInvalidUserID
	}
	token, err := s.Auth.GenerateJWT(user.ID, user.Mobile, tenantID)
	if err != nil {
		return RecoveryJWTResult{}, err
	}
	return RecoveryJWTResult{Token: token}, nil
}

func (s *Service) IssueRecoveryJWTInIdentityAuth(ctx context.Context, tenantID string, cmd RecoveryJWTCommand) (RecoveryJWTResult, error) {
	var result RecoveryJWTResult
	if err := s.doAdminServiceCommand(ctx, tenantID, identityAuthCommandTarget, "/internal/identity-auth/recovery-jwt", cmd, &result); err != nil {
		return RecoveryJWTResult{}, err
	}
	if strings.TrimSpace(result.Token) == "" {
		return RecoveryJWTResult{}, ErrInvalidRecoveryJWT
	}
	return result, nil
}
