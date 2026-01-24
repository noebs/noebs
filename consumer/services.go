package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/adonese/noebs/apperr"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/noebs/ipin"
	"github.com/sirupsen/logrus"
)

const (
	SMS_GATEWAY = "https://mazinhost.com/smsv1/sms/api?action=send-sms"
)

// CardFromNumber gets the gussesed associated mobile number to this pan
func (s *Service) CardFromNumber(c *fiber.Ctx) error {
	// the user must submit in their mobile number *ONLY*, and it is get
	q := c.Query("mobile_number")
	if q == "" {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "mobile number is empty", "code": "empty_mobile_number"})
	}
	tenantID := resolveTenantID(c, s.NoebsConfig)
	pan, err := s.Store.GetPanByMobile(c.UserContext(), tenantID, q)
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "No user with such mobile number", "code": "mobile_number_not_found"})
	}
	return c.Status(http.StatusOK).JSON(fiber.Map{"result": pan})
}

// GetCards Get all cards for the currently authorized user
func (s *Service) GetCards(c *fiber.Ctx) error {
	username := getMobile(c)
	if username == "" {
		return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"message": "unauthorized access", "code": "unauthorized_access"})
	}
	tenantID := resolveTenantID(c, s.NoebsConfig)
	userCards, err := s.Store.GetCardsOrFail(c.UserContext(), tenantID, username)
	if err != nil {
		// handle the error somehow
		logrus.WithFields(logrus.Fields{
			"code":    "unable to fetch cards",
			"message": err.Error(),
		}).Info("unable to get cards from store")
		return c.Status(http.StatusNotFound).JSON(fiber.Map{"cards": nil, "main_card": nil})
	}
	return c.Status(http.StatusOK).JSON(fiber.Map{"cards": userCards.Cards, "main_card": userCards.Cards[0]})
}

func (s *Service) AddDeviceToken(c *fiber.Ctx) error {
	username := getMobile(c)
	type data struct {
		Token string `json:"token"`
	}

	var req data
	if err := bindJSON(c, &req); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	tenantID := resolveTenantID(c, s.NoebsConfig)
	if err := s.Store.UpsertDeviceToken(c.UserContext(), tenantID, username, req.Token); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "db_error"})
	}
	return c.Status(http.StatusOK).JSON(nil)
}

// Beneficiaries manage all of beneficiaries data
func (s *Service) Beneficiaries(c *fiber.Ctx) error {
	mobile := getMobile(c)

	var req ebs_fields.Beneficiary
	_ = parseJSON(c, &req)
	s.Logger.Printf("the data in beneficiary is: %+v", req)
	tenantID := resolveTenantID(c, s.NoebsConfig)
	user, err := s.Store.GetUserByMobile(c.UserContext(), tenantID, mobile)
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	if c.Method() == fiber.MethodPost {
		req.UserID = user.ID
		if err := s.Store.UpsertBeneficiary(c.UserContext(), tenantID, user.ID, req); err != nil {
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "bad_request"})
		}
		return c.Status(http.StatusCreated).JSON(nil)
	} else if c.Method() == fiber.MethodGet {
		list, err := s.Store.ListBeneficiaries(c.UserContext(), tenantID, user.ID)
		if err != nil {
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "bad_request"})
		}
		return c.Status(http.StatusOK).JSON(list)
	} else if c.Method() == fiber.MethodDelete {
		if err := s.Store.DeleteBeneficiary(c.UserContext(), tenantID, user.ID, req.Data); err != nil {
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "bad_request"})
		}
		return c.Status(http.StatusNoContent).JSON(nil)
	}
	return nil
}

// AddCards Allow users to add card to their profile
// if main_card was set to true, then it will be their main card AND
// it will remove the previously selected one FIXME
func (s *Service) AddCards(c *fiber.Ctx) error {
	var listCards []ebs_fields.Card
	username := getMobile(c)
	if err := parseJSON(c, &listCards); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	tenantID := resolveTenantID(c, s.NoebsConfig)
	user, err := s.Store.GetUserByMobile(c.UserContext(), tenantID, username)
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "bad_request", "message": err.Error()})
	}

	// manually zero-valueing card ID to avoid gorm upserting it
	for idx := range listCards {
		listCards[idx].ID = 0
		listCards[idx].UserID = user.ID
	}
	if err := s.Store.AddCards(c.UserContext(), tenantID, user.ID, listCards); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	return c.Status(http.StatusOK).JSON(fiber.Map{"code": "ok", "message": "cards added"})
}

// EditCard allow authorized users to edit their cards (e.g., edit pan / expdate)
// this updates any card via
func (s *Service) EditCard(c *fiber.Ctx) error {
	var req ebs_fields.Card
	if err := bindJSON(c, &req); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "unmarshalling_error"})
	}
	username := getMobile(c)
	if username == "" {
		return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"message": "unauthorized access", "code": "unauthorized_access"})
	}
	// If no ID was provided that means we are adding a new card. We don't want that!
	if req.CardIdx == "" {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "card idx is empty", "code": "card_idx_empty"})
	}
	tenantID := resolveTenantID(c, s.NoebsConfig)
	user, err := s.Store.GetUserByMobile(c.UserContext(), tenantID, username)
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "database_error"})
	}
	req.UserID = user.ID
	if err := s.Store.UpdateCard(c.UserContext(), tenantID, user.ID, req); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "database_error", "message": err.Error()})
	}
	return c.Status(http.StatusCreated).JSON(fiber.Map{"result": "ok"})
}

// RemoveCard allow authorized users to remove their card
// when the send the card id (from its list in app view)
func (s *Service) RemoveCard(c *fiber.Ctx) error {
	username := getMobile(c)
	if username == "" {
		return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"message": "unauthorized access", "code": "unauthorized_access"})
	}
	var card ebs_fields.Card
	if err := bindJSON(c, &card); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "unmarshalling_error"})
	}
	s.Logger.Printf("the card is: %v+#", card)
	// If no ID was provided that means we are adding a new card. We don't want that!
	if card.CardIdx == "" {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "card idx is empty", "code": "card_idx_empty"})
	}

	tenantID := resolveTenantID(c, s.NoebsConfig)
	user, err := s.Store.GetUserByMobile(c.UserContext(), tenantID, username)
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "unmarshalling_error"})
	}
	card.UserID = user.ID
	if err := s.Store.DeleteCard(c.UserContext(), tenantID, user.ID, card.CardIdx); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "database_error", "message": err.Error()})
	}
	return c.Status(http.StatusOK).JSON(fiber.Map{"result": "ok"})
}

// NecToName gets an nec number from the context and maps it to its meter number
func (s *Service) NecToName(c *fiber.Ctx) error {
	if nec := c.Query("nec"); nec != "" {
		tenantID := resolveTenantID(c, s.NoebsConfig)
		name, err := s.Store.GetMeterName(c.UserContext(), tenantID, nec)
		if err != nil {
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "No user found with this NEC", "code": "nec_not_found"})
		} else {
			return c.Status(http.StatusOK).JSON(fiber.Map{"result": name})
		}
	}
	return nil
}

var billerChan = make(chan billerForm)

// BillerHooks submits results to external endpoint
func (s *Service) BillerHooks(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case value, ok := <-billerChan:
			if !ok {
				return
			}
			log.Printf("The recv is: %v", value)
			data, err := json.Marshal(&value)
			if err != nil {
				log.Printf("error marshaling biller hook payload: %v", err)
				continue
			}
			// FIXME this code is dangerous
			if value.to == "" {
				value.to = "http://test.tawasuloman.com:8088/ShihabSudanWS/ShihabEBSConfirmation"
			}
			if _, err := http.Post(value.to, "application/json", bytes.NewBuffer(data)); err != nil {
				log.Printf("the error is: %v", err)
			}
		case res, ok := <-ebs_fields.EBSRes:
			if !ok {
				return
			}
			tenantID := s.NoebsConfig.DefaultTenantID
			if tenantID == "" {
				tenantID = store.DefaultTenantID
			}
			if err := s.Store.UpsertCacheCard(ctx, tenantID, res); err != nil {
				log.Printf("cache card update failed: %v", err)
			}
		}
	}
}

// PaymentOrder used to perform a transaction on behalf of a noebs user. This api should be used behind an authorization middleware
// The goal of this api is to allow our customers to perform certain types of transactions (recurred ones) without having to worry about it.
// For example, if a user wants to make saving, or in case they want to they want to pay for their rent. Recurring payment scenarios are a lot.
// The current proposal is to use a _wallet_. Simply, a user will put a money into noebs bank account. Whenever a user want to perform a recurred payment, noebs can then
// use their wallet to perform the transaction.
//
// ## Problems we have so far
// - We are not allowed to store value, we cannot save users money in our account
// - We cannot store user's payment information (pan, ipin, exp date) in our system
// - And we don't want the user to everytime login into the app and key in their payment information
func (s *Service) PaymentOrder() fiber.Handler {
	return func(c *fiber.Ctx) error {
		mobile := getMobile(c)
		var req ebs_fields.Token
		token, _ := uuid.NewRandom()
		tenantID := resolveTenantID(c, s.NoebsConfig)
		user, err := s.Store.GetCardsOrFail(c.UserContext(), tenantID, mobile)
		if err != nil {
			log.Printf("error in retrieving card: %v", err)
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "bad_request", "message": err.Error()})
		}

		// there shouldn't be any error here, but still
		if err := bindJSON(c, &req); err != nil {
			log.Printf("error in retrieving card: %v", err)
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "bad_request", "message": err.Error()})
		}
		ipinBlock, err := ipin.Encrypt(s.NoebsConfig.EBSConsumerKey, user.Cards[0].IPIN, token.String())
		if err != nil {
			log.Printf("error in encryption: %v", err)
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "bad_request", "message": err.Error()})
		}
		data := ebs_fields.ConsumerCardTransferFields{
			ConsumerCommonFields: ebs_fields.ConsumerCommonFields{
				ApplicationId: s.NoebsConfig.ConsumerID,
				TranDateTime:  ebs_fields.EbsDate(),
				UUID:          token.String(),
			},
			// user.Cards[0] won't error, since we:
			// query the result in [ebs_fields.GetUserCard] and order them by is_main and first created
			// if no card was added to the user, the [ebs_fields.GetUserCard] will error and we handle it
			ConsumerCardHolderFields: ebs_fields.ConsumerCardHolderFields{
				Pan:     user.Cards[0].Pan,
				Ipin:    ipinBlock,
				ExpDate: user.Cards[0].Expiry,
			},
			AmountFields: ebs_fields.AmountFields{
				TranAmount:       float32(req.Amount), // it should be populated
				TranCurrencyCode: "SDG",
			},
			ToCard: req.ToCard,
		}
		updatedRequest, err := json.Marshal(&data)
		if err != nil {
			jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrMarshal, err.Error()))
			return nil
		}
		// Modify request body for downstream handlers
		c.Request().SetBody(updatedRequest)
		c.Request().Header.SetContentType("application/json")

		// Call the next handler
		return c.Next()

		// pubsub := s.Redis.Subscribe("chan_cashouts")
		// // Wait for confirmation that subscription is created before publishing anything.
		// _, err := pubsub.Receive()
		// if err != nil {
		// 	log.Printf("Error in pubsub: %v", err)

		// }
		// // Publish a message.
		// err = s.Redis.Publish("chan_cashouts", msg).Err() // So, we are currently just pushing to the data
		// if err != nil {
		// 	log.Printf("Error in pubsub: %v", err)
		// }
		// time.AfterFunc(time.Second, func() {
		// 	// When pubsub is closed channel is closed too.
		// 	_ = pubsub.Close()
		// })
	}
}

// CashoutPub experimental support to add pubsub support
// we need to make this api public
func (s *Service) CashoutPub(ctx context.Context) {
	_ = ctx
	s.Logger.Printf("cashout pubsub disabled (redis removed)")
}

func (s *Service) pubSub(ctx context.Context, channel string, message interface{}) {
	_ = ctx
	_ = channel
	_ = message
}

func updatePaymentInfo(ebsBills *ebs_fields.ConsumerBillInquiryFields, b bills) {
	switch b.PayeeID {
	case "0010010002": // zain
		ebsBills.PaymentInfo = "MPHONE=" + b.Phone
	case "0010010004": // mtn
		ebsBills.PaymentInfo = "MPHONE=" + b.Phone
	case "0010010006": //sudani
		ebsBills.PaymentInfo = "MPHONE=" + b.Phone
	case "0055555555": // e-invoice
		// ("customerBillerRef="
		ebsBills.PaymentInfo = "customerBillerRef=" + b.Ref
	case "0010030002": // mohe
		// "SETNUMBER=$seatNumber/STUDCOURSEID=${id.courseID}/STUDFORMKIND=${type.id}"
		ebsBills.PaymentInfo = fmt.Sprintf("SETNUMBER=%s/STUDCOURSEID=%s/STUDFORMKIND=%s", b.SeatNumber, b.CourseID, b.FormKind)
	case "0010030004": // mohe-arab
		// "STUCNAME=$name/STUCPHONE=$phone/STUDCOURSEID=${id.courseID}/STUDFORMKIND=${type.id}"
		ebsBills.PaymentInfo = fmt.Sprintf("STUCNAME=%s/STUCPHONE=%s/STUDCOURSEID=%s/STUDFORMKIND=%s", b.Name, b.Phone, b.CourseID, b.FormKind)
	case "0010030003": // Customs
		// return "BANKCODE=$bank/DECLARANTCODE=$id"
		ebsBills.PaymentInfo = "BANKCODE=$bank/DECLARANTCODE=" + ebsBills.PaymentInfo
	case "0010050001": // e-15
		// return "SERVICEID=$id/INVOICENUMBER=$invoice/PHONENUMBER=$p"
		ebsBills.PaymentInfo = fmt.Sprintf("SERVICEID=%s/INVOICENUMBER=%s/PHONENUMBER=%s", b.ServiceID, b.InvoiceNumber, b.Phone)
	}
}

// bills represents a json object for all of ebs bills
type bills struct {
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

func parseDueAmounts(payeeId string, paymentInfo map[string]any) (billAmounts, error) {
	var b billAmounts
	if paymentInfo == nil {
		return b, errors.New("not a biller")
	}
	switch payeeId {
	case "0010010002": // zain
		b.Amount = paymentInfo["totalAmount"].(string)
		b.DueAmount = paymentInfo["unbilledAmount"].(string)
		b.PaidAmount = paymentInfo["billedAmount"].(string)
		return b, nil // FIXME(adonese): Zain also has an `unbilledAmount` field like mtn but we are using totalAmount here just for testing
	case "0010010004": // mtn
		if t, ok := paymentInfo["total"].(string); ok {
			b.Amount = t
			b.DueAmount = t
			return b, nil
		}
		return b, errors.New("not a biller")

	case "0010010006": //sudani
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
		b.MinAmount = paymentInfo["minAmount"].(string)
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
	case "0010030003": // Customs
		if t, ok := paymentInfo["AmountToBePaid"].(string); ok {
			b.Amount = t
			b.DueAmount = t
		}
		return b, nil
	case "0010050001": // e-15
		b.Amount = paymentInfo["TotalAmount"].(string)
		b.DueAmount = paymentInfo["DueAmount"].(string)
		return b, nil
	default:
		return b, nil
	}
	return b, nil

}

type billAmounts struct {
	Amount     string `json:"amount,omitempty"`
	DueAmount  string `json:"due_amount,omitempty"`
	MinAmount  string `json:"min_amount"`
	PaidAmount string `json:"paid_amount"`
}

func removeComma(amount string) string {
	return strings.ReplaceAll(amount, ",", "")
}

func toInt(amount string) int {
	d, _ := strconv.Atoi(amount)
	return d
}

// billerID retrieves the type of a mobile number (the operator and the type with prepaid or postpaid) using
// some heuristics -- it fallback to making a request to ebs as a last resort to get the type of phone number (by making a free transaction, that is bill inquiry)
func (s *Service) billerID(ctx context.Context, tenantID, mobile string) (string, error) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerBillInquiryEndpoint
	var b bills
	b.PayeeID = guessMobile(mobile)
	b.Phone = mobile
	uid, _ := uuid.NewRandom()
	var fields ebs_fields.ConsumerBillInquiryFields
	fields.ApplicationId = s.NoebsConfig.ConsumerID
	fields.UUID = uid.String()
	updatePaymentInfo(&fields, b)
	fields.PayeeId = b.PayeeID
	ipinBlock, err := ipin.Encrypt(s.NoebsConfig.EBSConsumerKey, s.NoebsConfig.BillInquiryIPIN, uid.String())
	if err != nil {
		s.Logger.Printf("error in encryption: %v", err)
		return "", err
	}
	fields.ConsumerCardHolderFields.Ipin = ipinBlock
	fields.ConsumerCardHolderFields.Pan = s.NoebsConfig.BillInquiryPAN
	fields.ConsumerCardHolderFields.ExpDate = s.NoebsConfig.BillInquiryExpDate
	fields.ConsumerCommonFields.TranDateTime = ebs_fields.EbsDate()
	cacheBills := ebs_fields.CacheBillers{Mobile: b.Phone, BillerID: b.PayeeID}
	// Get our cache results before hand
	if oldCache, err := s.Store.GetCacheBiller(ctx, tenantID, b.Phone); err == nil { // we have stored this phone number before
		fields.PayeeId = oldCache.BillerID // use the data we stored previously
	}
	jsonBuffer, err := json.Marshal(fields)
	if err != nil {
		// there's an error in parsing the struct. Server error.
		s.Logger.Printf("the error is: %v", err)
		return "", err
	}
	// the only part left is fixing EBS errors. Formalizing them per se.
	code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
	s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)
	// mask the pan
	res.MaskPAN()
	res.Name = s.ToDatabasename(url)
	if err := s.Store.CreateTransaction(ctx, tenantID, res.EBSResponse); err != nil {
		logrus.WithFields(logrus.Fields{
			"code":    "unable to migrate purchase model",
			"message": err,
		}).Info("error in migrating purchase model")
	}
	if ebsErr != nil {
		// it fails gracefully here..
		cacheBills.BillerID = flipBillerID(cacheBills.BillerID)
		_ = s.Store.UpsertCacheBiller(ctx, tenantID, cacheBills.Mobile, cacheBills.BillerID)
		return cacheBills.BillerID, nil
	} else {
		_ = s.Store.UpsertCacheBiller(ctx, tenantID, cacheBills.Mobile, cacheBills.BillerID)
		return cacheBills.BillerID, nil
	}
}

func flipBillerID(id string) string {
	newId := id
	switch id {
	case "0010010002": // zain bill payment
		newId = "0010010001" // zain top up
	case "0010010001":
		newId = "0010010002"
	case "0010010004": // mtn bill payment
		newId = "0010010003" // mtn top up
	case "0010010003":
		newId = "0010010004"
	case "0010010006": // sudani bill payment
		newId = "0010010005" // sudani top up
	case "0010010005":
		newId = "0010010006"
	}
	return newId
}

// isValidCard checks noebs database first and fallback to making an actual payment request
// to ensure that a card is actually valid
func (s *Service) isValidCard(ctx context.Context, tenantID string, card ebs_fields.CacheCards) (bool, error) {
	if tenantID == "" {
		tenantID = store.DefaultTenantID
	}
	if exists, err := s.Store.CardExists(ctx, tenantID, card.Pan); err == nil && exists {
		return true, nil
	}

	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerBalanceEndpoint
	var fields ebs_fields.ConsumerBalanceFields
	uid, _ := uuid.NewRandom()
	fields.UUID = uid.String()
	fields.ConsumerCommonFields.TranDateTime = ebs_fields.EbsDate()
	fields.ApplicationId = s.NoebsConfig.ConsumerID

	ipinBlock, err := ipin.Encrypt(s.NoebsConfig.EBSConsumerKey, s.NoebsConfig.BillInquiryIPIN, uid.String())
	if err != nil {
		s.Logger.Printf("error in encryption: %v", err)
		return false, err
	}
	fields.ConsumerCardHolderFields.Ipin = ipinBlock
	fields.ConsumerCardHolderFields.Pan = card.Pan
	fields.ConsumerCardHolderFields.ExpDate = card.Expiry

	jsonBuffer, err := json.Marshal(fields)
	if err != nil {
		s.Logger.Printf("error in encryption: %v", err)
		return false, err
	}

	// the only part left is fixing EBS errors. Formalizing them per se.
	_, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)

	// s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)
	// mask the pan
	res.MaskPAN()
	res.Name = s.ToDatabasename(url)
	if err := s.Store.CreateTransaction(ctx, tenantID, res.EBSResponse); err != nil {
		logrus.WithFields(logrus.Fields{
			"code":    "unable to migrate purchase model",
			"message": err,
		}).Info("error in migrating purchase model")
	}
	if res.ResponseCode == ebs_fields.INVALIDCARD {
		return false, ebsErr
	}
	return true, nil
}

func guessMobile(mobile string) string {
	if strings.HasPrefix("091", mobile) {
		return "0010010002" // zain bills
	} else if strings.HasPrefix("096", mobile) {
		return "0010010002" // zain bills
	} else if strings.HasPrefix("099", mobile) {
		return "0010010004" // mtn bills
	} else if strings.HasPrefix("092", mobile) {
		return "0010010004" // mtn bills
	} else {
		return "0010010006" // sudani
	}
}

func (s *Service) GetIpinPubKey(ctx context.Context, tenantID string) error {
	s.Logger.Printf("someone launched getipin goroutine")
	url := s.NoebsConfig.IPIN + ebs_fields.QRPublicKey
	s.Logger.Printf("EBS url is: %v", url)
	id, _ := uuid.NewRandom()
	fields := ebs_fields.ConsumerGenerateIPINFields{Username: s.NoebsConfig.EBSIPINUsername, TranDateTime: ebs_fields.EbsDate(), UUID: id.String()}
	jsonBuffer, err := json.Marshal(fields)
	if err != nil {
		return errors.New("missing fields")
	}
	// the only part left is fixing EBS errors. Formalizing them per se.
	code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
	s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)
	res.Name = s.ToDatabasename(url)
	if err := s.Store.CreateTransaction(ctx, tenantID, res.EBSResponse); err != nil {
		s.Logger.Printf("transaction save failed: %v", err)
	}
	if ebsErr != nil {
		return errors.New("error in transaction: ebs")
	} else {
		// store the key somewhere
		// this may potentially introduces a race condition!
		ebsIpinEncryptionKey = res.PubKeyValue
		return nil
	}
}

// Notifications handles various crud operations (json)
func (s *Service) Notifications(c *fiber.Ctx) error {
	mobile := getMobile(c)
	tenantID := resolveTenantID(c, s.NoebsConfig)
	user, err := s.Store.GetUserByMobile(c.UserContext(), tenantID, mobile)
	if err != nil {
		s.Logger.Printf("Error finding user: %v", err)
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	records, err := s.Store.GetNotifications(c.UserContext(), tenantID, user.Mobile)
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if err := c.Status(http.StatusOK).JSON(records); err != nil {
		return err
	}
	_ = s.Store.MarkNotificationsRead(c.UserContext(), tenantID, mobile)
	return nil
}

// GetUser returns the user profile object for the currently logged in user
func (s *Service) GetUser(c *fiber.Ctx) error {
	mobile := getMobile(c)
	tenantID := resolveTenantID(c, s.NoebsConfig)
	user, err := s.Store.GetUserByMobile(c.UserContext(), tenantID, mobile)
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "database_error"})
	}
	profile := ebs_fields.UserProfile{
		Fullname: user.Fullname,
		Username: user.Username,
		Email:    user.Email,
		Birthday: user.Birthday,
		Gender:   user.Gender,
	}
	return c.Status(http.StatusOK).JSON(profile)
}

// UpdateUser allows the currently logged in user to update their profile info
func (s *Service) UpdateUser(c *fiber.Ctx) error {
	var profile ebs_fields.UserProfile
	if err := bindJSON(c, &profile); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "binding_error"})
	}

	mobile := getMobile(c)
	tenantID := resolveTenantID(c, s.NoebsConfig)
	user, err := s.Store.GetUserByMobile(c.UserContext(), tenantID, mobile)
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "database_error"})
	}
	if profile.Username != "" {
		if other, err := s.Store.FindUserByUsername(c.UserContext(), tenantID, profile.Username); err == nil && other.ID != user.ID {
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "duplication_error", "message": "username already exists"})
		}
	}
	if err := s.Store.UpdateUserProfile(c.UserContext(), tenantID, user.ID, profile); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "database_error", "message": err.Error()})
	}
	return c.Status(http.StatusOK).JSON(fiber.Map{"result": "ok"})
}

func (s *Service) GetUserLanguage(c *fiber.Ctx) error {
	mobile := getMobile(c)
	tenantID := resolveTenantID(c, s.NoebsConfig)
	user, err := s.Store.GetUserByMobile(c.UserContext(), tenantID, mobile)
	if err != nil {
		s.Logger.Printf("ERROR: could not get user from by mobile: %v", err.Error())
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "database_error"})
	}
	return c.Status(http.StatusOK).JSON(fiber.Map{"language": user.Language})
}

func (s *Service) SetUserLanguage(c *fiber.Ctx) error {
	mobile := getMobile(c)
	language := c.Query("language")
	tenantID := resolveTenantID(c, s.NoebsConfig)
	user, err := s.Store.GetUserByMobile(c.UserContext(), tenantID, mobile)
	if err != nil {
		s.Logger.Printf("ERROR: could not get user from by mobile: %v", err.Error())
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "database_error"})
	}
	if language == "" {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "You must set a language", "code": "client_error"})
	}
	if err := s.Store.UpdateUserLanguage(c.UserContext(), tenantID, user.ID, language); err != nil {
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"message": err.Error(), "code": "database_error"})
	}
	return c.Status(http.StatusOK).JSON(fiber.Map{"result": "ok"})
}

func (s *Service) KYC(ctx *fiber.Ctx) error {
	// Decode the request body into the CreateUserRequest struct
	var request ebs_fields.KYCPassport
	if err := bindJSON(ctx, &request); err != nil {
		return ctx.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	tenantID := resolveTenantID(ctx, s.NoebsConfig)
	kyc := &ebs_fields.KYC{
		UserMobile:  request.Mobile,
		Mobile:      request.Mobile,
		Selfie:      request.Selfie,
		PassportImg: request.PassportImg,
	}
	passport := request.Passport
	if err := s.Store.UpdateKYC(ctx.UserContext(), tenantID, kyc, &passport); err != nil {
		return ctx.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	// Return a success message
	return ctx.Status(http.StatusOK).JSON(fiber.Map{"message": "KYC created successfully", "code": "ok"})
}
