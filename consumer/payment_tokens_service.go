package consumer

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/adonese/noebs/utils"
	"github.com/google/uuid"
)

var ErrMissingUUID = errors.New("missing uuid")

const paymentTokenFinalizationTimeout = 5 * time.Second

func (s *Service) GeneratePaymentTokenForUserID(ctx context.Context, tenantID string, userID int64, token ebs_fields.Token) (ebs_fields.Token, string, string, error) {
	if s == nil || s.Store == nil {
		return ebs_fields.Token{}, "", "", ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return ebs_fields.Token{}, "", "", err
	}
	if userID <= 0 {
		return ebs_fields.Token{}, "", "", store.ErrInvalidUserID
	}
	if token.Amount < 0 {
		return ebs_fields.Token{}, "", "", store.ErrInvalidAmount
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
	token.IsPaid = false
	token.PaymentStatus = ebs_fields.PaymentTokenStatusAvailable
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

func (s *Service) GetPaymentTokenForUserID(ctx context.Context, tenantID string, userID int64, uuid string) ([]ebs_fields.Token, *ebs_fields.Token, error) {
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
	result.ToCard = utils.MaskPAN(result.ToCard)
	return nil, result, nil
}

// NoebsQuickPayment executes a quick payment through EBS after resolving token
// state from the card-vault service.
func (s *Service) NoebsQuickPayment(ctx context.Context, tenantID string, userID int64, req ebs_fields.QuickPaymentFields, uuidQuery, tokenQuery string) (ebs_fields.EBSParserFields, error) {
	if s == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingService
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return ebs_fields.EBSParserFields{}, err
	}
	if userID <= 0 {
		return ebs_fields.EBSParserFields{}, store.ErrInvalidUserID
	}
	if err := validateQuickPaymentRequest(req); err != nil {
		return ebs_fields.EBSParserFields{}, err
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
	req.ConsumerCommonFields.UUID = resolution.RailUUID
	req.ToCard = resolution.ToCard
	req.TranAmount = float32(resolution.Amount)
	req.DynamicFees = s.NoebsConfig.EBSDynamicFees.CardTransferfees

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
	status := quickPaymentTerminalStatus(res, err, resolution.RailUUID)
	if status == "" {
		return res, errors.Join(err, ErrPaymentOutcomeUnknown)
	}
	finalizeCtx, cancelFinalize := context.WithTimeout(context.WithoutCancel(ctx), paymentTokenFinalizationTimeout)
	defer cancelFinalize()
	finalizeErr := s.FinalizeQuickPaymentTokenInCardVault(finalizeCtx, tenantID, userID, QuickPaymentTokenFinalizationCommand{
		UUID:     resolution.UUID,
		RailUUID: resolution.RailUUID,
		Status:   status,
	})

	hookErr := s.SubmitBillerHookInNotificationChat(ctx, tenantID, BillerHookCommand{
		EBS:          res.EBSResponse,
		IsSuccessful: status == ebs_fields.PaymentTokenStatusPaid,
		Token:        resolution.UUID,
	})

	return res, errors.Join(err, finalizeErr, hookErr)
}

func validateQuickPaymentRequest(req ebs_fields.QuickPaymentFields) error {
	for _, value := range []string{req.TranDateTime, req.Pan, req.Ipin, req.ExpDate} {
		if strings.TrimSpace(value) == "" {
			return ErrInvalidQuickPaymentRequest
		}
	}
	return nil
}

func quickPaymentTerminalStatus(res ebs_fields.EBSParserFields, err error, railUUID string) string {
	if strings.TrimSpace(railUUID) == "" || strings.TrimSpace(res.UUID) != strings.TrimSpace(railUUID) || strings.TrimSpace(res.ResponseMessage) == "" {
		return ""
	}
	var callErr *ebs_fields.CallError
	if !errors.As(err, &callErr) && res.ResponseCode == 0 {
		return ebs_fields.PaymentTokenStatusPaid
	}
	if callErr != nil && callErr.Status == http.StatusBadGateway && res.ResponseCode != 0 {
		return ebs_fields.PaymentTokenStatusFailed
	}
	return ""
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
