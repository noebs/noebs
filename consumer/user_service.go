package consumer

import (
	"context"
	"errors"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func (s *Service) GetCardsByUserID(ctx context.Context, tenantID string, userID int64) ([]ebs_fields.Card, *ebs_fields.Card, error) {
	if s == nil || s.Store == nil {
		return nil, nil, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return nil, nil, err
	}
	if userID <= 0 {
		return nil, nil, store.ErrInvalidUserID
	}
	cards, err := s.Store.ListCardsByUserID(ctx, tenantID, userID)
	if err != nil {
		return nil, nil, err
	}
	if len(cards) == 0 {
		return []ebs_fields.Card{}, nil, nil
	}
	main := cards[0]
	return cards, &main, nil
}

func (s *Service) AddDeviceToken(ctx context.Context, tenantID string, userID int64, token string) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	return s.Store.SetProfileDeviceToken(ctx, tenantID, userID, token)
}

func (s *Service) ListBeneficiariesForUserID(ctx context.Context, tenantID string, userID int64) ([]ebs_fields.Beneficiary, error) {
	return nil, store.ErrBeneficiaryRetired
}

func (s *Service) UpsertBeneficiaryForUserID(ctx context.Context, tenantID string, userID int64, b ebs_fields.Beneficiary) error {
	return store.ErrBeneficiaryRetired
}

func (s *Service) DeleteBeneficiaryForUserID(ctx context.Context, tenantID string, userID int64, data string) error {
	return store.ErrBeneficiaryRetired
}

func (s *Service) AddCardsForUserID(ctx context.Context, tenantID string, userID int64, mobile string, cards []ebs_fields.Card) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return store.ErrInvalidUserID
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	prepareCardsForUser(cards, userID, mobile)
	return s.Store.AddCards(ctx, tenantID, userID, cards)
}

func prepareCardsForUser(cards []ebs_fields.Card, userID int64, mobile string) {
	for i := range cards {
		cards[i].ID = 0
		cards[i].UserID = userID
		cards[i].Mobile = mobile
	}
}

func (s *Service) EditCardForUserID(ctx context.Context, tenantID string, userID int64, card ebs_fields.Card) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return store.ErrInvalidUserID
	}
	if strings.TrimSpace(card.CardIdx) == "" {
		return errors.New("card idx is empty")
	}
	card.UserID = userID
	return s.Store.UpdateCard(ctx, tenantID, userID, card)
}

func (s *Service) RemoveCardForUserID(ctx context.Context, tenantID string, userID int64, cardIdx string) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return store.ErrInvalidUserID
	}
	cardIdx = strings.TrimSpace(cardIdx)
	if cardIdx == "" {
		return errors.New("card idx is empty")
	}
	return s.Store.DeleteCard(ctx, tenantID, userID, cardIdx)
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

func (s *Service) Notifications(ctx context.Context, tenantID, mobile string) ([]ebs_fields.PushDataRecord, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return nil, ErrMissingMobile
	}
	records, err := s.Store.GetNotifications(ctx, tenantID, mobile)
	if err != nil {
		return nil, err
	}
	if err := s.Store.MarkNotificationsRead(ctx, tenantID, mobile); err != nil {
		return nil, err
	}
	return records, nil
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
