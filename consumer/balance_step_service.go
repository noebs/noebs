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
// commands identity-auth to issue a one-time account-recovery credential.
func (s *Service) BalanceStep(ctx context.Context, tenantID string, req BalanceStepRequest) (RecoveryCredentialResult, error) {
	if s == nil {
		return RecoveryCredentialResult{}, ErrMissingService
	}
	if s.Store == nil {
		return RecoveryCredentialResult{}, ErrMissingStore
	}
	if s.HTTPClient == nil {
		return RecoveryCredentialResult{}, ErrMissingHTTPClient
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return RecoveryCredentialResult{}, err
	}
	mobile := strings.TrimSpace(req.Mobile)
	if mobile == "" {
		return RecoveryCredentialResult{}, ErrMissingMobile
	}
	pan := strings.TrimSpace(req.Pan)
	if pan == "" {
		return RecoveryCredentialResult{}, store.ErrMissingPAN
	}

	card, err := s.ResolveCardByMobilePANInCardVault(ctx, tenantID, mobile, pan)
	if err != nil {
		return RecoveryCredentialResult{}, err
	}

	req.Mobile = ""
	req.Pan = pan
	req.ExpDate = card.ExpDate
	req.ApplicationId = s.NoebsConfig.ConsumerID
	if _, err := s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerBalanceEndpoint, req); err != nil {
		var ebsErr *ebs_fields.CallError
		if errors.As(err, &ebsErr) {
			return RecoveryCredentialResult{}, ErrTransactionFailed
		}
		return RecoveryCredentialResult{}, err
	}

	credential, err := s.IssueRecoveryCredentialInIdentityAuth(ctx, tenantID, RecoveryCredentialCommand{UserID: card.UserID, Mobile: mobile})
	if err != nil {
		return RecoveryCredentialResult{}, err
	}
	return credential, nil
}
