package consumer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

// GenerateVoucher generates a voucher via EBS and stores the notification through notification-chat.
func (s *Service) GenerateVoucher(ctx context.Context, tenantID string, fields ebs_fields.ConsumerGenerateVoucherFields) (ebs_fields.EBSParserFields, error) {
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

	res, err := s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerGenerateVoucher, req)

	data := PushData{
		TenantID:     tenantID,
		Type:         EBS_NOTIFICATION,
		Date:         time.Now().Unix(),
		Title:        "Voucher Generation",
		CallToAction: CTA_VOUCHER,
		EBSData:      res.EBSResponse,
		UUID:         fields.UUID,
		DeviceID:     deviceID,
	}
	data.EBSData.PAN = fields.Pan
	data.To = deviceID

	if err != nil {
		data.Body = fmt.Sprintf("Voucher generation failed due to: %v.", res.ResponseMessage)
		notifyErr := s.StoreNotificationEventsInNotificationChat(ctx, tenantID, notificationEvent{name: "sender-failure", data: data})
		return res, errors.Join(err, notifyErr)
	}

	data.Body = fmt.Sprintf("Voucher number generated for phone %v is %v", fields.VoucherNumber, res.VoucherCode)
	if notifyErr := s.StoreNotificationEventsInNotificationChat(ctx, tenantID, notificationEvent{name: "sender", data: data}); notifyErr != nil {
		return res, notifyErr
	}
	return res, nil
}
