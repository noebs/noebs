package consumer

import (
	"context"
	"strings"

	"github.com/adonese/noebs/store"
)

type CardByMobileCommand struct {
	Mobile string `json:"mobile"`
}

type CardByMobileResult struct {
	PAN     string `json:"pan"`
	ExpDate string `json:"expDate,omitempty"`
}

func (s *Service) ResolveCardByMobile(ctx context.Context, tenantID string, cmd CardByMobileCommand) (CardByMobileResult, error) {
	if s == nil || s.Store == nil {
		return CardByMobileResult{}, ErrMissingStore
	}
	if tenantID == "" {
		return CardByMobileResult{}, store.ErrMissingTenantID
	}
	mobile := strings.TrimSpace(cmd.Mobile)
	if mobile == "" {
		return CardByMobileResult{}, ErrMissingMobile
	}
	cards, err := s.Store.ListCardsByMobile(ctx, tenantID, mobile)
	if err != nil {
		return CardByMobileResult{}, err
	}
	if len(cards) == 0 {
		return CardByMobileResult{}, ErrReceiverHasNoCard
	}
	pan := strings.TrimSpace(cards[0].Pan)
	if pan == "" {
		return CardByMobileResult{}, ErrReceiverHasNoCard
	}
	return CardByMobileResult{PAN: pan, ExpDate: strings.TrimSpace(cards[0].Expiry)}, nil
}

func (s *Service) ResolveCardByMobileInCardVault(ctx context.Context, tenantID, mobile string) (CardByMobileResult, error) {
	var result CardByMobileResult
	if err := s.doAdminServiceCommand(ctx, tenantID, cardVaultCommandTarget, "/internal/card-vault/cards/by-mobile", CardByMobileCommand{Mobile: mobile}, &result); err != nil {
		return CardByMobileResult{}, err
	}
	if strings.TrimSpace(result.PAN) == "" {
		return CardByMobileResult{}, ErrReceiverHasNoCard
	}
	return result, nil
}
