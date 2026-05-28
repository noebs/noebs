package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
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
		return b, errors.New("not a biller")
	}
	switch payeeId {
	case "0010010002": // zain
		b.Amount, _ = paymentInfo["totalAmount"].(string)
		b.DueAmount, _ = paymentInfo["unbilledAmount"].(string)
		b.PaidAmount, _ = paymentInfo["billedAmount"].(string)
		return b, nil
	case "0010010004": // mtn
		if t, ok := paymentInfo["total"].(string); ok {
			b.Amount = t
			b.DueAmount = t
			return b, nil
		}
		return b, errors.New("not a biller")
	case "0010010006": // sudani
		if t, ok := paymentInfo["billAmount"].(string); ok {
			b.Amount = t
			b.DueAmount = t
		}
		return b, nil
	case "0055555555": // e-invoice
		if t, ok := paymentInfo["amount_due"].(string); ok {
			b.Amount = t
			b.DueAmount = t
		}
		if t, ok := paymentInfo["minAmount"].(string); ok {
			b.MinAmount = t
		}
		return b, nil
	case "0010030002": // mohe
		if t, ok := paymentInfo["dueAmount"].(string); ok {
			b.Amount = t
			b.DueAmount = t
		}
		return b, nil
	case "0010030004": // mohe-arab
		if t, ok := paymentInfo["dueAmount"].(string); ok {
			b.Amount = t
			b.DueAmount = t
		}
		return b, nil
	case "0010030003": // Customs
		if t, ok := paymentInfo["AmountToBePaid"].(string); ok {
			b.Amount = t
			b.DueAmount = t
		}
		return b, nil
	case "0010050001": // e-15
		b.Amount, _ = paymentInfo["TotalAmount"].(string)
		b.DueAmount, _ = paymentInfo["DueAmount"].(string)
		return b, nil
	default:
		return b, nil
	}
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
	id, _ := uuid.NewRandom()
	fields := ebs_fields.ConsumerGenerateIPINFields{
		Username:     s.NoebsConfig.EBSIPINUsername,
		TranDateTime: ebs_fields.EbsDate(),
		UUID:         id.String(),
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
