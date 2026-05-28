package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/google/uuid"
	"github.com/noebs/ipin"
)

func (s *Service) BillPayment(ctx context.Context, tenantID string, fields ebs_fields.ConsumerBillPaymentFields) (ebs_fields.EBSParserFields, error) {
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
	if _, err := s.serviceDiscoveryEndpoint(notificationCommandTarget); err != nil {
		return ebs_fields.EBSParserFields{}, err
	}

	req := fields
	deviceID := req.DeviceID
	req.ConsumerCommonFields.DelDeviceID()

	payload, err := json.Marshal(req)
	if err != nil {
		return ebs_fields.EBSParserFields{}, err
	}

	res, err := s.callEBSRawWithMutate(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerBillPaymentEndpoint, payload, func(p *ebs_fields.EBSParserFields) {
		// Add BillType, BillTo and BillInfo2 so that the client can show these fields in transactions history.
		p.EBSResponse.BillTo = p.PaymentInfo
		if d, err := json.Marshal(p.BillInfo); err == nil {
			p.EBSResponse.BillInfo2 = string(d)
		}
		switch p.PayeeID {
		case "0010010001", "0010010003", "0010010005":
			p.EBSResponse.BillType = "Telecom TopUp"
		case "0010010002", "0010010004", "0010010006":
			p.EBSResponse.BillType = "Telecom Bill Payment"
		case "0010030002", "0010030004":
			p.EBSResponse.BillType = "Education"
		case "0010030003":
			p.EBSResponse.BillType = "Customs"
		case "0010050001":
			p.EBSResponse.BillType = "Government E-15"
		case "0010020001":
			p.EBSResponse.BillType = "Electricity"
		}
	})

	data := PushData{
		TenantID:     tenantID,
		Type:         EBS_NOTIFICATION,
		Date:         time.Now().Unix(),
		CallToAction: CTA_BILL_PAYMENT,
		EBSData:      res.EBSResponse,
		UUID:         fields.UUID,
		DeviceID:     deviceID,
	}

	if err != nil {
		data.Title = "Payment Failure"
		data.EBSData.PAN = fields.Pan // Use the unmasked PAN for internal lookup.
		data.To = deviceID
		data.Body = fmt.Sprintf("Payment failed due to: %v.", res.ResponseMessage)
		notifyErr := s.StoreNotificationEventsInNotificationChat(ctx, tenantID, notificationEvent{name: "sender-failure", data: data})
		return res, errors.Join(err, notifyErr)
	}

	data.Title = "Payment Success"
	data.EBSData.PAN = fields.Pan // Use the unmasked PAN for internal lookup.
	data.To = deviceID
	events := []notificationEvent{}

	switch res.PayeeID {
	case "0010010001", "0010010002", "0010010003", "0010010004", "0010010005", "0010010006": // telecom
		phone, err := telecomPaymentPhone(res.PaymentInfo)
		if err != nil {
			return res, err
		}
		recipient := data
		recipient.Phone = phone
		recipient.UserMobile = phone
		recipient.Body = fmt.Sprintf("You have received %v %v on your phone: %v.", res.TranAmount, res.AccountCurrency, phone)
		events = append(events, notificationEvent{name: "recipient", data: recipient})
		data.Body = fmt.Sprintf("You have sent %v %v to phone: %v successfully.", res.TranAmount, res.AccountCurrency, phone)
	case "0010030002": // mohe
		data.Body = fmt.Sprintf("%v %v has been paid successfully for Education.", res.TranAmount, res.AccountCurrency)
	case "0010030004": // mohe arab
		phone, err := moheArabicPaymentPhone(res.PaymentInfo)
		if err != nil {
			return res, err
		}
		recipient := data
		recipient.Phone = phone
		recipient.UserMobile = phone
		recipient.Body = fmt.Sprintf("%v %v has been paid successfully for Education.", res.TranAmount, res.AccountCurrency)
		events = append(events, notificationEvent{name: "recipient", data: recipient})
		data.Body = fmt.Sprintf("%v %v has been paid successfully for Education.", res.TranAmount, res.AccountCurrency)
	case "0010030003": // Customs
		data.Body = fmt.Sprintf("%v %v has been paid successfully for Customs.", res.TranAmount, res.AccountCurrency)
	case "0010050001": // e-15
		data.Body = fmt.Sprintf("%v %v has been paid successfully for E-15.", res.TranAmount, res.AccountCurrency)
	case "0010020001": // electricity
		meter := ""
		if len(res.PaymentInfo) > 6 {
			meter = res.PaymentInfo[6:]
		}
		data.Body = fmt.Sprintf("%v %v has been paid successfully for Electricity Meter No. %v", res.TranAmount, res.AccountCurrency, meter)
	}

	events = append(events, notificationEvent{name: "sender", data: data})
	if notifyErr := s.StoreNotificationEventsInNotificationChat(ctx, tenantID, events...); notifyErr != nil {
		return res, notifyErr
	}
	return res, nil
}

func telecomPaymentPhone(paymentInfo string) (string, error) {
	paymentInfo = strings.TrimSpace(paymentInfo)
	const prefix = "MPHONE="
	if !strings.HasPrefix(paymentInfo, prefix) {
		return "", fmt.Errorf("%w: telecom paymentInfo", ErrInvalidPaymentInfo)
	}
	phone := strings.TrimSpace(strings.TrimPrefix(paymentInfo, prefix))
	if phone == "" {
		return "", fmt.Errorf("%w: telecom paymentInfo", ErrInvalidPaymentInfo)
	}
	return "0" + phone, nil
}

func moheArabicPaymentPhone(paymentInfo string) (string, error) {
	parts := strings.Split(paymentInfo, "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("%w: mohe paymentInfo", ErrInvalidPaymentInfo)
	}
	phonePart := strings.TrimSpace(parts[1])
	if len(phonePart) <= 10 {
		return "", fmt.Errorf("%w: mohe paymentInfo", ErrInvalidPaymentInfo)
	}
	phone := strings.TrimSpace(phonePart[10:])
	if phone == "" {
		return "", fmt.Errorf("%w: mohe paymentInfo", ErrInvalidPaymentInfo)
	}
	return phone, nil
}

// GetBills inquires a bill (telecoms, utilities, government, etc.) and maintains a per-MSISDN cache.
func (s *Service) GetBills(ctx context.Context, tenantID string, b Bills) (ebs_fields.EBSParserFields, BillAmounts, error) {
	var due BillAmounts
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return ebs_fields.EBSParserFields{}, due, err
	}
	b.PayeeID = strings.TrimSpace(b.PayeeID)
	if b.PayeeID == "" {
		return ebs_fields.EBSParserFields{}, due, ErrMissingBillerID
	}

	uid, err := uuid.NewRandom()
	if err != nil {
		return ebs_fields.EBSParserFields{}, due, err
	}

	var fields ebs_fields.ConsumerBillInquiryFields
	fields.ApplicationId = s.NoebsConfig.ConsumerID
	fields.UUID = uid.String()
	updatePaymentInfo(&fields, b)
	fields.PayeeId = b.PayeeID

	ipinBlock, err := ipin.Encrypt(s.NoebsConfig.EBSConsumerKey, s.NoebsConfig.BillInquiryIPIN, uid.String())
	if err != nil {
		return ebs_fields.EBSParserFields{}, due, err
	}

	fields.ConsumerCardHolderFields.Ipin = ipinBlock
	fields.ConsumerCardHolderFields.Pan = s.NoebsConfig.BillInquiryPAN
	fields.ConsumerCardHolderFields.ExpDate = s.NoebsConfig.BillInquiryExpDate
	fields.ConsumerCommonFields.TranDateTime = ebs_fields.EbsDate()

	cacheBills := ebs_fields.CacheBillers{Mobile: b.Phone, BillerID: b.PayeeID}

	payload, err := json.Marshal(fields)
	if err != nil {
		return ebs_fields.EBSParserFields{}, due, err
	}

	res, err := s.callEBSRaw(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerBillInquiryEndpoint, payload)
	if err != nil {
		return res, due, err
	}

	parsedDue, err := parseDueAmounts(fields.PayeeId, res.BillInfo)
	if err != nil {
		return res, due, err
	}

	if err := s.Store.UpsertCacheBiller(ctx, tenantID, cacheBills.Mobile, cacheBills.BillerID); err != nil {
		return res, due, err
	}
	return res, parsedDue, nil
}

// GetBiller returns a cached biller id for the MSISDN.
func (s *Service) GetBiller(ctx context.Context, tenantID, mobile string) (string, error) {
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return "", err
	}
	if mobile == "" {
		return "", ErrMissingMobile
	}
	if s == nil || s.Store == nil {
		return "", ErrMissingStore
	}
	cached, err := s.Store.GetCacheBiller(ctx, tenantID, mobile)
	if err != nil {
		return "", err
	}
	return cached.BillerID, nil
}
