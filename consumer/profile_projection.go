package consumer

import (
	"context"

	"github.com/adonese/noebs/store"
)

type PrincipalProjectionReference struct {
	Issuer  string `json:"issuer" binding:"required"`
	Subject string `json:"subject" binding:"required"`
}

type CreateProfileProjectionCommand struct {
	Fullname    string `json:"fullname"`
	Username    string `json:"username,omitempty"`
	Gender      string `json:"gender,omitempty"`
	Birthday    string `json:"birthday,omitempty"`
	Email       string `json:"email,omitempty"`
	Mobile      string `json:"mobile"`
	DeviceToken string `json:"device_token,omitempty"`
	Language    string `json:"language,omitempty"`
}

type ProfileProjection struct {
	UserID      int64  `json:"user_id"`
	TenantID    string `json:"tenant_id"`
	Issuer      string `json:"issuer"`
	Subject     string `json:"subject"`
	Fullname    string `json:"fullname"`
	Username    string `json:"username,omitempty"`
	Gender      string `json:"gender,omitempty"`
	Birthday    string `json:"birthday,omitempty"`
	Email       string `json:"email,omitempty"`
	Mobile      string `json:"mobile"`
	DeviceToken string `json:"device_token,omitempty"`
	Language    string `json:"language,omitempty"`
}

type ResolveProfileProjectionResult struct {
	UserID int64 `json:"user_id"`
}

// ResolveProfileProjection is read-only. An unknown principal is returned as
// not found; lookup never creates a local profile.
func (s *Service) ResolveProfileProjection(ctx context.Context, tenantID string, reference PrincipalProjectionReference) (ProfileProjection, error) {
	if s == nil || s.Store == nil {
		return ProfileProjection{}, ErrMissingStore
	}
	profile, err := s.Store.ResolveProfileProjection(ctx, store.PrincipalIdentity{
		TenantID: tenantID,
		Issuer:   reference.Issuer,
		Subject:  reference.Subject,
	})
	if err != nil {
		return ProfileProjection{}, err
	}
	return profileProjectionFromStore(profile), nil
}

// CreateProfileProjection is an explicit onboarding command. It does not
// resolve-or-create and does not create tenant state.
func (s *Service) CreateProfileProjection(ctx context.Context, tenantID string, principal PrincipalProjectionReference, command CreateProfileProjectionCommand) (ProfileProjection, error) {
	if s == nil || s.Store == nil {
		return ProfileProjection{}, ErrMissingStore
	}
	profile, err := s.Store.CreateProfileProjection(ctx, store.CreateProfileProjectionParams{
		PrincipalIdentity: store.PrincipalIdentity{
			TenantID: tenantID,
			Issuer:   principal.Issuer,
			Subject:  principal.Subject,
		},
		Fullname:    command.Fullname,
		Username:    command.Username,
		Gender:      command.Gender,
		Birthday:    command.Birthday,
		Email:       command.Email,
		Mobile:      command.Mobile,
		DeviceToken: command.DeviceToken,
		Language:    command.Language,
	})
	if err != nil {
		return ProfileProjection{}, err
	}
	return profileProjectionFromStore(profile), nil
}

func profileProjectionFromStore(profile store.ProfileProjection) ProfileProjection {
	return ProfileProjection{
		UserID:      profile.UserID,
		TenantID:    profile.TenantID,
		Issuer:      profile.Issuer,
		Subject:     profile.Subject,
		Fullname:    profile.Fullname,
		Username:    profile.Username,
		Gender:      profile.Gender,
		Birthday:    profile.Birthday,
		Email:       profile.Email,
		Mobile:      profile.Mobile,
		DeviceToken: profile.DeviceToken,
		Language:    profile.Language,
	}
}
