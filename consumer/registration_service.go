package consumer

import (
	"context"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

// RegisterWithCard validates card credentials through EBS, then commands
// identity-auth and card-vault to persist their service-owned state.
func (s *Service) RegisterWithCard(ctx context.Context, tenantID string, card ebs_fields.CacheCards) error {
	if s == nil {
		return ErrMissingService
	}
	if s.Store == nil {
		return ErrMissingStore
	}
	if s.HTTPClient == nil {
		return ErrMissingHTTPClient
	}
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	card.Mobile = strings.TrimSpace(card.Mobile)
	card.PublicKey = strings.TrimSpace(card.PublicKey)
	card.Pan = strings.TrimSpace(card.Pan)
	card.Expiry = strings.TrimSpace(card.Expiry)
	card.Password = strings.TrimSpace(card.Password)
	if card.Mobile == "" {
		return ErrMissingMobile
	}
	if card.PublicKey == "" {
		return ErrMissingPublicKey
	}
	if card.Password == "" {
		return ErrMissingPassword
	}
	if card.Pan == "" {
		return store.ErrMissingPAN
	}
	if card.Expiry == "" {
		return ErrMissingCardExpiry
	}
	ok, err := s.isValidCard(ctx, tenantID, card)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidCard
	}

	identity, err := s.RegisterWithCardIdentityInIdentityAuth(ctx, tenantID, RegisterWithCardIdentityCommand{
		Mobile:    card.Mobile,
		Password:  card.Password,
		PublicKey: card.PublicKey,
		Fullname:  card.Name,
	})
	if err != nil {
		return err
	}
	return s.StoreCompletedRegistrationCardInCardVault(ctx, tenantID, CompletedRegistrationCardCommand{
		Mobile:  card.Mobile,
		UserID:  identity.UserID,
		PAN:     card.Pan,
		ExpDate: card.Expiry,
	})
}

// CompleteRegistration performs step 2 in card issuance, then commands identity-auth
// and card-vault to persist their service-owned state.
func (s *Service) CompleteRegistration(ctx context.Context, tenantID string, fields ebs_fields.ConsumerCompleteRegistrationFields) (ebs_fields.EBSParserFields, error) {
	if s == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingService
	}
	if s.HTTPClient == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingHTTPClient
	}
	if tenantID == "" {
		return ebs_fields.EBSParserFields{}, store.ErrMissingTenantID
	}
	mobile := strings.TrimSpace(fields.Mobile)
	password := fields.NoebsPassword
	if mobile == "" {
		return ebs_fields.EBSParserFields{}, ErrMissingMobile
	}
	if strings.TrimSpace(password) == "" {
		return ebs_fields.EBSParserFields{}, ErrMissingPassword
	}

	// Never send noebs-specific fields to EBS.
	req := fields
	req.NoebsPassword = ""
	req.Mobile = ""

	var issuedPan string
	var issuedExp string
	res, err := s.callEBSJSONWithMutate(
		ctx,
		tenantID,
		s.NoebsConfig.ConsumerIP,
		ebs_fields.ConsumerCompleteRegistration,
		req,
		func(p *ebs_fields.EBSParserFields) {
			issuedPan = p.PAN
			issuedExp = p.ExpDate
		},
	)
	if err != nil {
		return res, err
	}

	issuedPan = strings.TrimSpace(issuedPan)
	if issuedPan == "" {
		return res, ErrMissingIssuedPAN
	}
	identity, err := s.CreateCompletedRegistrationIdentityInIdentityAuth(ctx, tenantID, CompletedRegistrationIdentityCommand{
		Mobile:   mobile,
		Password: password,
	})
	if err != nil {
		return res, err
	}
	if err := s.StoreCompletedRegistrationCardInCardVault(ctx, tenantID, CompletedRegistrationCardCommand{
		Mobile:  mobile,
		UserID:  identity.UserID,
		PAN:     issuedPan,
		ExpDate: issuedExp,
	}); err != nil {
		return res, err
	}

	return res, nil
}
