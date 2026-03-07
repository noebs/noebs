package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/google/uuid"
	"github.com/noebs/ipin"
)

func (s *Service) BillPayment(ctx context.Context, tenantID string, fields ebs_fields.ConsumerBillPaymentFields) (ebs_fields.EBSParserFields, error) {
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

	// Push notification (async).
	data := PushData{
		TenantID:      tenantID,
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
		data.Body = fmt.Sprintf("Payment failed due to: %v.", res.ResponseMessage)
		tranData <- data
		return res, err
	}

	data.Title = "Payment Success"
	data.EBSData.PAN = fields.Pan // Use the unmasked PAN for internal lookup.

	switch res.PayeeID {
	case "0010010001", "0010010002", "0010010003", "0010010004", "0010010005", "0010010006": // telecom
		// EBS returns payment info like "MPHONE=249..." (or similar). The old code sliced at [7:].
		phone := ""
		if len(res.PaymentInfo) > 7 {
			phone = "0" + res.PaymentInfo[7:]
		}
		if phone != "" {
			data.Phone = phone
			data.Body = fmt.Sprintf("You have received %v %v on your phone: %v.", res.TranAmount, res.AccountCurrency, phone)
			tranData <- data
			data.Phone = ""
		}
		data.Body = fmt.Sprintf("You have sent %v %v to phone: %v successfully.", res.TranAmount, res.AccountCurrency, phone)
	case "0010030002": // mohe
		data.Body = fmt.Sprintf("%v %v has been paid successfully for Education.", res.TranAmount, res.AccountCurrency)
	case "0010030004": // mohe arab
		// paymentInfo includes embedded data; old code used strings.Split(...)[1][10:].
		phone := ""
		parts := strings.Split(res.PaymentInfo, "/")
		if len(parts) > 1 && len(parts[1]) > 10 {
			phone = parts[1][10:]
		}
		if phone != "" {
			data.Phone = phone
			data.Body = fmt.Sprintf("%v %v has been paid successfully for Education.", res.TranAmount, res.AccountCurrency)
			tranData <- data
			data.Phone = ""
		} else {
			data.Body = fmt.Sprintf("%v %v has been paid successfully for Education.", res.TranAmount, res.AccountCurrency)
		}
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

	tranData <- data
	return res, nil
}

// GetBills inquires a bill (telecoms, utilities, government, etc.) and maintains a per-MSISDN cache.
func (s *Service) GetBills(ctx context.Context, tenantID string, b Bills) (ebs_fields.EBSParserFields, BillAmounts, error) {
	var due BillAmounts

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
	// Prefer cached biller id for this MSISDN when present.
	if s != nil && s.Store != nil {
		if oldCache, err := s.Store.GetCacheBiller(ctx, tenantID, b.Phone); err == nil {
			fields.PayeeId = oldCache.BillerID
			cacheBills.BillerID = oldCache.BillerID
		}
	}

	payload, err := json.Marshal(fields)
	if err != nil {
		return ebs_fields.EBSParserFields{}, due, err
	}

	res, err := s.callEBSRaw(ctx, tenantID, s.NoebsConfig.ConsumerIP, ebs_fields.ConsumerBillInquiryEndpoint, payload)
	if err != nil {
		// Some billers flip between prepaid/postpaid; maintain a heuristic cache.
		cacheBills.BillerID = flipBillerID(cacheBills.BillerID)
		if s != nil && s.Store != nil {
			_ = s.Store.UpsertCacheBiller(ctx, tenantID, cacheBills.Mobile, cacheBills.BillerID)
		}
		return res, due, err
	}

	parsedDue, err := parseDueAmounts(fields.PayeeId, res.BillInfo)
	if err != nil {
		cacheBills.BillerID = flipBillerID(cacheBills.BillerID)
		if s != nil && s.Store != nil {
			_ = s.Store.UpsertCacheBiller(ctx, tenantID, cacheBills.Mobile, cacheBills.BillerID)
		}
		return res, due, err
	}

	if s != nil && s.Store != nil {
		_ = s.Store.UpsertCacheBiller(ctx, tenantID, cacheBills.Mobile, cacheBills.BillerID)
	}
	return res, parsedDue, nil
}

// GetBiller returns a cached biller id for the MSISDN and triggers an async update when missing.
func (s *Service) GetBiller(ctx context.Context, tenantID, mobile string) (string, error) {
	if mobile == "" {
		return "", errors.New("empty_mobile")
	}
	if s == nil || s.Store == nil {
		return "", ErrMissingStore
	}
	guessed, err := s.Store.GetCacheBiller(ctx, tenantID, mobile)
	if err != nil {
		go s.billerID(ctx, tenantID, mobile)
		return "", err
	}
	return guessed.BillerID, nil
}
