package consumer

import (
	"context"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

type CompletedRegistrationIdentityCommand struct {
	Mobile   string `json:"mobile"`
	Password string `json:"password"`
	PAN      string `json:"pan"`
	ExpDate  string `json:"expDate,omitempty"`
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
	pan := strings.TrimSpace(cmd.PAN)
	if pan == "" {
		return CompletedRegistrationIdentityResult{}, ErrMissingIssuedPAN
	}

	user := ebs_fields.User{
		Mobile:        mobile,
		Username:      mobile,
		Password:      cmd.Password,
		MainCard:      pan,
		ExpDate:       strings.TrimSpace(cmd.ExpDate),
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

func (s *Service) StoreCompletedRegistrationCardInCardVault(ctx context.Context, tenantID string, cmd CompletedRegistrationCardCommand) error {
	return s.doAdminServiceCommand(ctx, tenantID, cardVaultCommandTarget, "/internal/card-vault/card-registration/cards", cmd, nil)
}
