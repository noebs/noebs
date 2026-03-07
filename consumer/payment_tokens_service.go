package consumer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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

	fullPan := ""
	if token.ToCard == "" {
		if len(user.Cards) == 0 {
			return ebs_fields.Token{}, "", "", errors.New("no card found")
		}
		fullPan = user.Cards[0].Pan
	} else {
		pan, err := ebs_fields.ExpandCard(token.ToCard, user.Cards)
		if err != nil {
			return ebs_fields.Token{}, "", "", err
		}
		fullPan = pan
	}

	token.ToCard = fullPan
	token.UUID = uuid.New().String()
	token.UserID = user.ID
	token.User = *user
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
	Mobile string `json:"mobile,omitempty"`
	ToCard string `json:"toCard,omitempty"`
	Amount int    `json:"amount,omitempty"`
}

func (s *Service) PaymentRequest(ctx context.Context, tenantID, senderMobile string, data PaymentRequestData) (ebs_fields.Token, string, string, error) {
	if s == nil || s.Store == nil {
		return ebs_fields.Token{}, "", "", ErrMissingStore
	}
	if tenantID == "" {
		return ebs_fields.Token{}, "", "", store.ErrMissingTenantID
	}
	senderMobile = strings.TrimSpace(senderMobile)
	if senderMobile == "" {
		return ebs_fields.Token{}, "", "", ErrMissingMobile
	}
	data.Mobile = strings.TrimSpace(data.Mobile)
	if data.Mobile == "" {
		return ebs_fields.Token{}, "", "", ErrMissingMobile
	}

	sender, err := s.Store.GetCardsOrFail(ctx, tenantID, senderMobile)
	if err != nil {
		return ebs_fields.Token{}, "", "", err
	}
	receiver, err := s.Store.GetUserByMobile(ctx, tenantID, data.Mobile)
	if err != nil {
		return ebs_fields.Token{}, "", "", err
	}

	fullPan := ""
	if data.ToCard == "" {
		if len(sender.Cards) == 0 {
			return ebs_fields.Token{}, "", "", errors.New("no card found")
		}
		fullPan = sender.Cards[0].Pan
	} else {
		pan, err := ebs_fields.ExpandCard(data.ToCard, sender.Cards)
		if err != nil {
			return ebs_fields.Token{}, "", "", err
		}
		fullPan = pan
	}

	token := ebs_fields.Token{
		ToCard: fullPan,
		Amount: data.Amount,
		UUID:   uuid.New().String(),
		UserID: sender.ID,
		User:   *sender,
	}
	if err := s.Store.CreateToken(ctx, tenantID, &token); err != nil {
		return ebs_fields.Token{}, "", "", err
	}

	safe := token
	safe.ToCard = utils.MaskPAN(token.ToCard)
	encoded, _ := ebs_fields.Encode(&safe)
	paymentLink := s.NoebsConfig.PaymentLinkBase + token.UUID

	name := sender.Fullname
	if name == "" {
		name = sender.Mobile
	}

	// Push notification to the receiver (async).
	pData := PushData{
		TenantID:     tenantID,
		Type:         NOEBS_NOTIFICATION,
		Date:         time.Now().Unix(),
		CallToAction: CTA_REQUEST_FUNDS,
		UUID:         token.UUID,
		DeviceID:     receiver.DeviceID,
		Title:        "Payment Request",
		Body:         fmt.Sprintf("%v has requested %v SDG from you.", name, token.Amount),
		Phone:        data.Mobile,
		UserMobile:   data.Mobile,
		PaymentRequest: ebs_fields.QrData{
			UUID:   token.UUID,
			ToCard: safe.ToCard,
			Amount: token.Amount,
		},
	}
	tranData <- pData

	return safe, encoded, paymentLink, nil
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

// NoebsQuickPayment performs a payment using a stored payment token UUID (or an encoded token).
func (s *Service) NoebsQuickPayment(ctx context.Context, tenantID string, req ebs_fields.QuickPaymentFields, uuidQuery, tokenQuery string) (ebs_fields.EBSParserFields, error) {
	if s == nil || s.Store == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingStore
	}
	if tenantID == "" {
		return ebs_fields.EBSParserFields{}, store.ErrMissingTenantID
	}

	var noebsToken ebs_fields.Token
	if strings.TrimSpace(req.EncodedPaymentToken) != "" {
		if t, err := ebs_fields.Decode(req.EncodedPaymentToken); err == nil {
			noebsToken = t
		}
	}
	if noebsToken.UUID == "" && strings.TrimSpace(tokenQuery) != "" {
		if t, err := ebs_fields.Decode(tokenQuery); err == nil {
			noebsToken = t
		}
	}
	if noebsToken.UUID == "" && strings.TrimSpace(uuidQuery) != "" {
		if t, err := s.Store.GetTokenByUUID(ctx, tenantID, uuidQuery); err == nil && t != nil {
			noebsToken = *t
		}
	}
	if noebsToken.UUID == "" {
		return ebs_fields.EBSParserFields{}, ErrMissingUUID
	}

	storedToken, err := s.Store.GetTokenByUUID(ctx, tenantID, noebsToken.UUID)
	if err != nil {
		return ebs_fields.EBSParserFields{}, err
	}

	if storedToken.Amount != 0 && int(req.TranAmount) != storedToken.Amount {
		return ebs_fields.EBSParserFields{}, ErrAmountMismatch
	}

	// Force the destination PAN + amount from the stored token.
	req.ApplicationId = s.NoebsConfig.ConsumerID
	req.ToCard = storedToken.ToCard
	req.TranAmount = float32(storedToken.Amount)

	senderPan := req.Pan
	receiverPan := storedToken.ToCard
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
		_ = s.Store.MarkTokenPaid(ctx, tenantID, storedToken.UUID)
	}

	// Notify external biller hooks (async).
	billerChan <- billerForm{EBS: res.EBSResponse, IsSuccessful: err == nil, Token: storedToken.UUID}

	return res, err
}
