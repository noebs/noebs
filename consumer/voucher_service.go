package consumer

import (
	"context"
	"fmt"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

// GenerateVoucher generates a voucher via EBS and enqueues a push notification.
func (s *Service) GenerateVoucher(ctx context.Context, tenantID string, fields ebs_fields.ConsumerGenerateVoucherFields) (ebs_fields.EBSParserFields, error) {
	if s == nil || s.Store == nil {
		return ebs_fields.EBSParserFields{}, ErrMissingStore
	}
	if tenantID == "" {
		return ebs_fields.EBSParserFields{}, store.ErrMissingTenantID
	}

	req := fields
	deviceID := req.DeviceID
	req.ConsumerCommonFields.DelDeviceID()

	res, err := s.callEBSJSON(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerGenerateVoucher, req)

	// Push notification (async).
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

	if err != nil {
		data.Body = fmt.Sprintf("Voucher generation failed due to: %v.", res.ResponseMessage)
		tranData <- data
		return res, err
	}

	data.Body = fmt.Sprintf("Voucher number generated for phone %v is %v", fields.VoucherNumber, res.VoucherCode)
	tranData <- data
	return res, nil
}
