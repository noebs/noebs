package consumer

import (
	"context"
	"fmt"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/adonese/noebs/utils"
)

type CompletedRegistrationIdentityCommand struct {
	Mobile   string `json:"mobile"`
	Password string `json:"password"`
}

type CompletedRegistrationIdentityResult struct {
	UserID int64 `json:"user_id"`
}

type CompletedRegistrationCardCommand struct {
	Mobile  string `json:"mobile"`
	UserID  int64  `json:"user_id"`
	PAN     string `json:"pan"`
	ExpDate string `json:"expDate,omitempty"`
}

type RegisterWithCardIdentityCommand struct {
	Mobile    string `json:"mobile"`
	Password  string `json:"password"`
	PublicKey string `json:"public_key"`
	Fullname  string `json:"fullname,omitempty"`
}

type RegisterWithCardIdentityResult struct {
	UserID int64 `json:"user_id"`
}

func (s *Service) CreateCompletedRegistrationIdentity(ctx context.Context, tenantID string, cmd CompletedRegistrationIdentityCommand) (CompletedRegistrationIdentityResult, error) {
	if s == nil || s.Store == nil {
		return CompletedRegistrationIdentityResult{}, ErrMissingStore
	}
	if tenantID == "" {
		return CompletedRegistrationIdentityResult{}, store.ErrMissingTenantID
	}
	mobile := strings.TrimSpace(cmd.Mobile)
	if mobile == "" {
		return CompletedRegistrationIdentityResult{}, ErrMissingMobile
	}
	if strings.TrimSpace(cmd.Password) == "" {
		return CompletedRegistrationIdentityResult{}, ErrMissingPassword
	}

	user := ebs_fields.User{
		Mobile:        mobile,
		Username:      mobile,
		Password:      cmd.Password,
		IsVerified:    true,
		IsPasswordOTP: true,
	}
	if err := user.HashPassword(); err != nil {
		return CompletedRegistrationIdentityResult{}, err
	}
	user.SanitizeName()
	if err := s.Store.CreateUser(ctx, tenantID, &user); err != nil {
		return CompletedRegistrationIdentityResult{}, err
	}
	return CompletedRegistrationIdentityResult{UserID: user.ID}, nil
}

func (s *Service) RegisterWithCardIdentity(ctx context.Context, tenantID string, cmd RegisterWithCardIdentityCommand) (RegisterWithCardIdentityResult, error) {
	if s == nil || s.Store == nil {
		return RegisterWithCardIdentityResult{}, ErrMissingStore
	}
	if tenantID == "" {
		return RegisterWithCardIdentityResult{}, store.ErrMissingTenantID
	}
	mobile := strings.TrimSpace(cmd.Mobile)
	if mobile == "" {
		return RegisterWithCardIdentityResult{}, ErrMissingMobile
	}
	password := strings.TrimSpace(cmd.Password)
	if password == "" {
		return RegisterWithCardIdentityResult{}, ErrMissingPassword
	}
	publicKey := strings.TrimSpace(cmd.PublicKey)
	if publicKey == "" {
		return RegisterWithCardIdentityResult{}, ErrMissingPublicKey
	}

	var user *ebs_fields.User
	existing, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	switch {
	case err == nil && existing.IsVerified:
		return RegisterWithCardIdentityResult{}, ErrUserAlreadyExists
	case err == nil:
		user = existing
	case store.ErrNotFound(err):
		user = &ebs_fields.User{Mobile: mobile, Username: mobile}
	default:
		return RegisterWithCardIdentityResult{}, err
	}

	user.Fullname = strings.TrimSpace(cmd.Fullname)
	user.MainCard = ""
	user.ExpDate = ""
	user.Password = password
	user.PublicKey = publicKey
	if err := user.HashPassword(); err != nil {
		return RegisterWithCardIdentityResult{}, err
	}
	otp, err := user.GenerateOtp()
	if err != nil {
		return RegisterWithCardIdentityResult{}, err
	}
	user.SanitizeName()

	if user.ID == 0 {
		if err := s.Store.CreateUser(ctx, tenantID, user); err != nil {
			return RegisterWithCardIdentityResult{}, err
		}
	} else {
		if err := s.Store.UpdateUser(ctx, tenantID, user); err != nil {
			return RegisterWithCardIdentityResult{}, err
		}
	}

	if err := utils.SendSMS(&s.NoebsConfig, utils.SMS{
		Mobile:  mobile,
		Message: fmt.Sprintf("Your one-time access code is: %s. DON'T share it with anyone.", otp),
	}); err != nil {
		return RegisterWithCardIdentityResult{}, err
	}

	return RegisterWithCardIdentityResult{UserID: user.ID}, nil
}

func (s *Service) StoreCompletedRegistrationCard(ctx context.Context, tenantID string, cmd CompletedRegistrationCardCommand) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	if cmd.UserID <= 0 {
		return store.ErrInvalidUserID
	}
	mobile := strings.TrimSpace(cmd.Mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	pan := strings.TrimSpace(cmd.PAN)
	if pan == "" {
		return ErrMissingIssuedPAN
	}

	card := ebs_fields.CacheCards{Pan: pan, Expiry: strings.TrimSpace(cmd.ExpDate), Mobile: mobile}
	if err := s.Store.UpsertCacheCard(ctx, tenantID, card); err != nil {
		return err
	}
	newCard := card.NewCardFromCached(int(cmd.UserID))
	newCard.ID = 0
	newCard.IsMain = true
	return s.Store.AddCards(ctx, tenantID, cmd.UserID, []ebs_fields.Card{newCard})
}

func (s *Service) CreateCompletedRegistrationIdentityInIdentityAuth(ctx context.Context, tenantID string, cmd CompletedRegistrationIdentityCommand) (CompletedRegistrationIdentityResult, error) {
	var result CompletedRegistrationIdentityResult
	if err := s.doAdminServiceCommand(ctx, tenantID, identityAuthCommandTarget, "/internal/identity-auth/card-registration/users", cmd, &result); err != nil {
		return CompletedRegistrationIdentityResult{}, err
	}
	return result, nil
}

func (s *Service) RegisterWithCardIdentityInIdentityAuth(ctx context.Context, tenantID string, cmd RegisterWithCardIdentityCommand) (RegisterWithCardIdentityResult, error) {
	var result RegisterWithCardIdentityResult
	if err := s.doAdminServiceCommand(ctx, tenantID, identityAuthCommandTarget, "/internal/identity-auth/register-with-card/users", cmd, &result); err != nil {
		return RegisterWithCardIdentityResult{}, err
	}
	if result.UserID <= 0 {
		return RegisterWithCardIdentityResult{}, store.ErrInvalidUserID
	}
	return result, nil
}

func (s *Service) StoreCompletedRegistrationCardInCardVault(ctx context.Context, tenantID string, cmd CompletedRegistrationCardCommand) error {
	return s.doAdminServiceCommand(ctx, tenantID, cardVaultCommandTarget, "/internal/card-vault/card-registration/cards", cmd, nil)
}
