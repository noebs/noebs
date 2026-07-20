package consumer

import (
	"context"
	"errors"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func (s *Service) AddDeviceToken(ctx context.Context, tenantID string, userID int64, token string) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	return s.Store.SetProfileDeviceToken(ctx, tenantID, userID, token)
}

func (s *Service) NecToName(ctx context.Context, tenantID, nec string) (string, error) {
	if s == nil || s.Store == nil {
		return "", ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return "", err
	}
	nec = strings.TrimSpace(nec)
	if nec == "" {
		return "", errors.New("missing nec")
	}
	return s.Store.GetMeterName(ctx, tenantID, nec)
}

func (s *Service) GetUserProfile(ctx context.Context, tenantID string, userID int64) (ebs_fields.UserProfile, error) {
	if s == nil || s.Store == nil {
		return ebs_fields.UserProfile{}, ErrMissingStore
	}
	profile, err := s.Store.FindProfileProjectionByUserID(ctx, tenantID, userID)
	if err != nil {
		return ebs_fields.UserProfile{}, err
	}
	return ebs_fields.UserProfile{
		Fullname: profile.Fullname,
		Username: profile.Username,
		Email:    profile.Email,
		Birthday: profile.Birthday,
		Gender:   profile.Gender,
	}, nil
}

func (s *Service) UpdateUserProfile(ctx context.Context, tenantID string, userID int64, profile ebs_fields.UserProfile) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	return s.Store.UpdateProfileProjection(ctx, tenantID, userID, profileUpdate(profile))
}

func (s *Service) GetUserLanguage(ctx context.Context, tenantID string, userID int64) (string, error) {
	if s == nil || s.Store == nil {
		return "", ErrMissingStore
	}
	profile, err := s.Store.FindProfileProjectionByUserID(ctx, tenantID, userID)
	if err != nil {
		return "", err
	}
	return profile.Language, nil
}

func (s *Service) SetUserLanguage(ctx context.Context, tenantID string, userID int64, language string) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	return s.Store.SetProfileLanguage(ctx, tenantID, userID, language)
}

func (s *Service) UpdateKYC(ctx context.Context, tenantID string, userID int64, req ebs_fields.KYCPassport) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	return s.Store.UpdateProfileKYC(ctx, tenantID, userID, req)
}

func profileUpdate(profile ebs_fields.UserProfile) store.ProfileProjectionUpdate {
	update := store.ProfileProjectionUpdate{}
	if profile.Fullname != "" {
		update.Fullname = &profile.Fullname
	}
	if profile.Username != "" {
		update.Username = &profile.Username
	}
	if profile.Email != "" {
		update.Email = &profile.Email
	}
	if profile.Birthday != "" {
		update.Birthday = &profile.Birthday
	}
	if profile.Gender != "" {
		update.Gender = &profile.Gender
	}
	return update
}

func (s *Service) GetTransactionByUUIDForUser(ctx context.Context, tenantID string, userID int64, uuid string) (*ebs_fields.EBSResponse, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, store.ErrInvalidUserID
	}
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return nil, store.ErrMissingUUID
	}
	transaction, err := s.Store.GetTransactionByUUIDForParticipantUserID(ctx, tenantID, userID, uuid)
	if store.ErrNotFound(err) {
		return nil, ErrTransactionNotFound
	}
	if err != nil {
		return nil, err
	}
	return transaction, nil
}
