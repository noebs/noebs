package consumer

import (
	"context"
	"errors"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

type BalanceStepRequest struct {
	ebs_fields.ConsumerBalanceFields
	Mobile string `json:"mobile,omitempty"`
}

// BalanceStep validates card credentials through card-vault and EBS, then
// commands identity-auth to issue the account-recovery JWT.
func (s *Service) BalanceStep(ctx context.Context, tenantID string, req BalanceStepRequest) (string, error) {
	if s == nil {
		return "", ErrMissingService
	}
	if s.Store == nil {
		return "", ErrMissingStore
	}
	if s.HTTPClient == nil {
		return "", ErrMissingHTTPClient
	}
	if tenantID == "" {
		return "", store.ErrMissingTenantID
	}
	mobile := strings.TrimSpace(req.Mobile)
	if mobile == "" {
		return "", ErrMissingMobile
	}
	pan := strings.TrimSpace(req.Pan)
	if pan == "" {
		return "", store.ErrMissingPAN
	}

	card, err := s.ResolveCardByMobilePANInCardVault(ctx, tenantID, mobile, pan)
	if err != nil {
		return "", err
	}

	req.Mobile = ""
	req.Pan = pan
	req.ExpDate = card.ExpDate
	req.ApplicationId = s.NoebsConfig.ConsumerID
	if _, err := s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerBalanceEndpoint, req); err != nil {
		var ebsErr *ebs_fields.CallError
		if errors.As(err, &ebsErr) {
			return "", ErrTransactionFailed
		}
		return "", err
	}

	token, err := s.IssueRecoveryJWTInIdentityAuth(ctx, tenantID, RecoveryJWTCommand{UserID: card.UserID, Mobile: mobile})
	if err != nil {
		return "", err
	}
	return token.Token, nil
}
