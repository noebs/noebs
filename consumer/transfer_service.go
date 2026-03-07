package consumer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

// CardTransfer performs a P2P card transfer and enqueues push notifications.
func (s *Service) CardTransfer(ctx context.Context, tenantID string, fields ebs_fields.ConsumerCardTransferAndMobileFields) (ebs_fields.EBSParserFields, error) {
	if s == nil || s.Store == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingStore
	}
	if tenantID == "" {
		return ebs_fields.EBSParserFields{}, store.ErrMissingTenantID
	}

	req := fields
	deviceID := req.DeviceID
	req.ConsumerCommonFields.DelDeviceID()

	res, err := s.callEBSJSONWithMutate(
		ctx,
		tenantID,
		s.NoebsConfig.ConsumerIP,
		ebs_fields.ConsumerCardTransferEndpoint,
		req,
		func(p *ebs_fields.EBSParserFields) {
			// Persist sender/receiver to support history queries.
			p.EBSResponse.SenderPAN = fields.Pan
			p.EBSResponse.ReceiverPAN = fields.ToCard
		},
	)

	// Push notification (async).
	data := PushData{
		TenantID:      tenantID,
		Type:          EBS_NOTIFICATION,
		Date:          time.Now().Unix(),
		Title:         "Card Transfer",
		CallToAction:  CTA_CARD_TRANSFER,
		EBSData:       res.EBSResponse,
		UUID:          fields.UUID,
		DeviceID:      deviceID,
		PaymentRequest: ebs_fields.QrData{},
	}

	if err != nil {
		// Sender notification.
		data.EBSData.PAN = fields.Pan
		data.Body = fmt.Sprintf("Card Transfer failed due to: %v.", res.ResponseMessage)
		tranData <- data
		return res, err
	}

	// Receiver notification.
	data.EBSData.PAN = fields.ToCard
	data.Body = fmt.Sprintf("You have received %v %v from %v.", fields.TranAmount, res.AccountCurrency, res.PAN)
	tranData <- data

	// Sender notification.
	data.EBSData.PAN = fields.Pan
	data.Body = fmt.Sprintf("%v %v has been transferred successfully from your account to %v.", fields.TranAmount, res.AccountCurrency, res.ToCard)
	tranData <- data

	return res, nil
}

// MobileTransfer performs a P2P transfer to a recipient resolved by MSISDN (mobile number).
func (s *Service) MobileTransfer(ctx context.Context, tenantID string, fields ebs_fields.ConsumerMobileTransferFields) (ebs_fields.EBSParserFields, error) {
	if s == nil || s.Store == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingStore
	}
	if tenantID == "" {
		return ebs_fields.EBSParserFields{}, store.ErrMissingTenantID
	}

	receiverMobile := strings.TrimSpace(fields.Mobile)
	if receiverMobile == "" {
		return ebs_fields.EBSParserFields{}, ErrMissingMobile
	}

	receiver, err := s.Store.GetUserByMobile(ctx, tenantID, receiverMobile)
	if err != nil {
		return ebs_fields.EBSParserFields{}, err
	}

	toCard := receiver.MainCard
	if toCard == "" {
		if withCards, err := s.Store.GetCardsOrFail(ctx, tenantID, receiverMobile); err == nil && len(withCards.Cards) > 0 {
			toCard = withCards.Cards[0].Pan
		}
	}
	if toCard == "" {
		return ebs_fields.EBSParserFields{}, ErrReceiverHasNoCard
	}

	req := fields
	req.ToCard = toCard
	deviceID := req.DeviceID
	req.ConsumerCommonFields.DelDeviceID()

	res, err := s.callEBSJSONWithMutate(
		ctx,
		tenantID,
		s.NoebsConfig.ConsumerIP,
		ebs_fields.ConsumerCardTransferEndpoint,
		req,
		func(p *ebs_fields.EBSParserFields) {
			p.EBSResponse.SenderPAN = fields.Pan
			p.EBSResponse.ReceiverPAN = toCard
		},
	)

	// Push notifications (async).
	data := PushData{
		TenantID:     tenantID,
		Type:         EBS_NOTIFICATION,
		Date:         time.Now().Unix(),
		Title:        "Card Transfer",
		CallToAction: CTA_CARD_TRANSFER,
		EBSData:      res.EBSResponse,
		UUID:         fields.UUID,
		DeviceID:     deviceID,
	}

	if err != nil {
		// Sender notification.
		data.EBSData.PAN = fields.Pan
		data.Body = fmt.Sprintf("Card Transfer failed due to: %v.", res.ResponseMessage)
		tranData <- data
		return res, err
	}

	// Receiver notification.
	data.EBSData.PAN = toCard
	data.Body = fmt.Sprintf("You have received %v %v from %v.", fields.TranAmount, res.AccountCurrency, res.PAN)
	tranData <- data

	// Sender notification.
	data.EBSData.PAN = fields.Pan
	data.Body = fmt.Sprintf("%v %v has been transferred successfully from your account to %v.", fields.TranAmount, res.AccountCurrency, res.ToCard)
	tranData <- data

	return res, nil
}
