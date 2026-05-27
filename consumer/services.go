package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/google/uuid"
	"github.com/noebs/ipin"
)

var billerChan = make(chan billerForm)

type billerHookPayload struct {
	PaymentToken    string  `json:"payment_token"`
	IsSuccessful    bool    `json:"is_successful"`
	ResponseCode    int     `json:"response_code"`
	ResponseMessage string  `json:"response_message,omitempty"`
	ResponseStatus  string  `json:"response_status,omitempty"`
	UUID            string  `json:"uuid,omitempty"`
	TranAmount      float32 `json:"tran_amount,omitempty"`
	ApprovalCode    string  `json:"approval_code,omitempty"`
	ReferenceNumber string  `json:"reference_number,omitempty"`
	TranDateTime    string  `json:"tran_date_time,omitempty"`
}

// BillerHooks submits results to an optional external endpoint and updates cache_cards from EBS responses.
//
// tenantID must be explicit; defaults must be applied at the boundary (main).
func (s *Service) BillerHooks(ctx context.Context, tenantID string) {
	httpClient := &http.Client{Timeout: 5 * time.Second}
	for {
		select {
		case <-ctx.Done():
			return
		case value, ok := <-billerChan:
			if !ok {
				return
			}
			hookURL := strings.TrimSpace(s.NoebsConfig.ConsumerBillerHooksURL)
			if hookURL == "" {
				// Explicitly disabled by config.
				if s.NoebsConfig.IsDebug {
					log.Printf("biller hook disabled token=%s success=%v ebs=%s", value.Token, value.IsSuccessful, value.EBS.String())
				}
				continue
			}
			log.Printf("biller hook event token=%s success=%v ebs=%s", value.Token, value.IsSuccessful, value.EBS.String())
			parsed, err := url.Parse(hookURL)
			if err != nil || parsed.Scheme == "" || parsed.Host == "" {
				log.Printf("invalid consumer_biller_hooks_url=%q err=%v", hookURL, err)
				continue
			}
			if parsed.Scheme != "https" && !s.NoebsConfig.IsDebug {
				log.Printf("refusing to post biller hook to non-https url=%q (enable is_debug to allow)", hookURL)
				continue
			}

			payload := billerHookPayload{
				PaymentToken:    value.Token,
				IsSuccessful:    value.IsSuccessful,
				ResponseCode:    value.EBS.ResponseCode,
				ResponseMessage: value.EBS.ResponseMessage,
				ResponseStatus:  value.EBS.ResponseStatus,
				UUID:            value.EBS.UUID,
				TranAmount:      value.EBS.TranAmount,
				ApprovalCode:    value.EBS.ApprovalCode,
				ReferenceNumber: value.EBS.ReferenceNumber,
				TranDateTime:    value.EBS.TranDateTime,
			}
			data, err := json.Marshal(&payload)
			if err != nil {
				log.Printf("error marshaling biller hook payload: %v", err)
				continue
			}

			reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, hookURL, bytes.NewBuffer(data))
			if err != nil {
				cancel()
				log.Printf("error creating biller hook request: %v", err)
				continue
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := httpClient.Do(req)
			cancel()
			if err != nil {
				log.Printf("biller hook post failed: %v", err)
				continue
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= 300 {
				log.Printf("biller hook post failed: status=%d url=%q", resp.StatusCode, hookURL)
			}
		case res, ok := <-ebs_fields.EBSRes:
			if !ok {
				return
			}
			if tenantID == "" {
				log.Printf("cache card update skipped: missing tenant_id")
				continue
			}
			if s == nil || s.Store == nil {
				continue
			}
			if err := s.Store.UpsertCacheCard(ctx, tenantID, res); err != nil {
				log.Printf("cache card update failed: %v", err)
			}
		}
	}
}

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
	if tenantID == "" {
		return false, store.ErrMissingTenantID
	}
	if strings.TrimSpace(card.Pan) == "" {
		return false, store.ErrMissingPAN
	}
	if strings.TrimSpace(card.Expiry) == "" {
		return false, ErrMissingCardExpiry
	}
	if err := s.requireTransactionProjectionTarget(); err != nil {
		return false, err
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
	if tenantID == "" {
		return store.ErrMissingTenantID
	}
	if err := s.requireTransactionProjectionTarget(); err != nil {
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
