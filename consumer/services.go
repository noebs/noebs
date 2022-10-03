package consumer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/utils"
	"github.com/google/uuid"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-redis/redis/v7"
	"github.com/noebs/ipin"
	"github.com/sirupsen/logrus"
)

const (
	SMS_GATEWAY = "https://mazinhost.com/smsv1/sms/api?action=send-sms"
)

// CardFromNumber gets the gussesed associated mobile number to this pan
func (s *Service) CardFromNumber(c *gin.Context) {
	// the user must submit in their mobile number *ONLY*, and it is get
	q, ok := c.GetQuery("mobile_number")
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"message": "mobile number is empty", "code": "empty_mobile_number"})
		return
	}
	// now search through redis for this mobile number!
	// first check if we have already collected that number before
	pan, err := s.Redis.Get(q + ":pan").Result()
	if err == nil {
		c.JSON(http.StatusOK, gin.H{"result": pan})
		return
	}
	username, err := s.Redis.Get(q).Result()
	if err == redis.Nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "No user with such mobile number", "code": "mobile_number_not_found"})
		return
	}
	if pan, ok := utils.PanfromMobile(username, s.Redis); ok {
		c.JSON(http.StatusOK, gin.H{"result": pan})
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"message": "No user with such mobile number", "code": "mobile_number_not_found"})
	}

}

// GetCards Get all cards for the currently authorized user
func (s *Service) GetCards(c *gin.Context) {
	username := c.GetString("mobile")
	if username == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		return
	}
	userCards, err := ebs_fields.GetUserCards(username, s.Db)
	if err != nil {
		// handle the error somehow
		logrus.WithFields(logrus.Fields{
			"error":   "unable to get results from redis",
			"message": err.Error(),
		}).Info("unable to get results from redis")
		c.JSON(http.StatusOK, gin.H{"cards": nil, "main_card": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{"cards": userCards.Cards, "main_card": userCards.Cards[0]})
}

// AddCards Allow users to add card to their profile
// if main_card was set to true, then it will be their main card AND
// it will remove the previously selected one FIXME
func (s *Service) AddCards(c *gin.Context) {
	var listCards []ebs_fields.Card
	username := c.GetString("mobile")
	if err := c.ShouldBindBodyWith(&listCards, binding.JSON); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "bad_request", "message": err})
		return
	}
	user, err := ebs_fields.NewUserByMobile(username, s.Db)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "bad_request", "message": err})
		return
	}

	// manually zero-valueing card ID to avoid gorm upserting it
	for idx := range listCards {
		listCards[idx].ID = 0
		listCards[idx].UserID = user.ID
	}
	if err := user.UpsertCards(listCards); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "bad_request", "message": err})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": "ok", "message": "cards added"})
}

// EditCard allow authorized users to edit their cards (e.g., edit pan / expdate)
// this updates any card via
func (s *Service) EditCard(c *gin.Context) {
	var req ebs_fields.Card
	err := c.ShouldBindWith(&req, binding.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error(), "code": "unmarshalling_error"})
		return
	}
	username := c.GetString("mobile")
	if username == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		return
	}
	// If no ID was provided that means we are adding a new card. We don't want that!
	if req.CardIdx == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "card idx is empty", "code": "card_idx_empty"})
		return
	}
	user, err := ebs_fields.NewUserByMobile(username, s.Db)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error(), "code": "database_error"})
		return
	}
	req.UserID = user.ID
	if err := ebs_fields.UpdateCard(req, s.Db); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "database_error", "message": err})
		return
	} else {
		c.JSON(http.StatusCreated, gin.H{"result": "ok"})
		return
	}
}

// RemoveCard allow authorized users to remove their card
// when the send the card id (from its list in app view)
func (s *Service) RemoveCard(c *gin.Context) {
	username := c.GetString("mobile")
	if username == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		return
	}
	var card ebs_fields.Card
	err := c.ShouldBindWith(&card, binding.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error(), "code": "unmarshalling_error"})
		return
	}
	s.Logger.Printf("the card is: %v+#", card)
	// If no ID was provided that means we are adding a new card. We don't want that!
	if card.CardIdx == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "card idx is empty", "code": "card_idx_empty"})
		return
	}

	user, err := ebs_fields.NewUserByMobile(username, s.Db)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error(), "code": "unmarshalling_error"})
		return
	}
	card.UserID = user.ID
	if err := ebs_fields.DeleteCard(card, s.Db); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "database_error", "message": err})
		return
	} else {
		c.JSON(http.StatusCreated, gin.H{"result": "ok"})
		return
	}
}

// NecToName gets an nec number from the context and maps it to its meter number
func (s *Service) NecToName(c *gin.Context) {
	if nec := c.Query("nec"); nec != "" {
		name, err := s.Redis.HGet("meters", nec).Result()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": "No user found with this NEC", "code": "nec_not_found"})
		} else {
			c.JSON(http.StatusOK, gin.H{"result": name})
		}
	}
}

var billerChan = make(chan billerForm)

// BillerHooks submits results to external endpoint
func BillerHooks() {

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
func (s *Service) PaymentOrder() gin.HandlerFunc {
	return func(c *gin.Context) {
		mobile := c.GetString("mobile")
		var req ebs_fields.PaymentToken
		token, _ := uuid.NewRandom()
		user, err := ebs_fields.GetUserCards(mobile, s.Db)
		if err != nil {
			log.Printf("error in retrieving card: %v", err)
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": "bad_request", "message": err.Error()})
		}

		// there shouldn't be any error here, but still
		if err := c.ShouldBindBodyWith(&req, binding.JSON); err != nil {
			log.Printf("error in retrieving card: %v", err)
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": "bad_request", "message": err.Error()})
		}
		ipinBlock, err := ipin.Encrypt("MFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBANx4gKYSMv3CrWWsxdPfxDxFvl+Is/0kc1dvMI1yNWDXI3AgdI4127KMUOv7gmwZ6SnRsHX/KAM0IPRe0+Sa0vMCAwEAAQ==", user.Cards[0].IPIN, token.String())
		if err != nil {
			log.Printf("error in encryption: %v", err)
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": "bad_request", "message": err.Error()})
		}
		data := ebs_fields.ConsumerCardTransferFields{
			ConsumerCommonFields: ebs_fields.ConsumerCommonFields{
				ApplicationId: "ACTSCon",
				TranDateTime:  "022821135300",
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
		// Modify gin's context to update the request body
		c.Request.Body = ioutil.NopCloser(bytes.NewBuffer(updatedRequest))
		c.Request.ContentLength = int64(len(updatedRequest))
		c.Request.Header.Set("Content-Type", "application/json")

		// Call the next handler
		c.Next()

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
func (s *Service) CashoutPub() {
	pubsub := s.Redis.Subscribe("chan_cashouts")

	// Wait for confirmation that subscription is created before publishing anything.
	_, err := pubsub.Receive()
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

func (s *Service) pubSub(channel string, message interface{}) {
	pubsub := s.Redis.Subscribe(channel)

	// Wait for confirmation that subscription is created before publishing anything.
	_, err := pubsub.Receive()
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
		}
		return b, nil
	case "0010010006": //sudani
		if t, ok := paymentInfo["billAmount"].(string); ok {
			b.Amount = t
			b.DueAmount = t
		}

		return b, nil
	case "0055555555": // e-invoice
		b.Amount = paymentInfo["amount_due"].(string)
		b.DueAmount = paymentInfo["amount_due"].(string)
		b.MinAmount = paymentInfo["minAmount"].(string)
	case "0010030002": // mohe
		b.Amount = paymentInfo["dueAmount"].(string)
		b.DueAmount = paymentInfo["dueAmount"].(string)
		return b, nil
	case "0010030004": // mohe-arab
		b.Amount = paymentInfo["dueAmount"].(string)
		b.DueAmount = paymentInfo["dueAmount"].(string)
		return b, nil
	case "0010030003": // Customs
		b.Amount = paymentInfo["AmountToBePaid"].(string)
		b.DueAmount = paymentInfo["AmountToBePaid"].(string)
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
