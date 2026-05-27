package consumer

import (
	"context"
	"strings"

	"github.com/adonese/noebs/store"
	"github.com/adonese/noebs/utils"
)

type CardByMobileCommand struct {
	Mobile string `json:"mobile"`
}

type CardByMobileResult struct {
	PAN     string `json:"pan"`
	ExpDate string `json:"expDate,omitempty"`
}

type MaskedCardsCommand struct{}

type MaskedCardsResult struct {
	MaskedPANs []string `json:"masked_pans"`
}

type MaskedCardByMobileCommand struct {
	Mobile string `json:"mobile"`
}

type MaskedCardByMobileResult struct {
	MaskedPAN string `json:"masked_pan"`
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

func (s *Service) ListMaskedCardsForUserID(ctx context.Context, tenantID string, userID int64, _ MaskedCardsCommand) (MaskedCardsResult, error) {
	if s == nil || s.Store == nil {
		return MaskedCardsResult{}, ErrMissingStore
	}
	if tenantID == "" {
		return MaskedCardsResult{}, store.ErrMissingTenantID
	}
	if userID <= 0 {
		return MaskedCardsResult{}, store.ErrInvalidUserID
	}
	cards, err := s.Store.ListCardsByUserID(ctx, tenantID, userID)
	if err != nil {
		return MaskedCardsResult{}, err
	}
	result := MaskedCardsResult{MaskedPANs: make([]string, 0, len(cards))}
	for _, card := range cards {
		pan := strings.TrimSpace(card.Pan)
		if pan == "" {
			return MaskedCardsResult{}, store.ErrMissingPAN
		}
		result.MaskedPANs = append(result.MaskedPANs, utils.MaskPAN(pan))
	}
	return result, nil
}

func (s *Service) ListMaskedCardsInCardVault(ctx context.Context, tenantID string, userID int64) (MaskedCardsResult, error) {
	var result MaskedCardsResult
	if err := s.doCardVaultCommand(ctx, tenantID, userID, "/internal/card-vault/cards/masked", MaskedCardsCommand{}, &result); err != nil {
		return MaskedCardsResult{}, err
	}
	return result, nil
}

func (s *Service) ResolveMaskedCardByMobile(ctx context.Context, tenantID string, cmd MaskedCardByMobileCommand) (MaskedCardByMobileResult, error) {
	if s == nil || s.Store == nil {
		return MaskedCardByMobileResult{}, ErrMissingStore
	}
	if tenantID == "" {
		return MaskedCardByMobileResult{}, store.ErrMissingTenantID
	}
	mobile := strings.TrimSpace(cmd.Mobile)
	if mobile == "" {
		return MaskedCardByMobileResult{}, ErrMissingMobile
	}
	cards, err := s.Store.ListCardsByMobile(ctx, tenantID, mobile)
	if err != nil {
		return MaskedCardByMobileResult{}, err
	}
	if len(cards) == 0 {
		return MaskedCardByMobileResult{}, ErrReceiverHasNoCard
	}
	pan := strings.TrimSpace(cards[0].Pan)
	if pan == "" {
		return MaskedCardByMobileResult{}, store.ErrMissingPAN
	}
	return MaskedCardByMobileResult{MaskedPAN: utils.MaskPAN(pan)}, nil
}

func (s *Service) ResolveMaskedCardByMobileInCardVault(ctx context.Context, tenantID, mobile string) (MaskedCardByMobileResult, error) {
	var result MaskedCardByMobileResult
	if err := s.doAdminServiceCommand(ctx, tenantID, cardVaultCommandTarget, "/internal/card-vault/cards/masked-by-mobile", MaskedCardByMobileCommand{Mobile: mobile}, &result); err != nil {
		return MaskedCardByMobileResult{}, err
	}
	if strings.TrimSpace(result.MaskedPAN) == "" {
		return MaskedCardByMobileResult{}, ErrReceiverHasNoCard
	}
	return result, nil
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
