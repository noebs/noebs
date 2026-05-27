package consumer

import (
	"context"
	"errors"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/adonese/noebs/utils"
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
	if tenantID == "" {
		return nil, store.ErrMissingTenantID
	}
	if len(phones) == 0 {
		return nil, errors.New("empty phones")
	}

	out := make([]CheckUserResult, 0, len(phones))
	for _, phone := range phones {
		phone = strings.TrimSpace(phone)
		if phone == "" {
			continue
		}
		user, err := s.Store.GetUserByMobile(ctx, tenantID, phone)
		if err != nil {
			out = append(out, CheckUserResult{Phone: phone, IsUser: false})
			continue
		}

		pan := user.MainCard
		if pan == "" {
			userCards, err := s.Store.GetCardsOrFail(ctx, tenantID, phone)
			if err == nil && len(userCards.Cards) > 0 {
				pan = userCards.Cards[0].Pan
			} else {
				// Keep legacy behavior: skip users without cards.
				continue
			}
		}

		masked := ""
		if pan != "" {
			masked = utils.MaskPAN(pan)
		}
		out = append(out, CheckUserResult{Phone: phone, IsUser: true, Pan: masked})
	}
	return out, nil
}

func (s *Service) SetMainCard(ctx context.Context, tenantID, mobile, pan string) error {
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
	pan = strings.TrimSpace(pan)
	if pan == "" {
		return errors.New("missing pan")
	}

	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return err
	}
	if ok, err := s.Store.CardExists(ctx, tenantID, pan); err != nil || !ok {
		if err != nil {
			return err
		}
		return errors.New("card does not exist")
	}
	if err := s.Store.SetMainCard(ctx, tenantID, user.ID, pan); err != nil {
		return err
	}
	return s.Store.UpdateUserColumns(ctx, tenantID, user.ID, map[string]any{"main_card": pan})
}

func (s *Service) SetMainCardForUserID(ctx context.Context, tenantID string, userID int64, pan string) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	if userID <= 0 {
		return store.ErrInvalidUserID
	}
	pan = strings.TrimSpace(pan)
	if pan == "" {
		return errors.New("missing pan")
	}
	if ok, err := s.Store.CardExists(ctx, tenantID, pan); err != nil || !ok {
		if err != nil {
			return err
		}
		return errors.New("card does not exist")
	}
	return s.Store.SetMainCard(ctx, tenantID, userID, pan)
}

func (s *Service) GetTransactions(ctx context.Context, tenantID, mobile string) ([]ebs_fields.EBSResponse, error) {
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

	user, err := s.Store.GetCardsOrFail(ctx, tenantID, mobile)
	if err != nil {
		return nil, err
	}

	var trans []ebs_fields.EBSResponse
	for _, card := range user.Cards {
		masked := utils.MaskPAN(card.Pan)
		cardTrans, err := s.Store.GetTransactionsByMaskedPan(ctx, tenantID, masked)
		if err == nil {
			trans = append(trans, cardTrans...)
		}
	}
	return trans, nil
}
