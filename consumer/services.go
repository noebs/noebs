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
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/utils"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/noebs/ipin"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm/clause"
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
	ctx := c.UserContext()
	// now search through redis for this mobile number!
	// first check if we have already collected that number before
	pan, err := s.Redis.Get(ctx, q+":pan").Result()
	if err == nil {
		return c.Status(http.StatusOK).JSON(fiber.Map{"result": pan})
	}
	username, err := s.Redis.Get(ctx, q).Result()
	if err == redis.Nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "No user with such mobile number", "code": "mobile_number_not_found"})
	}
	if pan, ok := utils.PanfromMobile(ctx, username, s.Redis); ok {
		return c.Status(http.StatusOK).JSON(fiber.Map{"result": pan})
	} else {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "No user with such mobile number", "code": "mobile_number_not_found"})
	}
}

// GetCards Get all cards for the currently authorized user
func (s *Service) GetCards(c *fiber.Ctx) error {
	username := getMobile(c)
	if username == "" {
		return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"message": "unauthorized access", "code": "unauthorized_access"})
	}
	userCards, err := ebs_fields.GetCardsOrFail(username, s.Db)
	if err != nil {
		// handle the error somehow
		logrus.WithFields(logrus.Fields{
			"code":    "unable to get results from redis",
			"message": err.Error(),
		}).Info("unable to get results from redis")
		return c.Status(http.StatusNotFound).JSON(fiber.Map{"cards": nil, "main_card": nil})
	}
	return c.Status(http.StatusOK).JSON(fiber.Map{"cards": userCards.Cards, "main_card": userCards.Cards[0]})
}

func (s *Service) AddFirebaseID(c *fiber.Ctx) error {
	username := getMobile(c)
	type data struct {
		Token string `json:"token"`
	}

	var req data
	if err := bindJSON(c, &req); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	if res := s.Db.Debug().Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "mobile"}},
		DoUpdates: clause.Assignments(map[string]interface{}{"device_id": req.Token}),
	}).Create(&ebs_fields.User{Mobile: username, DeviceID: req.Token}); res.Error != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": res.Error, "code": "db_error"})
	}
	return c.Status(http.StatusOK).JSON(nil)
}

// Beneficiaries manage all of beneficiaries data
func (s *Service) Beneficiaries(c *fiber.Ctx) error {
	mobile := getMobile(c)

	var req ebs_fields.Beneficiary
	_ = parseJSON(c, &req)
	s.Logger.Printf("the data in beneficiary is: %+v", req)
	user, err := ebs_fields.NewUserWithBeneficiaries(mobile, s.Db)
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	if c.Method() == fiber.MethodPost {
		user.UpsertBeneficiary([]ebs_fields.Beneficiary{req})
		return c.Status(http.StatusCreated).JSON(nil)
	} else if c.Method() == fiber.MethodGet {
		return c.Status(http.StatusOK).JSON(user.Beneficiaries)
	} else if c.Method() == fiber.MethodDelete {
		req.UserID = user.ID
		ebs_fields.DeleteBeneficiary(req, s.Db)
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
	user, err := ebs_fields.GetUserByMobile(username, s.Db)
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "bad_request", "message": err.Error()})
	}

	// manually zero-valueing card ID to avoid gorm upserting it
	for idx := range listCards {
		listCards[idx].ID = 0
		listCards[idx].UserID = user.ID
	}
	if err := user.UpsertCards(listCards); err != nil {
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
	user, err := ebs_fields.GetUserByMobile(username, s.Db)
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "database_error"})
	}
	req.UserID = user.ID
	if err := ebs_fields.UpdateCard(req, s.Db); err != nil {
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

	user, err := ebs_fields.GetUserByMobile(username, s.Db)
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "unmarshalling_error"})
	}
	card.UserID = user.ID
	if err := ebs_fields.DeleteCard(card, s.Db); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "database_error", "message": err.Error()})
	}
	return c.Status(http.StatusOK).JSON(fiber.Map{"result": "ok"})
}

// NecToName gets an nec number from the context and maps it to its meter number
func (s *Service) NecToName(c *fiber.Ctx) error {
	if nec := c.Query("nec"); nec != "" {
		ctx := c.UserContext()
		name, err := s.Redis.HGet(ctx, "meters", nec).Result()
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
func (s Service) BillerHooks() {

	for {
		select {
		case value := <-billerChan:
			log.Printf("The recv is: %v", value)
			data, _ := json.Marshal(&value)
			// FIXME this code is dangerous
			if value.to == "" {
				value.to = "http://test.tawasuloman.com:8088/ShihabSudanWS/ShihabEBSConfirmation"
			}
			if _, err := http.Post(value.to, "application/json", bytes.NewBuffer(data)); err != nil {
				log.Printf("the error is: %v", err)
			}
		case res := <-ebs_fields.EBSRes:
			s.Db.Debug().Model(&ebs_fields.CacheCards{}).Where("pan = ?", res.Pan).Update("is_valid", res.IsValid)
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
		user, err := ebs_fields.GetCardsOrFail(mobile, s.Db)
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
		updatedRequest, _ := json.Marshal(&data)
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
	pubsub := s.Redis.Subscribe(ctx, "chan_cashouts")

	// Wait for confirmation that subscription is created before publishing anything.
	_, err := pubsub.Receive(ctx)
	if err != nil {
		log.Printf("There is an error in connecting to chan.")
		return
	}

	// // Go channel which receives messages.
	ch := pubsub.Channel()

	// Consume messages.
	var card cashoutFields
	for msg := range ch {
		// So this is how we gonna do it! So great!
		// we have to parse the payload here:
		if err := json.Unmarshal([]byte(msg.Payload), &card); err != nil {
			log.Printf("Error in marshaling data: %v", err)
			continue
		}

		data, err := json.Marshal(&card)
		if err != nil {
			log.Printf("Error in marshaling response: %v", err)
			continue
		}
		_, err = http.Post(card.Endpoint, "application/json", bytes.NewBuffer(data))
		if err != nil {
			log.Printf("Error in response: %v", err)
		}
		fmt.Println(msg.Channel, msg.Payload)
	}
}

func (s *Service) pubSub(ctx context.Context, channel string, message interface{}) {
	pubsub := s.Redis.Subscribe(ctx, channel)

	// Wait for confirmation that subscription is created before publishing anything.
	_, err := pubsub.Receive(ctx)
	if err != nil {
		panic(err)
	}

	// // Go channel which receives messages.
	ch := pubsub.Channel()

	time.AfterFunc(time.Second, func() {
		// When pubsub is closed channel is closed too.
		_ = pubsub.Close()
	})

	// Consume messages.
	for msg := range ch {
		fmt.Println(msg.Channel, msg.Payload)
	}
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
func (s *Service) billerID(mobile string) (string, error) {
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
	if oldCache, err := ebs_fields.GetBillerInfo(b.Phone, s.Db); err == nil { // we have stored this phone number before
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
	if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
		logrus.WithFields(logrus.Fields{
			"code":    "unable to migrate purchase model",
			"message": err,
		}).Info("error in migrating purchase model")
	}
	if ebsErr != nil {
		// it fails gracefully here..
		cacheBills.Save(s.Db, true)
		return cacheBills.BillerID, nil
	} else {
		cacheBills.Save(s.Db, false)
		return cacheBills.BillerID, nil
	}
}

// isValidCard checks noebs database first and fallback to making an actual payment request
// to ensure that a card is actually valid
func (s *Service) isValidCard(card ebs_fields.CacheCards) (bool, error) {
	var dbCard ebs_fields.Card
	if res := s.Db.Where("pan = ?", card.Pan).First(&dbCard); res.Error == nil {
		// if the card made it to the db this means it's a valid card
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
	if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
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

func (s *Service) GetIpinPubKey() error {
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
	s.Db.Table("transactions").Create(&res.EBSResponse)
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
	user, err := ebs_fields.GetUserByMobile(mobile, s.Db)
	if err != nil {
		s.Logger.Printf("Error finding user: %v", err)
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	var notifications []PushData
	s.Db.Where("user_mobile = ?", user.Mobile).Find(&notifications)
	if err := c.Status(http.StatusOK).JSON(notifications); err != nil {
		return err
	}
	var pushdata PushData
	pushdata.UpdateIsRead(mobile, s.Db)
	return nil
}

// GetUser returns the user profile object for the currently logged in user
func (s *Service) GetUser(c *fiber.Ctx) error {
	mobile := getMobile(c)

	var profile ebs_fields.UserProfile
	if res := s.Db.Model(&ebs_fields.User{}).Where("mobile = ?", mobile).First(&profile); res.Error != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": res.Error.Error(), "code": "database_error"})
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
	user, err := ebs_fields.GetUserByMobile(mobile, s.Db)
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "database_error"})
	}
	var tmpUser ebs_fields.User
	if res := s.Db.Where("username = ?", profile.Username).First(&tmpUser); res.Error == nil && tmpUser.Mobile != user.Mobile {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "duplication_error", "message": "username already exists"})
	}
	user.Fullname = profile.Fullname
	user.Username = profile.Username
	user.Email = profile.Email
	user.Birthday = profile.Birthday
	user.Gender = profile.Gender
	if err := ebs_fields.UpdateUser(user, s.Db); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "database_error", "message": err.Error()})
	}
	return c.Status(http.StatusOK).JSON(fiber.Map{"result": "ok"})
}

func (s *Service) GetUserLanguage(c *fiber.Ctx) error {
	mobile := getMobile(c)
	user, err := ebs_fields.GetUserByMobile(mobile, s.Db)
	if err != nil {
		s.Logger.Printf("ERROR: could not get user from by mobile: %v", err.Error())
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "database_error"})
	}
	return c.Status(http.StatusOK).JSON(fiber.Map{"language": user.Language})
}

func (s *Service) SetUserLanguage(c *fiber.Ctx) error {
	mobile := getMobile(c)
	language := c.Query("language")
	user, err := ebs_fields.GetUserByMobile(mobile, s.Db)
	if err != nil {
		s.Logger.Printf("ERROR: could not get user from by mobile: %v", err.Error())
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "database_error"})
	}
	if language == "" {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "You must set a language", "code": "client_error"})
	}
	user.Language = language
	if err := ebs_fields.UpdateUser(user, s.Db); err != nil {
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
	if err := ebs_fields.UpdateUserWithKYC(s.Db, &request); err != nil {
		return ctx.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	// Return a success message
	return ctx.Status(http.StatusOK).JSON(fiber.Map{"message": "KYC created successfully", "code": "ok"})
}
