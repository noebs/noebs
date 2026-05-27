package consumer

import (
	"context"
	"errors"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/adonese/noebs/utils"
	"github.com/google/uuid"
)

var ErrMissingUUID = errors.New("missing uuid")

// GeneratePaymentToken creates a payment token for the authorized user (mobile).
//
// NOTE: the returned encoded token intentionally masks the destination PAN to avoid exposing card data.
func (s *Service) GeneratePaymentToken(ctx context.Context, tenantID, mobile string, token ebs_fields.Token) (ebs_fields.Token, string, string, error) {
	if s == nil || s.Store == nil {
		return ebs_fields.Token{}, "", "", ErrMissingStore
	}
	if tenantID == "" {
		return ebs_fields.Token{}, "", "", store.ErrMissingTenantID
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ebs_fields.Token{}, "", "", ErrMissingMobile
	}

	user, err := s.Store.GetCardsOrFail(ctx, tenantID, mobile)
	if err != nil {
		return ebs_fields.Token{}, "", "", err
	}

	return s.generatePaymentTokenForCards(ctx, tenantID, user.ID, user.Cards, token)
}

func (s *Service) GeneratePaymentTokenForUserID(ctx context.Context, tenantID string, userID int64, token ebs_fields.Token) (ebs_fields.Token, string, string, error) {
	if s == nil || s.Store == nil {
		return ebs_fields.Token{}, "", "", ErrMissingStore
	}
	if tenantID == "" {
		return ebs_fields.Token{}, "", "", store.ErrMissingTenantID
	}
	if userID <= 0 {
		return ebs_fields.Token{}, "", "", store.ErrInvalidUserID
	}
	cards, err := s.Store.ListCardsByUserID(ctx, tenantID, userID)
	if err != nil {
		return ebs_fields.Token{}, "", "", err
	}
	return s.generatePaymentTokenForCards(ctx, tenantID, userID, cards, token)
}

func (s *Service) generatePaymentTokenForCards(ctx context.Context, tenantID string, userID int64, cards []ebs_fields.Card, token ebs_fields.Token) (ebs_fields.Token, string, string, error) {
	fullPan := ""
	if token.ToCard == "" {
		if len(cards) == 0 {
			return ebs_fields.Token{}, "", "", errors.New("no card found")
		}
		fullPan = cards[0].Pan
	} else {
		pan, err := ebs_fields.ExpandCard(token.ToCard, cards)
		if err != nil {
			return ebs_fields.Token{}, "", "", err
		}
		fullPan = pan
	}

	token.ToCard = fullPan
	token.UUID = uuid.New().String()
	token.UserID = userID
	if err := s.Store.CreateToken(ctx, tenantID, &token); err != nil {
		return ebs_fields.Token{}, "", "", err
	}

	// Mask the PAN in the encoded token for safety.
	safe := token
	safe.ToCard = utils.MaskPAN(token.ToCard)
	encoded, _ := ebs_fields.Encode(&safe)
	paymentLink := s.NoebsConfig.PaymentLinkBase + token.UUID
	return safe, encoded, paymentLink, nil
}

type PaymentRequestData struct {
	ToCard string `json:"toCard,omitempty"`
	Amount int    `json:"amount,omitempty"`
}

func (s *Service) PaymentRequestForUserID(ctx context.Context, tenantID string, userID int64, data PaymentRequestData) (ebs_fields.Token, string, string, error) {
	return s.GeneratePaymentTokenForUserID(ctx, tenantID, userID, ebs_fields.Token{
		ToCard: data.ToCard,
		Amount: data.Amount,
	})
}

// GetPaymentToken returns either a single token (when uuid != "") or all tokens for the given user.
func (s *Service) GetPaymentToken(ctx context.Context, tenantID, mobile, uuid string) ([]ebs_fields.Token, *ebs_fields.Token, error) {
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

	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return nil, nil, err
	}

	if strings.TrimSpace(uuid) == "" {
		tokens, err := s.Store.GetAllTokensByUserID(ctx, tenantID, user.ID)
		if err != nil {
			return nil, nil, err
		}
		for i := range tokens {
			tokens[i].ToCard = utils.MaskPAN(tokens[i].ToCard)
		}
		return tokens, nil, nil
	}

	result, err := s.Store.GetTokenByUUID(ctx, tenantID, uuid)
	if err != nil {
		return nil, nil, err
	}
	result.ToCard = utils.MaskPAN(result.ToCard)
	return nil, result, nil
}

func (s *Service) GetPaymentTokenForUserID(ctx context.Context, tenantID string, userID int64, uuid string) ([]ebs_fields.Token, *ebs_fields.Token, error) {
	if s == nil || s.Store == nil {
		return nil, nil, ErrMissingStore
	}
	if tenantID == "" {
		return nil, nil, store.ErrMissingTenantID
	}
	if userID <= 0 {
		return nil, nil, store.ErrInvalidUserID
	}

	if strings.TrimSpace(uuid) == "" {
		tokens, err := s.Store.GetAllTokensByUserID(ctx, tenantID, userID)
		if err != nil {
			return nil, nil, err
		}
		for i := range tokens {
			tokens[i].ToCard = utils.MaskPAN(tokens[i].ToCard)
		}
		return tokens, nil, nil
	}

	result, err := s.Store.GetTokenByUUID(ctx, tenantID, uuid)
	if err != nil {
		return nil, nil, err
	}
	if result.UserID != userID {
		return nil, nil, store.ErrInvalidUserID
	}
	result.ToCard = utils.MaskPAN(result.ToCard)
	return nil, result, nil
}

// NoebsQuickPayment executes a quick payment through EBS after resolving token
// state from the card-vault service.
func (s *Service) NoebsQuickPayment(ctx context.Context, tenantID string, userID int64, req ebs_fields.QuickPaymentFields, uuidQuery, tokenQuery string) (ebs_fields.EBSParserFields, error) {
	if s == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingService
	}
	if tenantID == "" {
		return ebs_fields.EBSParserFields{}, store.ErrMissingTenantID
	}
	if userID <= 0 {
		return ebs_fields.EBSParserFields{}, store.ErrInvalidUserID
	}
	resolution, err := s.ResolveQuickPaymentTokenFromCardVault(ctx, tenantID, userID, QuickPaymentTokenResolveCommand{
		BodyToken:  req.EncodedPaymentToken,
		QueryToken: tokenQuery,
		UUID:       uuidQuery,
		Amount:     int(req.TranAmount),
	})
	if err != nil {
		return ebs_fields.EBSParserFields{}, err
	}

	// Force the destination PAN + amount from the stored token.
	req.ApplicationId = s.NoebsConfig.ConsumerID
	req.ToCard = resolution.ToCard
	req.TranAmount = float32(resolution.Amount)

	senderPan := req.Pan
	receiverPan := resolution.ToCard
	payload := req.MarshallP2pFields()

	res, err := s.callEBSRawWithMutate(
		ctx,
		tenantID,
		s.NoebsConfig.ConsumerIP,
		ebs_fields.ConsumerCardTransferEndpoint,
		payload,
		func(p *ebs_fields.EBSParserFields) {
			p.EBSResponse.SenderPAN = senderPan
			p.EBSResponse.ReceiverPAN = receiverPan
		},
	)
	if err == nil {
		if markErr := s.MarkQuickPaymentTokenPaidInCardVault(ctx, tenantID, userID, QuickPaymentTokenPaidCommand{UUID: resolution.UUID}); markErr != nil {
			return res, markErr
		}
	}

	hookErr := s.SubmitBillerHookInNotificationChat(ctx, tenantID, BillerHookCommand{EBS: res.EBSResponse, IsSuccessful: err == nil, Token: resolution.UUID})
	if hookErr != nil {
		return res, errors.Join(err, hookErr)
	}

	return res, err
}

func quickPaymentTokenUUID(req ebs_fields.QuickPaymentFields, uuidQuery, tokenQuery string) (string, error) {
	bodyToken := strings.TrimSpace(req.EncodedPaymentToken)
	queryToken := strings.TrimSpace(tokenQuery)
	queryUUID := strings.TrimSpace(uuidQuery)

	count := 0
	for _, value := range []string{bodyToken, queryToken, queryUUID} {
		if value != "" {
			count++
		}
	}
	if count == 0 {
		return "", ErrMissingUUID
	}
	if count > 1 {
		return "", ErrAmbiguousPaymentToken
	}

	if queryUUID != "" {
		return queryUUID, nil
	}

	encoded := bodyToken
	if encoded == "" {
		encoded = queryToken
	}
	token, err := ebs_fields.Decode(encoded)
	if err != nil {
		return "", ErrInvalidPaymentToken
	}
	tokenUUID := strings.TrimSpace(token.UUID)
	if tokenUUID == "" {
		return "", ErrInvalidPaymentToken
	}
	return tokenUUID, nil
}
