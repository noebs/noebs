package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/parsing"
	"github.com/adonese/noebs/store"
	"github.com/google/uuid"
	"github.com/noebs/ipin"
)

// Bills represents an inquiry request for EBS billers (telecoms, utilities, etc).
type Bills struct {
	Phone         string `json:"phone"`
	Ref           string `json:"ref"`
	SeatNumber    string `json:"seat_number"`
	CourseID      string `json:"course_id"`
	FormKind      string `json:"form_kind"`
	Name          string `json:"name"`
	Bank          string `json:"bank"`
	DeclarantCode string `json:"declarant_code"` // declarant code
	InvoiceNumber string `json:"invoice"`
	PayeeID       string `json:"payee_id"`
	ServiceID     string `json:"service_id"`
}

type BillAmounts struct {
	Amount     string `json:"amount,omitempty"`
	DueAmount  string `json:"due_amount,omitempty"`
	MinAmount  string `json:"min_amount"`
	PaidAmount string `json:"paid_amount"`
}

func updatePaymentInfo(ebsBills *ebs_fields.ConsumerBillInquiryFields, b Bills) {
	switch b.PayeeID {
	case "0010010002": // zain
		ebsBills.PaymentInfo = "MPHONE=" + b.Phone
	case "0010010004": // mtn
		ebsBills.PaymentInfo = "MPHONE=" + b.Phone
	case "0010010006": // sudani
		ebsBills.PaymentInfo = "MPHONE=" + b.Phone
	case "0055555555": // e-invoice
		ebsBills.PaymentInfo = "customerBillerRef=" + b.Ref
	case "0010030002": // mohe
		ebsBills.PaymentInfo = "SETNUMBER=" + b.SeatNumber + "/STUDCOURSEID=" + b.CourseID + "/STUDFORMKIND=" + b.FormKind
	case "0010030004": // mohe-arab
		ebsBills.PaymentInfo = "STUCNAME=" + b.Name + "/STUCPHONE=" + b.Phone + "/STUDCOURSEID=" + b.CourseID + "/STUDFORMKIND=" + b.FormKind
	case "0010030003": // Customs
		ebsBills.PaymentInfo = "BANKCODE=$bank/DECLARANTCODE=" + ebsBills.PaymentInfo
	case "0010050001": // e-15
		ebsBills.PaymentInfo = "SERVICEID=" + b.ServiceID + "/INVOICENUMBER=" + b.InvoiceNumber + "/PHONENUMBER=" + b.Phone
	}
}

func parseDueAmounts(payeeId string, paymentInfo map[string]any) (BillAmounts, error) {
	var b BillAmounts
	if paymentInfo == nil {
		return b, fmt.Errorf("%w: paymentInfo", ErrInvalidPaymentInfo)
	}
	switch payeeId {
	case "0010010002": // zain
		var err error
		if b.Amount, err = requiredPaymentInfoString(paymentInfo, "totalAmount"); err != nil {
			return b, err
		}
		if b.DueAmount, err = requiredPaymentInfoString(paymentInfo, "unbilledAmount"); err != nil {
			return b, err
		}
		if b.PaidAmount, err = requiredPaymentInfoString(paymentInfo, "billedAmount"); err != nil {
			return b, err
		}
		return b, nil
	case "0010010004": // mtn
		total, err := requiredPaymentInfoString(paymentInfo, "total")
		if err != nil {
			return b, err
		}
		b.Amount = total
		b.DueAmount = total
		return b, nil
	case "0010010006": // sudani
		billAmount, err := requiredPaymentInfoString(paymentInfo, "billAmount")
		if err != nil {
			return b, err
		}
		b.Amount = billAmount
		b.DueAmount = billAmount
		return b, nil
	case "0055555555": // e-invoice
		amountDue, err := requiredPaymentInfoString(paymentInfo, "amount_due")
		if err != nil {
			return b, err
		}
		b.Amount = amountDue
		b.DueAmount = amountDue
		if t, ok := paymentInfo["minAmount"].(string); ok {
			b.MinAmount = t
		}
		return b, nil
	case "0010030002": // mohe
		dueAmount, err := requiredPaymentInfoString(paymentInfo, "dueAmount")
		if err != nil {
			return b, err
		}
		b.Amount = dueAmount
		b.DueAmount = dueAmount
		return b, nil
	case "0010030004": // mohe-arab
		dueAmount, err := requiredPaymentInfoString(paymentInfo, "dueAmount")
		if err != nil {
			return b, err
		}
		b.Amount = dueAmount
		b.DueAmount = dueAmount
		return b, nil
	case "0010030003": // Customs
		amountToBePaid, err := requiredPaymentInfoString(paymentInfo, "AmountToBePaid")
		if err != nil {
			return b, err
		}
		b.Amount = amountToBePaid
		b.DueAmount = amountToBePaid
		return b, nil
	case "0010050001": // e-15
		var err error
		if b.Amount, err = requiredPaymentInfoString(paymentInfo, "TotalAmount"); err != nil {
			return b, err
		}
		if b.DueAmount, err = requiredPaymentInfoString(paymentInfo, "DueAmount"); err != nil {
			return b, err
		}
		return b, nil
	default:
		return b, nil
	}
}

func requiredPaymentInfoString(paymentInfo map[string]any, key string) (string, error) {
	text, err := parsing.RequiredString(paymentInfo, key)
	if err != nil {
		return "", fmt.Errorf("%w: paymentInfo.%s", ErrInvalidPaymentInfo, key)
	}
	return text, nil
}

// isValidCard verifies card credentials with EBS.
func (s *Service) isValidCard(ctx context.Context, tenantID string, card ebs_fields.CacheCards) (bool, error) {
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(card.Pan) == "" {
		return false, store.ErrMissingPAN
	}
	if strings.TrimSpace(card.Expiry) == "" {
		return false, ErrMissingCardExpiry
	}

	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerBalanceEndpoint
	var fields ebs_fields.ConsumerBalanceFields
	uid, err := uuid.NewRandom()
	if err != nil {
		return false, err
	}
	fields.UUID = uid.String()
	fields.ConsumerCommonFields.TranDateTime = ebs_fields.EbsDate()
	fields.ApplicationId = s.NoebsConfig.ConsumerID

	ipinBlock, err := ipin.Encrypt(s.NoebsConfig.EBSConsumerKey, s.NoebsConfig.BillInquiryIPIN, uid.String())
	if err != nil {
		return false, err
	}
	fields.ConsumerCardHolderFields.Ipin = ipinBlock
	fields.ConsumerCardHolderFields.Pan = card.Pan
	fields.ConsumerCardHolderFields.ExpDate = card.Expiry

	jsonBuffer, err := json.Marshal(fields)
	if err != nil {
		return false, err
	}

	_, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
	res.MaskPAN()
	res.Name = s.ToDatabasename(url)
	recordErr := s.recordTransaction(ctx, tenantID, res.EBSResponse)

	if res.ResponseCode == ebs_fields.INVALIDCARD {
		return false, errors.Join(ErrInvalidCard, ebsErr, recordErr)
	}
	if ebsErr != nil {
		return false, errors.Join(ebsErr, recordErr)
	}
	if recordErr != nil {
		return false, recordErr
	}
	return true, nil
}

func (s *Service) GetIpinPubKey(ctx context.Context, tenantID string) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	url := s.NoebsConfig.IPINIp + ebs_fields.QRPublicKey
	id, err := newConsumerUUIDString()
	if err != nil {
		return err
	}
	fields := ebs_fields.ConsumerGenerateIPINFields{
		Username:     s.NoebsConfig.EBSIPINUsername,
		TranDateTime: ebs_fields.EbsDate(),
		UUID:         id,
	}
	jsonBuffer, err := json.Marshal(fields)
	if err != nil {
		return errors.New("missing fields")
	}
	_, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
	res.Name = s.ToDatabasename(url)
	recordErr := s.recordTransaction(ctx, tenantID, res.EBSResponse)
	if ebsErr != nil {
		return errors.Join(errors.New("error in transaction: ebs"), recordErr)
	}
	if recordErr != nil {
		return recordErr
	}
	ebsIpinEncryptionKey = res.PubKeyValue
	return nil
}
