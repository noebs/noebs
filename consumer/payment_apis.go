// Package consumer provides services for EBS consumer APIs, and custom
// apis we have developed as well.
// It offers a seamless and unified api to be used for both
// merchant pos and mobile payment
/*
EBS Services


- Get Balance

- Working Key

- Is Alive

- Card transfer

- Billers

Special Payment

Special Payment is a secure way to tokenize payments for external service providers through a custom url
link. The URL is only valid once and it cannot be reused.

Workflow

Here's how the system works.

- Generate a payment token (/consumer/generate_token)

Parameters:
- amount

- biller id

It will return a new response with a UUID that to be used by the client's users for payment.

- Special payment (/consumer/special_payment/:UUID)

Parameters:

- tranAmount (the same as before!)

- billerId

- ConsumerServices payload

It will return 400 ONLY if the amount OR the biller id didn't match the specified UUID in the system


Examples

cURL:

1. Generate Token
curl -X POST https://api.soluspay.net/api/v1/payment_token -d '{"amount": 10}'

> {"result":{"amount":10,"uuid":"6eb3ae20-ecbc-4603-b079-ed98549cf9f2"},"uuid":"6eb3ae20-ecbc-4603-b079-ed98549cf9f2"}

2. Inquire token via UUID
curl -X GET https://api.soluspay.net/api/v1/payment/6eb3ae20-ecbc-4603-b079-ed98549cf9f2 -d '{"amount": 10}'

3. Complete Payment
curl -X POST https://api.soluspay.net/api/v1/payment/6eb3ae20-ecbc-4603-b079-ed98549cf9f2 -d '{"amount": 10}'


NOTE that in payment inquiry we use GET method, while we use POST for payment

* Note authentication might be added to this API

PIN Block

Please advice with ebs documentations about iPIN block encryption. You can cite these locations for iPIN implementation:

- https://github.com/adonese/donates (JS)
- https://github.com/adonese/noebs-wasm (GO)
- https://github.com/adonese/cashq (Java)

*/
package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/adonese/noebs/apperr"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/adonese/noebs/utils"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/noebs/ipin"
	"github.com/sirupsen/logrus"
)

// we use a simple string to store the ipin key and reuse it across noebs.
var ebsIpinEncryptionKey string

// Service consumer for utils.Service struct
type Service struct {
	Store       *store.Store
	NoebsConfig ebs_fields.NoebsConfig
	Logger      *logrus.Logger
	Auth        Auther
}

var fees = ebs_fields.NewDynamicFeesWithDefaults()

func (s *Service) recordTransaction(ctx context.Context, tenantID string, res ebs_fields.EBSResponse) error {
	if tenantID == "" {
		tenantID = store.DefaultTenantID
	}
	return s.Store.CreateTransaction(ctx, tenantID, res)
}

// Purchase performs special payment api from ebs consumer services
// It requires: card info (src), amount fields, specialPaymentId (destination)
// in order to complete the transaction
func (s *Service) Purchase(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerPurchaseEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerPurchaseFields{}
	bindingErr := bindJSON(c, &fields)
	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})
	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		fields.DynamicFees = fees.SpecialPaymentFees
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)
		res.Name = s.ToDatabasename(url)
		tenantID := resolveTenantID(c, s.NoebsConfig)
		if err := s.recordTransaction(c.UserContext(), tenantID, res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.EBSResponse, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// IsAlive performs isAlive request to inquire for ebs server availability
func (s *Service) IsAlive(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerIsAliveEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerIsAliveFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		//// mask the pan
		res.MaskPAN()
		res.Name = s.ToDatabasename(url)

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// BillPayment is responsible for utility, telecos, e-government and other payment services
func (s *Service) BillPayment(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerBillPaymentEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerBillPaymentFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		deviceID := fields.DeviceID
		fields.ConsumerCommonFields.DelDeviceID()
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		res.MaskPAN()

		res.Name = s.ToDatabasename(url)

		// Adding BillType, BillTo and BillInfo2 so that the mobile client can show these fields in transactions history
		res.EBSResponse.BillTo = res.PaymentInfo
		d, err := json.Marshal(res.BillInfo)
		if err != nil {
			s.Logger.Println("Error in marshalling:", err)
		} else {
			res.EBSResponse.BillInfo2 = string(d)
		}
		switch res.PayeeID {
		case "0010010001", "0010010003", "0010010005":
			res.EBSResponse.BillType = "Telecom TopUp"
		case "0010010002", "0010010004", "0010010006":
			res.EBSResponse.BillType = "Telecom Bill Payment"
		case "0010030002", "0010030004":
			res.EBSResponse.BillType = "Education"
		case "0010030003":
			res.EBSResponse.BillType = "Customs"
		case "0010050001":
			res.EBSResponse.BillType = "Government E-15"
		case "0010020001":
			res.EBSResponse.BillType = "Electricity"
		}

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		// This is for push notification
		var data PushData
		data.Type = EBS_NOTIFICATION
		data.Date = res.CreatedAt.Unix()
		data.CallToAction = CTA_BILL_PAYMENT
		data.EBSData = res.EBSResponse
		data.UUID = fields.UUID
		data.DeviceID = deviceID

		if ebsErr != nil {
			// This is for push notifications (failure)
			data.Title = "Payment Failure"
			data.EBSData.PAN = fields.Pan // Changing the masked PAN with the unmasked one.
			data.Body = fmt.Sprintf("Payment failed due to: %v.", res.ResponseMessage)
			tranData <- data

			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			// This is for push notifications (success)
			data.Title = "Payment Success"
			data.EBSData.PAN = fields.Pan // Changing the masked PAN with the unmasked one.
			data.UUID = fields.UUID

			switch res.PayeeID {
			case "0010010001", "0010010002", "0010010003", "0010010004", "0010010005", "0010010006": // telecom
				phone := "0" + res.PaymentInfo[7:]
				data.Phone = phone
				data.Body = fmt.Sprintf("You have received %v %v on your phone: %v.", res.TranAmount, res.AccountCurrency, phone)
				tranData <- data
				data.Body = fmt.Sprintf("You have sent %v %v to phone: %v successfully.", res.TranAmount, res.AccountCurrency, phone)
				data.Phone = ""
			case "0010030002": // mohe
				data.Body = fmt.Sprintf("%v %v has been paid successfully for Education.", res.TranAmount, res.AccountCurrency)
			case "0010030004": // mohe arab
				// TODO: This case NEED to be tested
				phone := strings.Split(res.PaymentInfo, "/")[1][10:]
				data.Phone = phone
				data.Body = fmt.Sprintf("%v %v has been paid successfully for Education.", res.TranAmount, res.AccountCurrency)
				tranData <- data
				data.Phone = ""
			case "0010030003": // Customs
				data.Body = fmt.Sprintf("%v %v has been paid successfully for Customs.", res.TranAmount, res.AccountCurrency)
			case "0010050001": // e-15
				data.Body = fmt.Sprintf("%v %v has been paid successfully for E-15.", res.TranAmount, res.AccountCurrency)
			case "0010020001": // electricity
				meter := res.PaymentInfo[6:]
				data.Body = fmt.Sprintf("%v %v has been paid successfully for Electricity Meter No. %v", res.TranAmount, res.AccountCurrency, meter)
			}

			tranData <- data

			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}
	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// GetBills for any EBS supported bill just by the entityID (phone number or the invoice ID). A good abstraction over EBS
// services. The function also updates a local database for each result for subsequent queries.
func (s *Service) GetBills(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerBillInquiryEndpoint
	var b bills
	if bindingErr := bindJSON(c, &b); bindingErr != nil {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": bindingErr.Error()})
	}

	uid, _ := uuid.NewRandom()
	var fields ebs_fields.ConsumerBillInquiryFields
	fields.ApplicationId = s.NoebsConfig.ConsumerID
	fields.UUID = uid.String()
	updatePaymentInfo(&fields, b)
	fields.PayeeId = b.PayeeID
	ipinBlock, err := ipin.Encrypt(s.NoebsConfig.EBSConsumerKey, s.NoebsConfig.BillInquiryIPIN, uid.String())
	if err != nil {
		s.Logger.Printf("error in encryption: %v", err)
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
	}
	fields.ConsumerCardHolderFields.Ipin = ipinBlock
	fields.ConsumerCardHolderFields.Pan = s.NoebsConfig.BillInquiryPAN
	fields.ConsumerCardHolderFields.ExpDate = s.NoebsConfig.BillInquiryExpDate
	fields.ConsumerCommonFields.TranDateTime = ebs_fields.EbsDate()
	cacheBills := ebs_fields.CacheBillers{Mobile: b.Phone, BillerID: b.PayeeID}
	tenantID := resolveTenantID(c, s.NoebsConfig)
	// Get our cache results before hand
	if oldCache, err := s.Store.GetCacheBiller(c.UserContext(), tenantID, b.Phone); err == nil { // we have stored this phone number before
		fields.PayeeId = oldCache.BillerID // use the data we stored previously
		cacheBills.BillerID = oldCache.BillerID
	}
	jsonBuffer, err := json.Marshal(fields)
	if err != nil {
		// there's an error in parsing the struct. Server error.
		er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
	}
	// the only part left is fixing EBS errors. Formalizing them per se.
	code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
	s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)
	// mask the pan
	res.MaskPAN()
	res.Name = s.ToDatabasename(url)
	if err := s.recordTransaction(c.UserContext(), tenantID, res.EBSResponse); err != nil {
		logrus.WithFields(logrus.Fields{
			"code":    "unable to migrate purchase model",
			"message": err,
		}).Info("error in migrating purchase model")
	}
	if ebsErr != nil {
		cacheBills.BillerID = flipBillerID(cacheBills.BillerID)
		_ = s.Store.UpsertCacheBiller(c.UserContext(), tenantID, cacheBills.Mobile, cacheBills.BillerID)
		payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
		jsonResponse(c, code, payload)
	} else {
		due, err := parseDueAmounts(fields.PayeeId, res.BillInfo)
		if err != nil {
			// hardcoded
			cacheBills.BillerID = flipBillerID(cacheBills.BillerID)
			_ = s.Store.UpsertCacheBiller(c.UserContext(), tenantID, cacheBills.Mobile, cacheBills.BillerID)
			payload := ebs_fields.ErrorDetails{Code: 502, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, 502, payload)
			return
		}
		jsonResponse(c, code, fiber.Map{"ebs_response": res, "due_amount": due})
		_ = s.Store.UpsertCacheBiller(c.UserContext(), tenantID, cacheBills.Mobile, cacheBills.BillerID)
	}
}

// Register with card allow a user to register through noebs and assigning a card to them
func (s *Service) RegisterWithCard(c *fiber.Ctx) {
	var card ebs_fields.CacheCards
	bindJSON(c, &card)
	// why are we checking for card.PublicKey and card.Mobile here?
	tenantID := resolveTenantID(c, s.NoebsConfig)
	if ok, err := s.isValidCard(c.UserContext(), tenantID, card); !ok || card.PublicKey == "" || card.Mobile == "" {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "invalid_card_or_missing_credentials"})
		return
	}
	// Make sure user is unique
	var user *ebs_fields.User
	tmpUser, err := s.Store.GetUserByMobile(c.UserContext(), tenantID, card.Mobile)
	if err == nil && tmpUser.IsVerified {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "User with this mobile number already exists"})
		return
	}
	if err == nil {
		user = tmpUser
	} else {
		user = &ebs_fields.User{Mobile: card.Mobile, Username: card.Mobile}
	}
	user.Fullname = card.Name
	user.MainCard = card.Pan
	user.ExpDate = card.Expiry
	user.Password = card.Password
	user.PublicKey = card.PublicKey
	_ = user.HashPassword()
	key, err := user.GenerateOtp()
	if err != nil {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "bad_request"})
		return
	}
	if user.ID == 0 {
		if err := s.Store.CreateUser(c.UserContext(), tenantID, user); err != nil {
			jsonResponse(c, http.StatusInternalServerError, fiber.Map{"code": "database_error", "message": "could not create user"})
			return
		}
	} else {
		if err := s.Store.UpdateUser(c.UserContext(), tenantID, user); err != nil {
			jsonResponse(c, http.StatusInternalServerError, fiber.Map{"code": "database_error", "message": "could not update user"})
			return
		}
	}
	ucard := card.NewCardFromCached(int(user.ID))
	ucard.ID = 0
	ucard.IsMain = true
	_ = s.Store.AddCards(c.UserContext(), tenantID, user.ID, []ebs_fields.Card{ucard})
	jsonResponse(c, http.StatusOK, fiber.Map{"result": "ok"})
	go utils.SendSMS(&s.NoebsConfig, utils.SMS{Mobile: card.Mobile, Message: fmt.Sprintf("Your one-time access code is: %s. DON'T share it with anyone.", key)})
}

// BillerID retrieves a billerID from noebs and performs an ebs request
// if a phone number doesn't exist in our system
func (s *Service) GetBiller(c *fiber.Ctx) {
	var mobile string
	mobile = c.Query("mobile")
	if mobile == "" {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "empty_mobile", "code": "empty_mobile"})
		return
	}
	tenantID := resolveTenantID(c, s.NoebsConfig)
	guessed, err := s.Store.GetCacheBiller(c.UserContext(), tenantID, mobile)
	if err != nil {
		// we don't know about this
		// what if we go CRAZY here and launch a new request to EBS!
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "no record", "code": "empty_mobile"})
		go s.billerID(c.UserContext(), tenantID, mobile) // we are launching a go routine here to update this query for future reference
		return
	}
	jsonResponse(c, http.StatusOK, fiber.Map{"biller_id": guessed.BillerID})
}

// BillInquiry for telecos, utility and government (billers inquiries)
func (s *Service) BillInquiry(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerBillInquiryEndpoint
	var fields = ebs_fields.ConsumerBillInquiryFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		// Save to Redis lis

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})

		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// Balance gets performs get balance transaction for the provided card info
func (s *Service) Balance(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerBalanceEndpoint
	var fields = ebs_fields.ConsumerBalanceFields{}
	bindingErr := bindJSON(c, &fields)
	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})
	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		res.Name = s.ToDatabasename(url)

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}
		s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse)
	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// TransactionStatus queries EBS to get the status of the transaction
func (s *Service) TransactionStatus(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerTransactionStatusEndpoint
	var fields = ebs_fields.ConsumerTransactionStatusFields{}
	bindingErr := bindJSON(c, &fields)
	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})
	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		// this is really extremely a complex case
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)
		res.Name = s.ToDatabasename(url)
		s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse)
		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res.OriginalTransaction})
		}
	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

func (s *Service) storeLastTransactions(ctx context.Context, merchantID string, res *ebs_fields.EBSParserFields) error {
	_ = ctx
	_ = merchantID
	_ = res
	return nil
}

// WorkingKey get ebs working key for encrypting ipin for consumer transactions
func (s *Service) WorkingKey(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerWorkingKeyEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerWorkingKeyFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res, "fees": fees})

		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// CardTransfer performs p2p transactions
func (s *Service) CardTransfer(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCardTransferEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerCardTransferAndMobileFields{}
	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		fields.DynamicFees = fees.CardTransferfees
		deviceID := fields.DeviceID
		fields.ConsumerCommonFields.DelDeviceID()
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrMarshal, err.Error()))
			return
		}
		s.Logger.Printf("the request is: %v", string(jsonBuffer))
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		res.EBSResponse.SenderPAN = utils.MaskPAN(fields.Pan)
		res.EBSResponse.ReceiverPAN = utils.MaskPAN(fields.ToCard)

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		// This is for push notifications
		var data PushData
		data.Type = EBS_NOTIFICATION
		data.Date = res.CreatedAt.Unix()
		data.Title = "Card Transfer"
		data.CallToAction = CTA_CARD_TRANSFER
		data.EBSData = res.EBSResponse
		data.UUID = fields.UUID
		data.DeviceID = deviceID

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			// This is for push notifications (sender)
			data.EBSData.PAN = fields.Pan
			data.Body = fmt.Sprintf("Card Transfer failed due to: %v.", res.ResponseMessage)
			tranData <- data

			jsonResponse(c, code, payload)
		} else {
			// This is for push notifications (receiver)
			data.EBSData.PAN = fields.ToCard

			data.Body = fmt.Sprintf("You have received %v %v from %v.", fields.TranAmount, res.AccountCurrency, res.PAN)
			tranData <- data

			// This is for push notifications (sender)
			data.EBSData.PAN = fields.Pan
			data.Body = fmt.Sprintf("%v %v has been transferred successfully from your account to %v.", fields.TranAmount, res.AccountCurrency, res.ToCard)
			tranData <- data

			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// CashIn performs cash in transactions
func (s *Service) CashIn(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCashInEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerCashInFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrMarshal, err.Error()))
			return
		}
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// CashIn performs cash in transactions
func (s *Service) QRMerchantRegistration(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerQRGenerationEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerQRRegistration{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrMarshal, err.Error()))
			return
		}
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// CashOut performs cashout transactions
func (s *Service) CashOut(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCashOutEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerCashoOutFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrMarshal, err.Error()))
			return
		}
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url) // rename me to cashin transaction

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// AccountTransfer performs p2p transactions
func (s *Service) AccountTransfer(c *fiber.Ctx) {

	url := s.NoebsConfig.ConsumerIP + ebs_fields.AccountTransferEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumrAccountTransferFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})
	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrMarshal, err.Error()))
			return
		}
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()
		res.Name = s.ToDatabasename(url)
		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}
		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// IPinChange changes the ipin for the card holder provided card
func (s *Service) IPinChange(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerChangeIPinEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerIPinFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})

		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// Status get transactions status from ebs
func (s *Service) Status(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerStatusEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerStatusFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})
	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)
		// mask the pan
		res.MaskPAN()
		res.Name = s.ToDatabasename(url)
		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}
		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// QRPayment performs QR payment transaction. This is EBS-based QR transaction, and to be confused with noebs one
func (s *Service) QRPayment(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerQRPaymentEndpoint // EBS simulator endpoint url goes here.

	var fields = ebs_fields.ConsumerQRPaymentFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})

		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// QRRefund performs qr refund transaction
func (s *Service) QRTransactions(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.MerchantTransactionStatus // EBS simulator endpoint url goes here.
	var fields = ebs_fields.ConsumerQRStatus{}
	bindingErr := bindJSON(c, &fields)
	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			// also store value to redis

			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			s.storeLastTransactions(c.UserContext(), fields.MerchantID, &res)
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// QRRefund performs qr refund transaction
func (s *Service) QRRefund(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerQRRefundEndpoint // EBS simulator endpoint url goes here.

	var fields = ebs_fields.ConsumerQRRefundFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// QRRefund performs qr refund transaction
func (s *Service) QRComplete(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerComplete // EBS simulator endpoint url goes here.

	var fields = ebs_fields.ConsumerQRCompleteFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// QRGeneration generates a qr token for the registered merchant
func (s *Service) QRGeneration(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerQRGenerationEndpoint // EBS simulator endpoint url goes here.

	var fields = ebs_fields.MerchantRegistrationFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})

		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// GenerateIpin generates a new ipin for card holder
func (s *Service) GenerateIpin(c *fiber.Ctx) {
	url := s.NoebsConfig.IPIN + ebs_fields.IPinGeneration // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerGenerateIPin{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		uid, _ := uuid.NewRandom()
		// encrypt the password here
		s.Logger.Printf("ipin password is: %v", s.NoebsConfig.EBSIPINPassword)

		ipinBlock, err := ipin.Encrypt(s.NoebsConfig.EBSIpinKey, s.NoebsConfig.EBSIPINPassword, uid.String())
		if err != nil {
			s.Logger.Printf("error in encryption: %v", err)
			jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_request", "message": err.Error()})
		}
		fields.Username = s.NoebsConfig.EBSIPINUsername
		fields.Password = ipinBlock
		fields.UUID = uid.String()

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			ebsIpinEncryptionKey = ""
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// CompleteIpin performs an otp check from ebs to complete ipin change transaction
func (s *Service) CompleteIpin(c *fiber.Ctx) {
	url := s.NoebsConfig.IPIN + ebs_fields.IPinCompletion // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerGenerateIPinCompletion{}

	bindJSON(c, &fields)
	s.Logger.Printf("ipin password is: %v", s.NoebsConfig.EBSIPINPassword)
	uid, _ := uuid.NewRandom()
	passwordBlock, _ := ipin.Encrypt(s.NoebsConfig.EBSIpinKey, s.NoebsConfig.EBSIPINPassword, uid.String())
	ipinBlock, _ := ipin.Encrypt(s.NoebsConfig.EBSIpinKey, fields.Ipin, uid.String())
	otp, _ := ipin.Encrypt(s.NoebsConfig.EBSIpinKey, fields.Otp, uid.String())
	fields.Password = passwordBlock
	fields.Ipin = ipinBlock
	fields.Otp = otp
	fields.UUID = uid.String()

	fields.Username = s.NoebsConfig.EBSIPINUsername

	jsonBuffer, err := json.Marshal(fields)
	if err != nil {
		// there's an error in parsing the struct. Server error.
		er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
	}

	// the only part left is fixing EBS errors. Formalizing them per se.
	code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
	s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)
	// mask the pan
	res.MaskPAN()

	res.Name = s.ToDatabasename(url)
	tenantID := resolveTenantID(c, s.NoebsConfig)
	_ = s.recordTransaction(c.UserContext(), tenantID, res.EBSResponse)

	if ebsErr != nil {
		ebsIpinEncryptionKey = ""
		payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
		jsonResponse(c, code, payload)
	} else {
		jsonResponse(c, code, fiber.Map{"ebs_response": res})
	}
}

// CompleteIpin performs an otp check from ebs to complete ipin change transaction
func (s *Service) IPINKey(c *fiber.Ctx) {
	url := s.NoebsConfig.IPIN + ebs_fields.QRPublicKey // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	s.Logger.Printf("EBS url is: %v", url)

	var fields = ebs_fields.ConsumerGenerateIPINFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			// store the key somewhere
			ebsIpinEncryptionKey = res.PubKeyValue
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// GeneratePaymentToken is used by noebs user to charge their customers.
// the toCard field in `Token` uses a masked PAN (first 6 digits and last 4 digits and any number of * in between)
func (s *Service) GeneratePaymentToken(c *fiber.Ctx) {
	var token ebs_fields.Token
	mobile := getMobile(c)
	bindJSON(c, &token)
	tenantID := resolveTenantID(c, s.NoebsConfig)
	user, err := s.Store.GetCardsOrFail(c.UserContext(), tenantID, mobile)
	if err != nil {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": err.Error()})
		return
	}

	if len(user.Cards) < 1 && token.ToCard == "" {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "no card found"})
		return
	}
	fullPan, err := ebs_fields.ExpandCard(token.ToCard, user.Cards)
	if err != nil {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "no_card_found", "message": err.Error()})
		return
	}
	token.ToCard = fullPan
	token.UUID = uuid.New().String()
	token.UserID = user.ID
	token.User = *user
	if err := s.Store.CreateToken(c.UserContext(), tenantID, &token); err != nil {
		s.Logger.Printf("error in saving payment token: %v", err)
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": err.Error(), "message": "Unable to save payment token"})
		return
	}
	encoded, _ := ebs_fields.Encode(&token)
	s.Logger.Printf("token is: %v", encoded)
	paymentLink := s.NoebsConfig.PaymentLinkBase + token.UUID
	jsonResponse(c, http.StatusCreated, fiber.Map{"token": encoded, "result": encoded, "uuid": token.UUID, "payment_link": paymentLink})
}

func (s *Service) PaymentRequest(c *fiber.Ctx) {
	mobile := getMobile(c)

	type PRData struct {
		Mobile string `json:"mobile,omitempty"`
		ToCard string `json:"toCard,omitempty"`
		Amount int    `json:"amount,omitempty"`
	}

	var data PRData
	err := bindJSON(c, &data)
	if err != nil {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "binding_error", "message": err.Error()})
		return
	}

	tenantID := resolveTenantID(c, s.NoebsConfig)
	sender, err := s.Store.GetCardsOrFail(c.UserContext(), tenantID, mobile)
	if err != nil {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "database_error", "message": err.Error()})
		return
	}

	receiver, err := s.Store.GetUserByMobile(c.UserContext(), tenantID, data.Mobile)
	if err != nil {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "database_error", "message": err.Error()})
		return
	}

	if len(sender.Cards) < 1 && data.ToCard == "" {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "no card found"})
		return
	}

	fullPan, err := ebs_fields.ExpandCard(data.ToCard, sender.Cards)
	if err != nil {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "no_card_found", "message": err.Error()})
		return
	}

	var token ebs_fields.Token
	token.ToCard = fullPan
	token.Amount = data.Amount
	token.UUID = uuid.New().String()
	token.UserID = sender.ID
	token.User = *sender
	if err := s.Store.CreateToken(c.UserContext(), tenantID, &token); err != nil {
		s.Logger.Printf("error in saving payment token: %v", err)
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": err.Error(), "message": "Unable to save payment token"})
		return
	}
	encoded, _ := ebs_fields.Encode(&token)
	paymentLink := s.NoebsConfig.PaymentLinkBase + token.UUID

	name := sender.Fullname
	if name == "" {
		name = sender.Mobile
	}
	// This is for push notification
	var pData PushData
	pData.Type = NOEBS_NOTIFICATION
	pData.Date = time.Now().Unix()
	pData.CallToAction = CTA_REQUEST_FUNDS
	pData.UUID = token.UUID
	pData.DeviceID = receiver.DeviceID
	pData.Title = "Payment Request"
	pData.Body = fmt.Sprintf("%v has requested %v SDG from you.", name, token.Amount)
	pData.Phone = data.Mobile
	pData.UserMobile = data.Mobile
	pData.PaymentRequest = ebs_fields.QrData{UUID: token.UUID, ToCard: token.ToCard, Amount: token.Amount}
	tranData <- pData
	jsonResponse(c, http.StatusCreated, fiber.Map{"token": encoded, "result": encoded, "uuid": token.UUID, "payment_link": paymentLink})
}

// GetPaymentToken retrieves a generated payment token by UUID
// This service should be accessed via an authorization header
func (s *Service) GetPaymentToken(c *fiber.Ctx) {
	username := getMobile(c)
	if username == "" {
		ve := validationError{Message: "Empty payment id", Code: "empty_uuid"}
		jsonResponse(c, http.StatusBadRequest, ve)
		return
	}
	tenantID := resolveTenantID(c, s.NoebsConfig)
	user, err := s.Store.GetUserByMobile(c.UserContext(), tenantID, username)
	if err != nil {
		ve := validationError{Message: "user doesn't exist", Code: "record_not_found"}
		jsonResponse(c, http.StatusBadRequest, ve)
		return
	}
	uuid := c.Query("uuid")
	if uuid == "" { // the user wants to enlist *all* tokens generated for them
		tokens, err := s.Store.GetAllTokensByUserID(c.UserContext(), tenantID, user.ID)
		if err != nil {
			ve := validationError{Message: "error in retrieving tokens", Code: "error_retrieving_tokens"}
			jsonResponse(c, http.StatusBadRequest, ve)
			return
		}
		for _, token := range tokens {
			token.ToCard = utils.MaskPAN(token.ToCard)
		}
		jsonResponse(c, http.StatusOK, fiber.Map{"token": tokens, "count": len(tokens)})
		return
	}
	result, err := s.Store.GetTokenByUUID(c.UserContext(), tenantID, uuid)
	if err != nil {
		jsonResponse(c, http.StatusNotFound, fiber.Map{"code": "record_not_found", "message": "token not found"})
		return
	}

	// Masking the PAN
	result.ToCard = utils.MaskPAN(result.ToCard)
	jsonResponse(c, http.StatusOK, result)
}

// NoebsQuickPayment performs a QR or payment via url transaction
// The api should be like this, and it should work for both the mobile and the web clients
// The very unique thing about the full payment token is that it is self-containted, the implmenter
// doesn't have to do an http call to inquire about its fields
// but, let's walkthrough the process, should we used a uuid of the payment token instead.
// - a user will click on an item
// - the app or web will make a call to generate a payment token
// - and it will return a link and a payment token. the link, or noebs link is only valid in the case of
// noebs' vendors payments (e.g., Solus or tuti): in that, it cannot work for the case of ecommerce
// - there are two cases for using the endpoint:
// - using the full-token should render the forms to show the details of the token (toCard, amount, and any comments)
// - using the uuid only, should be followed by the client performing a request to get the token info
// request body fields should always take precendents over query params
func (s *Service) NoebsQuickPayment(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCardTransferEndpoint

	// those should be nil, and assumed to be sent in the request body -- that's fine.
	uuid := c.Query("uuid")
	// token has serious security issues as it exposes the payment card info
	// in the "to" field.
	token := c.Query("token")
	var noebsToken ebs_fields.Token

	var data ebs_fields.QuickPaymentFields
	bindJSON(c, &data) // ignore the errors
	tenantID := resolveTenantID(c, s.NoebsConfig)
	paymentToken, err := ebs_fields.Decode(data.EncodedPaymentToken)
	if err != nil {
		// you should now work on the uuid and token
		if t, err := ebs_fields.Decode(token); err == nil {
			noebsToken = t
		} else {
			if t, err := s.Store.GetTokenByUUID(c.UserContext(), tenantID, uuid); err == nil {
				noebsToken = *t
			}
		}
	} else {
		// we are getting paymentToken from the request
		noebsToken = paymentToken
	}
	storedToken, err := s.Store.GetTokenByUUID(c.UserContext(), tenantID, noebsToken.UUID)
	if err != nil {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "bad_token", "message": err.Error()})
		return
	}

	if storedToken.Amount != 0 && storedToken.Amount != int(data.TranAmount) {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "amount_mismatch", "message": "amount_mismatch"})
		return
	}
	data.ApplicationId = s.NoebsConfig.ConsumerID
	data.ToCard = storedToken.ToCard
	data.TranAmount = float32(noebsToken.Amount)
	code, res, ebsErr := ebs_fields.EBSHttpClient(url, data.MarshallP2pFields())
	res.EBSResponse.SenderPAN = data.Pan
	res.EBSResponse.ReceiverPAN = storedToken.ToCard
	if err := s.recordTransaction(c.UserContext(), tenantID, res.EBSResponse); err != nil {
		s.Logger.Printf("Error saving transactions: %v", err.Error())
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"code": err.Error(), "message": "unable_to_save_transaction"})
	}
	if ebsErr == nil {
		_ = s.Store.MarkTokenPaid(c.UserContext(), tenantID, storedToken.UUID)
	}

	go pushMessage(fmt.Sprintf("Amount of: %v was added! Download noebs apps!", res.EBSResponse.TranAmount))
	if ebsErr != nil {
		payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
		jsonResponse(c, code, payload)
	} else {
		jsonResponse(c, code, fiber.Map{"ebs_response": res})
	}
	billerChan <- billerForm{EBS: res.EBSResponse, IsSuccessful: ebsErr == nil, Token: data.UUID}
}

// EbsGetCardInfo get card holder name from pan. Currently is limited to telecos only
func (s *Service) EbsGetCardInfo(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCardInfo // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerCardInfoFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// GetMSISDNFromCard for ussd to get pan info from sim card
func (s *Service) GetMSISDNFromCard(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerPANFromMobile // EBS simulator endpoint url goes here.
	var fields = ebs_fields.ConsumerPANFromMobileFields{}
	bindingErr := bindJSON(c, &fields)
	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})
	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)
		// mask the pan
		res.MaskPAN()
		res.Name = s.ToDatabasename(url)
		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}
		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}
	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// QRPayment performs QR payment transaction
func (s *Service) RegisterCard(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerRegister // EBS simulator endpoint url goes here.

	var fields = ebs_fields.ConsumerRegistrationFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})

		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// CompleteRegistration step 2 in card issuance process
func (s *Service) CompleteRegistration(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCompleteRegistration // EBS simulator endpoint url goes here.

	var fields = ebs_fields.ConsumerCompleteRegistrationFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// if no errors, then proceed to creating a new user
		tenantID := resolveTenantID(c, s.NoebsConfig)
		user := ebs_fields.User{Mobile: fields.Mobile, Username: fields.Mobile}
		user.Password = fields.NoebsPassword
		user.HashPassword()
		user.SanitizeName()
		if err := s.Store.CreateUser(c.UserContext(), tenantID, &user); err != nil {
			jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": "database_error", "message": err.Error()})
			return
		}

		fields.NoebsPassword = ""
		fields.Mobile = ""

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		res.Name = s.ToDatabasename(url)

		if err := s.recordTransaction(c.UserContext(), tenantID, res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			jsonResponse(c, code, fiber.Map{"ebs_response": res})
			// Associate the card to that user
			// create a card here
			card := ebs_fields.CacheCards{Pan: res.PAN, Expiry: res.ExpDate}

			// now, it is better to store this card as a cached card
			_ = s.Store.UpsertCacheCard(c.UserContext(), tenantID, card)

			// we associated the newly created card to its owner
			_ = s.Store.AddCards(c.UserContext(), tenantID, user.ID, []ebs_fields.Card{card.NewCardFromCached(int(user.ID))})
		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

// QRPayment performs QR payment transaction
func (s *Service) GenerateVoucher(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerGenerateVoucher // EBS simulator endpoint url goes here.

	var fields = ebs_fields.ConsumerGenerateVoucherFields{}

	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		deviceID := fields.DeviceID
		fields.ConsumerCommonFields.DelDeviceID()
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		// This is for push notifications
		var data PushData
		data.Type = EBS_NOTIFICATION
		data.Date = res.CreatedAt.Unix()
		data.Title = "Voucher Generation"
		data.CallToAction = CTA_VOUCHER
		data.EBSData = res.EBSResponse
		data.UUID = fields.UUID
		data.EBSData.PAN = fields.Pan
		data.DeviceID = deviceID

		if ebsErr != nil {
			// This is for push notifications (sender)
			data.Body = fmt.Sprintf("Voucher generation failed due to: %v.", res.ResponseMessage)
			tranData <- data

			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			jsonResponse(c, code, payload)
		} else {
			// This is for push notifications (sender)
			data.Body = fmt.Sprintf("Voucher number generated for phone %v is %v", fields.VoucherNumber, res.VoucherCode)
			tranData <- data

			jsonResponse(c, code, fiber.Map{"ebs_response": res})

		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

func (s *Service) CheckUser(c *fiber.Ctx) {
	type checkUserRequest struct {
		Phones []string `json:"phones"`
	}

	type checkUserResponse struct {
		Phone  string `json:"phone"`
		IsUser bool   `json:"is_user"`
		Pan    string `json:"PAN"`
	}

	var request checkUserRequest
	var response []checkUserResponse

	if err := bindJSON(c, &request); err != nil {
		s.Logger.Printf("The request is wrong. %v", err)
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": "Bad request.", "code": "bad_request"})
		return
	}

	for _, phone := range request.Phones {
		tenantID := resolveTenantID(c, s.NoebsConfig)
		user, err := s.Store.GetUserByMobile(c.UserContext(), tenantID, phone)
		if err != nil {
			response = append(response, checkUserResponse{Phone: phone, IsUser: false})
			continue
		}
		// Returning the masked pan of the user that exists (this is convenient
		// for the omnibox)
		pan := user.MainCard
		if pan == "" {
			userCards, err := s.Store.GetCardsOrFail(c.UserContext(), tenantID, phone)
			if err != nil {
				s.Logger.Printf("Error getting user cards: %v", err)
				// We will not return this user because they don't have any
				// cards (in our case this will not be useful for frontent)
				continue
			}
			// GetCardsOrFail returns the main card as the first one
			pan = userCards.Cards[0].Pan
		}
		var maskedPan string
		// Here we try to make this function backward compatible with the
		// database; in the beginning of the application the rule of having
		// every registered card be correct was not enforced like now, for
		// the purpose of testing of course, and this resulted in many
		// cards that exist in the database not having a pan which will
		// cause a runtime error if we don't skip them. This issue will not
		// face new users.
		if pan != "" {
			maskedPan = utils.MaskPAN(pan)
		}
		response = append(response, checkUserResponse{Phone: phone, IsUser: true, Pan: maskedPan})
	}
	jsonResponse(c, http.StatusOK, response)
}

func (s *Service) SetMainCard(c *fiber.Ctx) {
	type Card struct {
		Pan string `json:"PAN"`
	}
	mobile := getMobile(c)
	tenantID := resolveTenantID(c, s.NoebsConfig)
	user, err := s.Store.GetUserByMobile(c.UserContext(), tenantID, mobile)
	if err != nil {
		s.Logger.Printf("Error finding user in db: %v", err)
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"error": "Error finding user in the database"})
		return
	}

	var card Card
	err = bindJSON(c, &card)
	if err != nil {
		s.Logger.Printf("Binding error: %v", err)
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"error": "Binding error, make sure the sent json is correct"})
		return
	}

	if ok, err := s.Store.CardExists(c.UserContext(), tenantID, card.Pan); err != nil || !ok {
		s.Logger.Println("Card does not exist")
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"error": "Card does not exist"})
		return
	}
	if err := s.Store.SetMainCard(c.UserContext(), tenantID, user.ID, card.Pan); err != nil {
		s.Logger.Printf("Error updating cards: %v", err)
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"error": "Could not save card as main card"})
		return
	}
	if err := s.Store.UpdateUserColumns(c.UserContext(), tenantID, user.ID, map[string]any{"main_card": card.Pan}); err != nil {
		s.Logger.Printf("Error updating user.Pan: %v", err)
		jsonResponse(c, http.StatusInternalServerError, fiber.Map{"error": "Could not save card as main card"})
		return
	}

	jsonResponse(c, http.StatusOK, fiber.Map{"result": "ok"})
}

func (s *Service) MobileTransfer(c *fiber.Ctx) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCardTransferEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerMobileTransferFields{}
	bindingErr := bindJSON(c, &fields)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		jsonResponse(c, http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		tenantID := resolveTenantID(c, s.NoebsConfig)
		user, err := s.Store.GetUserByMobile(c.UserContext(), tenantID, fields.Mobile)
		if err != nil {
			s.Logger.Printf("Error getting user from db: %v", err)
			jsonResponse(c, http.StatusBadRequest, fiber.Map{"error": "error getting user from db, make sure mobile is correct"})
			return
		}
		fields.ToCard = user.MainCard

		fields.ApplicationId = s.NoebsConfig.ConsumerID
		fields.DynamicFees = fees.CardTransferfees
		deviceID := fields.DeviceID
		fields.ConsumerCommonFields.DelDeviceID()
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			jsonResponse(c, 0, apperr.Wrap(err, apperr.ErrMarshal, err.Error()))
			return
		}
		s.Logger.Printf("the request is: %v", string(jsonBuffer))
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)

		if err := s.recordTransaction(c.UserContext(), resolveTenantID(c, s.NoebsConfig), res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"code":    "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		// This is for push notifications
		var data PushData
		data.Type = EBS_NOTIFICATION
		data.Date = res.CreatedAt.Unix()
		data.Title = "Card Transfer"
		data.CallToAction = CTA_CARD_TRANSFER
		data.EBSData = res.EBSResponse
		data.UUID = fields.UUID
		data.DeviceID = deviceID

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			// This is for push notifications (sender)
			data.EBSData.PAN = fields.Pan
			data.Body = fmt.Sprintf("Card Transfer failed due to: %v", res.ResponseMessage)
			tranData <- data

			jsonResponse(c, code, payload)
		} else {
			// This is for push notifications (receiver)
			data.EBSData.PAN = fields.ToCard

			data.Body = fmt.Sprintf("You have received %v %v from %v", res.AccountCurrency, fields.TranAmount, res.PAN)
			tranData <- data

			// This is for push notifications (sender)
			data.EBSData.PAN = fields.Pan
			data.Body = fmt.Sprintf("%v %v has been transferred from your account to %v", res.AccountCurrency, fields.TranAmount, res.ToCard)
			tranData <- data

			jsonResponse(c, code, fiber.Map{"ebs_response": res})
		}

	default:
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"code": bindingErr.Error()})
	}
}

func (s *Service) GetTransactions(c *fiber.Ctx) {
	mobile := getMobile(c)
	tenantID := resolveTenantID(c, s.NoebsConfig)
	user, err := s.Store.GetCardsOrFail(c.UserContext(), tenantID, mobile)
	if err != nil {
		jsonResponse(c, http.StatusBadRequest, fiber.Map{"message": err.Error(), "code": "database_error"})
		return
	}

	var trans []ebs_fields.EBSResponse
	for _, card := range user.Cards {
		// Mask cards and perform the query for each card
		uMaskedPan := utils.MaskPAN(card.Pan)
		cardTrans, err := s.Store.GetTransactionsByMaskedPan(c.UserContext(), tenantID, uMaskedPan)
		if err == nil {
			trans = append(trans, cardTrans...)
		}
	}

	jsonResponse(c, http.StatusOK, trans)
}
