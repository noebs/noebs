package consumer

import (
	"context"
	"errors"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func (s *Service) CardFromNumber(ctx context.Context, tenantID, mobileNumber string) (string, error) {
	if s == nil || s.Store == nil {
		return "", ErrMissingStore
	}
	if tenantID == "" {
		return "", store.ErrMissingTenantID
	}
	mobileNumber = strings.TrimSpace(mobileNumber)
	if mobileNumber == "" {
		return "", ErrMissingMobile
	}
	identity, err := s.ResolveIdentityUserByMobileInIdentityAuth(ctx, tenantID, mobileNumber)
	if err != nil {
		return "", err
	}
	cards, err := s.Store.ListCardsByUserID(ctx, tenantID, identity.UserID)
	if err != nil {
		return "", err
	}
	if len(cards) == 0 {
		return "", errors.New("no cards")
	}
	return cards[0].Pan, nil
}

// GetCards returns the full card list for a user plus their main card.
func (s *Service) GetCards(ctx context.Context, tenantID, mobile string) ([]ebs_fields.Card, *ebs_fields.Card, error) {
	if s == nil || s.Store == nil {
		return nil, nil, ErrMissingStore
	}
	if tenantID == "" {
		return nil, nil, store.ErrMissingTenantID
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return nil, nil, ErrMissingMobile
	}
	identity, err := s.ResolveIdentityUserByMobileInIdentityAuth(ctx, tenantID, mobile)
	if err != nil {
		return nil, nil, err
	}
	cards, err := s.Store.ListCardsByUserID(ctx, tenantID, identity.UserID)
	if err != nil {
		return nil, nil, err
	}
	if len(cards) == 0 {
		return nil, nil, errors.New("no cards found")
	}
	main := cards[0]
	return cards, &main, nil
}

func (s *Service) GetCardsByUserID(ctx context.Context, tenantID string, userID int64) ([]ebs_fields.Card, *ebs_fields.Card, error) {
	if s == nil || s.Store == nil {
		return nil, nil, ErrMissingStore
	}
	if tenantID == "" {
		return nil, nil, store.ErrMissingTenantID
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
	if tenantID == "" {
		return store.ErrMissingTenantID
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

func (s *Service) ListBeneficiaries(ctx context.Context, tenantID, mobile string) ([]ebs_fields.Beneficiary, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	if tenantID == "" {
		return nil, store.ErrMissingTenantID
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return nil, ErrMissingMobile
	}
	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return nil, err
	}
	return s.Store.ListBeneficiaries(ctx, tenantID, user.ID)
}

func (s *Service) UpsertBeneficiary(ctx context.Context, tenantID, mobile string, b ebs_fields.Beneficiary) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return err
	}
	b.UserID = user.ID
	return s.Store.UpsertBeneficiary(ctx, tenantID, user.ID, b)
}

func (s *Service) DeleteBeneficiary(ctx context.Context, tenantID, mobile, data string) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return err
	}
	return s.Store.DeleteBeneficiary(ctx, tenantID, user.ID, data)
}

func (s *Service) AddCards(ctx context.Context, tenantID, mobile string, cards []ebs_fields.Card) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return err
	}
	for i := range cards {
		cards[i].ID = 0
		cards[i].UserID = user.ID
	}
	return s.Store.AddCards(ctx, tenantID, user.ID, cards)
}

func (s *Service) AddCardsForUserID(ctx context.Context, tenantID string, userID int64, cards []ebs_fields.Card) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	if userID <= 0 {
		return store.ErrInvalidUserID
	}
	for i := range cards {
		cards[i].ID = 0
		cards[i].UserID = userID
	}
	return s.Store.AddCards(ctx, tenantID, userID, cards)
}

func (s *Service) EditCard(ctx context.Context, tenantID, mobile string, card ebs_fields.Card) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	if strings.TrimSpace(card.CardIdx) == "" {
		return errors.New("card idx is empty")
	}
	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return err
	}
	card.UserID = user.ID
	return s.Store.UpdateCard(ctx, tenantID, user.ID, card)
}

func (s *Service) EditCardForUserID(ctx context.Context, tenantID string, userID int64, card ebs_fields.Card) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	if tenantID == "" {
		return store.ErrMissingTenantID
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

func (s *Service) RemoveCard(ctx context.Context, tenantID, mobile, cardIdx string) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	cardIdx = strings.TrimSpace(cardIdx)
	if cardIdx == "" {
		return errors.New("card idx is empty")
	}
	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return err
	}
	return s.Store.DeleteCard(ctx, tenantID, user.ID, cardIdx)
}

func (s *Service) RemoveCardForUserID(ctx context.Context, tenantID string, userID int64, cardIdx string) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	if tenantID == "" {
		return store.ErrMissingTenantID
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
	if tenantID == "" {
		return "", store.ErrMissingTenantID
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
	if tenantID == "" {
		return nil, store.ErrMissingTenantID
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return nil, ErrMissingMobile
	}
	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return nil, err
	}
	records, err := s.Store.GetNotifications(ctx, tenantID, user.Mobile)
	if err != nil {
		return nil, err
	}
	_ = s.Store.MarkNotificationsRead(ctx, tenantID, mobile)
	return records, nil
}

func (s *Service) GetUserProfile(ctx context.Context, tenantID, mobile string) (ebs_fields.UserProfile, error) {
	if s == nil || s.Store == nil {
		return ebs_fields.UserProfile{}, ErrMissingStore
	}
	if tenantID == "" {
		return ebs_fields.UserProfile{}, store.ErrMissingTenantID
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
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return err
	}
	if profile.Username != "" {
		if other, err := s.Store.FindUserByUsername(ctx, tenantID, profile.Username); err == nil && other.ID != user.ID {
			return errors.New("username already exists")
		}
	}
	return s.Store.UpdateUserProfile(ctx, tenantID, user.ID, profile)
}

func (s *Service) GetUserLanguage(ctx context.Context, tenantID, mobile string) (string, error) {
	if s == nil || s.Store == nil {
		return "", ErrMissingStore
	}
	if tenantID == "" {
		return "", store.ErrMissingTenantID
	}
	user, err := s.Store.GetUserByMobile(ctx, tenantID, strings.TrimSpace(mobile))
	if err != nil {
		return "", err
	}
	return user.Language, nil
}

func (s *Service) SetUserLanguage(ctx context.Context, tenantID, mobile, language string) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	user, err := s.Store.GetUserByMobile(ctx, tenantID, strings.TrimSpace(mobile))
	if err != nil {
		return err
	}
	language = strings.TrimSpace(language)
	if language == "" {
		return errors.New("missing language")
	}
	return s.Store.UpdateUserLanguage(ctx, tenantID, user.ID, language)
}

func (s *Service) UpdateKYC(ctx context.Context, tenantID string, req ebs_fields.KYCPassport) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	if strings.TrimSpace(req.Mobile) == "" {
		return ErrMissingMobile
	}

	kyc := &ebs_fields.KYC{
		UserMobile:  req.Mobile,
		Mobile:      req.Mobile,
		Selfie:      req.Selfie,
		PassportImg: req.PassportImg,
	}
	passport := req.Passport
	return s.Store.UpdateKYC(ctx, tenantID, kyc, &passport)
}

func (s *Service) GetTransactionByUUID(ctx context.Context, tenantID, uuid string) (*ebs_fields.EBSResponse, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	if tenantID == "" {
		return nil, store.ErrMissingTenantID
	}
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return nil, errors.New("missing uuid")
	}
	return s.Store.GetTransactionByUUID(ctx, tenantID, uuid)
}

func (s *Service) GetUserCards(ctx context.Context, tenantID, mobile string) (*ebs_fields.User, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	if tenantID == "" {
		return nil, store.ErrMissingTenantID
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return nil, ErrMissingMobile
	}
	identity, err := s.ResolveIdentityUserByMobileInIdentityAuth(ctx, tenantID, mobile)
	if err != nil {
		return nil, err
	}
	cards, err := s.Store.ListCardsByUserID(ctx, tenantID, identity.UserID)
	if err != nil {
		return nil, err
	}
	if len(cards) == 0 {
		return nil, errors.New("no cards found")
	}
	return &ebs_fields.User{
		Mobile:   identity.Mobile,
		MainCard: cards[0].Pan,
		ExpDate:  cards[0].Expiry,
		Cards:    cards,
	}, nil
}
