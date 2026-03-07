package consumer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/adonese/noebs/utils"
)

var (
	ErrMissingMobile    = errors.New("missing mobile")
	ErrMissingPublicKey = errors.New("missing public key")
	ErrInvalidCard      = errors.New("invalid card")
)

// RegisterWithCard creates or updates a noebs user and associates a card with them.
//
// NOTE: this assumes the provided card is already in our cache or can be validated
// against EBS (see isValidCard).
func (s *Service) RegisterWithCard(ctx context.Context, tenantID string, card ebs_fields.CacheCards) (string, error) {
	if s == nil || s.Store == nil {
		return "", ErrMissingStore
	}
	if tenantID == "" {
		return "", store.ErrMissingTenantID
	}
	card.Mobile = strings.TrimSpace(card.Mobile)
	card.PublicKey = strings.TrimSpace(card.PublicKey)
	if card.Mobile == "" {
		return "", ErrMissingMobile
	}
	if card.PublicKey == "" {
		return "", ErrMissingPublicKey
	}

	ok, err := s.isValidCard(ctx, tenantID, card)
	if !ok {
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrInvalidCard, err)
		}
		return "", ErrInvalidCard
	}

	var user *ebs_fields.User
	tmpUser, err := s.Store.GetUserByMobile(ctx, tenantID, card.Mobile)
	switch {
	case err == nil && tmpUser.IsVerified:
		return "", errors.New("user already exists")
	case err == nil:
		user = tmpUser
	case store.ErrNotFound(err):
		user = &ebs_fields.User{Mobile: card.Mobile, Username: card.Mobile}
	default:
		return "", err
	}

	user.Fullname = card.Name
	user.MainCard = card.Pan
	user.ExpDate = card.Expiry
	user.Password = card.Password
	user.PublicKey = card.PublicKey
	if err := user.HashPassword(); err != nil {
		return "", err
	}

	otp, err := user.GenerateOtp()
	if err != nil {
		return "", err
	}

	if user.ID == 0 {
		if err := s.Store.CreateUser(ctx, tenantID, user); err != nil {
			return "", err
		}
	} else {
		if err := s.Store.UpdateUser(ctx, tenantID, user); err != nil {
			return "", err
		}
	}

	ucard := card.NewCardFromCached(int(user.ID))
	ucard.ID = 0
	ucard.IsMain = true
	if err := s.Store.AddCards(ctx, tenantID, user.ID, []ebs_fields.Card{ucard}); err != nil {
		return "", err
	}

	go utils.SendSMS(&s.NoebsConfig, utils.SMS{
		Mobile:  card.Mobile,
		Message: fmt.Sprintf("Your one-time access code is: %s. DON'T share it with anyone.", otp),
	})

	return otp, nil
}

// CompleteRegistration performs step 2 in card issuance and creates a local user + card on success.
func (s *Service) CompleteRegistration(ctx context.Context, tenantID string, fields ebs_fields.ConsumerCompleteRegistrationFields) (ebs_fields.EBSParserFields, error) {
	if s == nil || s.Store == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingStore
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

	// Create the local user first (matches legacy behavior).
	user := ebs_fields.User{Mobile: mobile, Username: mobile, Password: password}
	if err := user.HashPassword(); err != nil {
		return ebs_fields.EBSParserFields{}, err
	}
	user.SanitizeName()
	if err := s.Store.CreateUser(ctx, tenantID, &user); err != nil {
		return ebs_fields.EBSParserFields{}, err
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

	// Associate the issued card to that user.
	if issuedPan != "" {
		card := ebs_fields.CacheCards{Pan: issuedPan, Expiry: issuedExp}
		if err := s.Store.UpsertCacheCard(ctx, tenantID, card); err != nil {
			return res, err
		}

		user.MainCard = issuedPan
		user.ExpDate = issuedExp
		if err := s.Store.UpdateUserColumns(ctx, tenantID, user.ID, map[string]any{
			"main_card":       issuedPan,
			"main_expdate":    issuedExp,
			"is_verified":     true,
			"is_password_otp": true,
		}); err != nil {
			return res, err
		}

		newCard := card.NewCardFromCached(int(user.ID))
		newCard.ID = 0
		newCard.IsMain = true
		if err := s.Store.AddCards(ctx, tenantID, user.ID, []ebs_fields.Card{newCard}); err != nil {
			return res, err
		}
	}

	return res, nil
}
