package consumer

import (
	"context"
	"errors"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

type CheckUserResult struct {
	Phone  string `json:"phone"`
	IsUser bool   `json:"is_user"`
	Pan    string `json:"PAN"`
}

func (s *Service) CheckUser(ctx context.Context, tenantID string, phones []string) ([]CheckUserResult, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	phones, err = normalizeCheckUserPhones(phones)
	if err != nil {
		return nil, err
	}
	if s.HTTPClient == nil {
		return nil, ErrMissingHTTPClient
	}

	out := make([]CheckUserResult, 0, len(phones))
	for _, phone := range phones {
		user, err := s.Store.GetUserByMobile(ctx, tenantID, phone)
		if err != nil {
			if !store.ErrNotFound(err) {
				return nil, err
			}
			out = append(out, CheckUserResult{Phone: phone, IsUser: false})
			continue
		}

		maskedCard, err := s.ResolveMaskedCardByMobileInCardVault(ctx, tenantID, user.Mobile)
		if errors.Is(err, ErrReceiverHasNoCard) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, CheckUserResult{Phone: phone, IsUser: true, Pan: maskedCard.MaskedPAN})
	}
	return out, nil
}

func normalizeCheckUserPhones(phones []string) ([]string, error) {
	if len(phones) == 0 {
		return nil, ErrMissingMobile
	}
	normalized := make([]string, 0, len(phones))
	for _, phone := range phones {
		phone = strings.TrimSpace(phone)
		if phone == "" {
			return nil, ErrMissingMobile
		}
		normalized = append(normalized, phone)
	}
	return normalized, nil
}

func (s *Service) SetMainCardForUserID(ctx context.Context, tenantID string, userID int64, pan string) error {
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
	pan = strings.TrimSpace(pan)
	if pan == "" {
		return store.ErrMissingPAN
	}
	if ok, err := s.Store.CardExists(ctx, tenantID, pan); err != nil || !ok {
		if err != nil {
			return err
		}
		return ErrCardNotFound
	}
	return s.Store.SetMainCard(ctx, tenantID, userID, pan)
}

func (s *Service) GetTransactionsForUserID(ctx context.Context, tenantID string, userID int64) ([]ebs_fields.EBSResponse, error) {
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

	cards, err := s.ListMaskedCardsInCardVault(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}

	var trans []ebs_fields.EBSResponse
	for _, masked := range cards.MaskedPANs {
		cardTrans, err := s.Store.GetTransactionsByMaskedPan(ctx, tenantID, masked)
		if err != nil {
			return nil, err
		}
		trans = append(trans, cardTrans...)
	}
	return trans, nil
}
