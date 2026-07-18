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
		return nil, nil, errors.New("no cards found")
	}
	main := cards[0]
	return cards, &main, nil
}

func (s *Service) AddDeviceToken(ctx context.Context, tenantID, mobile, token string) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return store.ErrMissingToken
	}
	return s.Store.UpsertDeviceToken(ctx, tenantID, mobile, token)
}

func (s *Service) ListBeneficiariesForUserID(ctx context.Context, tenantID string, userID int64) ([]ebs_fields.Beneficiary, error) {
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
	return s.Store.ListBeneficiaries(ctx, tenantID, userID)
}

func (s *Service) UpsertBeneficiaryForUserID(ctx context.Context, tenantID string, userID int64, b ebs_fields.Beneficiary) error {
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
	b.UserID = userID
	return s.Store.UpsertBeneficiary(ctx, tenantID, userID, b)
}

func (s *Service) DeleteBeneficiaryForUserID(ctx context.Context, tenantID string, userID int64, data string) error {
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
	return s.Store.DeleteBeneficiary(ctx, tenantID, userID, data)
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

func (s *Service) GetUserProfile(ctx context.Context, tenantID, mobile string) (ebs_fields.UserProfile, error) {
	if s == nil || s.Store == nil {
		return ebs_fields.UserProfile{}, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return ebs_fields.UserProfile{}, err
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ebs_fields.UserProfile{}, ErrMissingMobile
	}
	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return ebs_fields.UserProfile{}, err
	}
	return ebs_fields.UserProfile{
		Fullname: user.Fullname,
		Username: user.Username,
		Email:    user.Email,
		Birthday: user.Birthday,
		Gender:   user.Gender,
	}, nil
}

func (s *Service) UpdateUserProfile(ctx context.Context, tenantID, mobile string, profile ebs_fields.UserProfile) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	profile, err = normalizeUserProfileInput(profile)
	if err != nil {
		return err
	}
	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return err
	}
	if profile.Username != "" {
		if other, err := s.Store.FindUserByUsername(ctx, tenantID, profile.Username); err == nil {
			if other.ID != user.ID {
				return errors.New("username already exists")
			}
		} else if !store.ErrNotFound(err) {
			return err
		}
	}
	return s.Store.UpdateUserProfile(ctx, tenantID, user.ID, profile)
}

func (s *Service) GetUserLanguage(ctx context.Context, tenantID, mobile string) (string, error) {
	if s == nil || s.Store == nil {
		return "", ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return "", err
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return "", ErrMissingMobile
	}
	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return "", err
	}
	return user.Language, nil
}

func (s *Service) SetUserLanguage(ctx context.Context, tenantID, mobile, language string) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	language = strings.TrimSpace(language)
	if language == "" {
		return store.ErrMissingLanguage
	}
	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return err
	}
	return s.Store.UpdateUserLanguage(ctx, tenantID, user.ID, language)
}

func normalizeUserProfileInput(profile ebs_fields.UserProfile) (ebs_fields.UserProfile, error) {
	if profile.Fullname != "" {
		profile.Fullname = strings.TrimSpace(profile.Fullname)
	}
	if profile.Username != "" {
		profile.Username = strings.TrimSpace(profile.Username)
		if profile.Username == "" {
			return profile, store.ErrMissingUsername
		}
	}
	if profile.Email != "" {
		profile.Email = strings.ToLower(strings.TrimSpace(profile.Email))
		if profile.Email == "" {
			return profile, store.ErrMissingEmail
		}
	}
	if profile.Birthday != "" {
		profile.Birthday = strings.TrimSpace(profile.Birthday)
	}
	if profile.Gender != "" {
		profile.Gender = strings.TrimSpace(profile.Gender)
	}
	if profile.Fullname == "" && profile.Username == "" && profile.Email == "" && profile.Birthday == "" && profile.Gender == "" {
		return profile, store.ErrMissingData
	}
	return profile, nil
}

func (s *Service) UpdateKYC(ctx context.Context, tenantID, authenticatedMobile string, req ebs_fields.KYCPassport) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	authenticatedMobile = strings.TrimSpace(authenticatedMobile)
	if authenticatedMobile == "" {
		return ErrMissingMobile
	}
	user, err := s.Store.GetUserByMobile(ctx, tenantID, authenticatedMobile)
	if err != nil {
		return err
	}

	kyc := &ebs_fields.KYC{
		UserMobile:  user.Mobile,
		Mobile:      user.Mobile,
		Selfie:      req.Selfie,
		PassportImg: req.PassportImg,
	}
	passport := req.Passport
	passport.Mobile = user.Mobile
	return s.Store.UpdateKYC(ctx, tenantID, kyc, &passport)
}

func (s *Service) GetTransactionByUUIDForUser(ctx context.Context, tenantID string, userID int64, authenticatedMobile, uuid string) (*ebs_fields.EBSResponse, error) {
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
	authenticatedMobile = strings.TrimSpace(authenticatedMobile)
	if authenticatedMobile == "" {
		return nil, ErrMissingMobile
	}
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return nil, store.ErrMissingUUID
	}
	cards, err := s.ListMaskedCardsInCardVault(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	if len(cards.MaskedPANs) == 0 {
		return nil, ErrTransactionNotFound
	}
	transaction, err := s.Store.GetTransactionByUUIDForMaskedPANs(ctx, tenantID, uuid, cards.MaskedPANs)
	if store.ErrNotFound(err) {
		return nil, ErrTransactionNotFound
	}
	if err != nil {
		return nil, err
	}
	return transaction, nil
}
