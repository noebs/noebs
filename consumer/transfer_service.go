package consumer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

// CardTransfer performs a P2P card transfer and stores notification events through notification-chat.
func (s *Service) CardTransfer(ctx context.Context, tenantID string, fields ebs_fields.ConsumerCardTransferAndMobileFields) (ebs_fields.EBSParserFields, error) {
	if s == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingService
	}
	if s.Store == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingStore
	}
	if s.HTTPClient == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingHTTPClient
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return ebs_fields.EBSParserFields{}, err
	}
	if _, err := s.serviceDiscoveryEndpoint(notificationCommandTarget); err != nil {
		return ebs_fields.EBSParserFields{}, err
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

	data := PushData{
		TenantID:       tenantID,
		Type:           EBS_NOTIFICATION,
		Date:           time.Now().Unix(),
		Title:          "Card Transfer",
		CallToAction:   CTA_CARD_TRANSFER,
		EBSData:        res.EBSResponse,
		UUID:           fields.UUID,
		DeviceID:       deviceID,
		PaymentRequest: ebs_fields.QrData{},
	}

	if err != nil {
		sender := data
		sender.EBSData.PAN = fields.Pan
		sender.To = deviceID
		sender.Body = fmt.Sprintf("Card Transfer failed due to: %v.", res.ResponseMessage)
		notifyErr := s.StoreNotificationEventsInNotificationChat(ctx, tenantID, notificationEvent{name: "sender-failure", data: sender})
		return res, errors.Join(err, notifyErr)
	}

	receiver := data
	receiver.EBSData.PAN = fields.ToCard
	receiver.Body = fmt.Sprintf("You have received %v %v from %v.", fields.TranAmount, res.AccountCurrency, res.PAN)
	receiverMobile := strings.TrimSpace(fields.Mobile)
	if receiverMobile != "" {
		receiver.Phone = receiverMobile
		receiver.UserMobile = receiverMobile
	}

	sender := data
	sender.EBSData.PAN = fields.Pan
	sender.To = deviceID
	sender.Body = fmt.Sprintf("%v %v has been transferred successfully from your account to %v.", fields.TranAmount, res.AccountCurrency, res.ToCard)
	if notifyErr := s.StoreNotificationEventsInNotificationChat(
		ctx,
		tenantID,
		notificationEvent{name: "receiver", data: receiver},
		notificationEvent{name: "sender", data: sender},
	); notifyErr != nil {
		return res, notifyErr
	}

	return res, nil
}

// MobileTransfer performs a P2P transfer to a recipient resolved by MSISDN (mobile number).
func (s *Service) MobileTransfer(ctx context.Context, tenantID string, fields ebs_fields.ConsumerMobileTransferFields) (ebs_fields.EBSParserFields, error) {
	if s == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingService
	}
	if s.HTTPClient == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingHTTPClient
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return ebs_fields.EBSParserFields{}, err
	}

	receiverMobile := strings.TrimSpace(fields.Mobile)
	if receiverMobile == "" {
		return ebs_fields.EBSParserFields{}, ErrMissingMobile
	}
	if _, err := s.serviceDiscoveryEndpoint(notificationCommandTarget); err != nil {
		return ebs_fields.EBSParserFields{}, err
	}

	card, err := s.ResolveCardByMobileInCardVault(ctx, tenantID, receiverMobile)
	if err != nil {
		return ebs_fields.EBSParserFields{}, err
	}
	toCard := strings.TrimSpace(card.PAN)
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
		sender := data
		sender.EBSData.PAN = fields.Pan
		sender.To = deviceID
		sender.Body = fmt.Sprintf("Card Transfer failed due to: %v.", res.ResponseMessage)
		notifyErr := s.StoreNotificationEventsInNotificationChat(ctx, tenantID, notificationEvent{name: "sender-failure", data: sender})
		return res, errors.Join(err, notifyErr)
	}

	receiver := data
	receiver.EBSData.PAN = toCard
	receiver.Phone = receiverMobile
	receiver.UserMobile = receiverMobile
	receiver.Body = fmt.Sprintf("You have received %v %v from %v.", fields.TranAmount, res.AccountCurrency, res.PAN)

	sender := data
	sender.EBSData.PAN = fields.Pan
	sender.To = deviceID
	sender.Body = fmt.Sprintf("%v %v has been transferred successfully from your account to %v.", fields.TranAmount, res.AccountCurrency, res.ToCard)
	if notifyErr := s.StoreNotificationEventsInNotificationChat(
		ctx,
		tenantID,
		notificationEvent{name: "receiver", data: receiver},
		notificationEvent{name: "sender", data: sender},
	); notifyErr != nil {
		return res, notifyErr
	}

	return res, nil
}
