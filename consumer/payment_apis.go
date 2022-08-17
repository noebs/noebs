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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	firebase "firebase.google.com/go/v4"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/utils"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	"github.com/go-redis/redis/v7"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

//Service consumer for utils.Service struct
type Service struct {
	Redis       *redis.Client
	Db          *gorm.DB
	NoebsConfig ebs_fields.NoebsConfig
	Logger      *logrus.Logger
	FirebaseApp *firebase.App
	Auth        Auther
}

var fees = ebs_fields.NewDynamicFees()

//Purchase performs special payment api from ebs consumer services
// It requires: card info (src), amount fields, specialPaymentId (destination)
// in order to complete the transaction
func (s *Service) Purchase(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerPurchaseEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerPurchaseFields{}
	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)
	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})
	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		fields.DynamicFees = fees.SpecialPaymentFees
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)
		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.EBSResponse, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//IsAlive performs isAlive request to inquire for ebs server availability
func (s *Service) IsAlive(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerIsAliveEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerIsAliveFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		//// mask the pan
		res.MaskPAN()
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)
		res.Name = s.ToDatabasename(url)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//BillPayment is responsible for utility, telecos, e-government and other payment services
func (s *Service) BillPayment(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerBillPaymentEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerBillPaymentFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//BillInquiry for telecos, utility and government (billers inquiries)
func (s *Service) BillInquiry(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerBillInquiryEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerBillInquiryFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		// Save to Redis lis
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//Balance gets performs get balance transaction for the provided card info
func (s *Service) Balance(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerBalanceEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerBalanceFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//Balance gets performs get balance transaction for the provided card info
func (s *Service) TransactionStatus(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerTransactionStatusEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerTransactionStatusFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		// this is really extremely a complex case
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {

			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res.OriginalTransaction})

		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

func (s *Service) storeLastTransactions(merchantID string, res *ebs_fields.EBSParserFields) error {
	// this stores LastTransactions to redis
	// marshall the lastTransactions
	// store them into redis
	// store the lastTransactions into the database
	s.Logger.Printf("merchantID is: %s", merchantID)
	if res == nil {
		return errors.New("empty response")
	}
	// parse the last transactions
	data, err := json.Marshal(res.LastTransactions)
	if err != nil {
		return err
	}
	if _, err := s.Redis.HSet(merchantID, "data", data).Result(); err != nil {
		s.Logger.Printf("erorr in redis: %v", err)
		return err
	}
	return nil
}

//WorkingKey get ebs working key for encrypting ipin for consumer transactions
func (s *Service) WorkingKey(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerWorkingKeyEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerWorkingKeyFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res, "fees": fees})

		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//CardTransfer performs p2p transactions
func (s *Service) CardTransfer(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCardTransferEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerCardTransferAndMobileFields{}
	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		fields.DynamicFees = fees.CardTransferfees
		// save this to redis
		if mobile := fields.Mobile; mobile != "" {
			s.Redis.Set(fields.Mobile+":pan", fields.Pan, 0)
		}

		jsonBuffer := fields.MustMarshal()
		s.Logger.Printf("the request is: %v", string(jsonBuffer))
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//CashIn performs cash in transactions
func (s *Service) CashIn(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCashInEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerCashInFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer := fields.MustMarshal() // this part basically gets us into trouble
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//CashIn performs cash in transactions
func (s *Service) QRMerchantRegistration(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerQRGenerationEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerQRRegistration{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, _ := json.Marshal(fields) // this part basically gets us into trouble
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//CashOut performs cashout transactions
func (s *Service) CashOut(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCashOutEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerCashoOutFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer := fields.MustMarshal()
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url) // rename me to cashin transaction
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//AccountTransfer performs p2p transactions
func (s *Service) AccountTransfer(c *gin.Context) {

	url := s.NoebsConfig.ConsumerIP + ebs_fields.AccountTransferEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumrAccountTransferFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, _ := json.Marshal(fields)
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//IPinChange changes the ipin for the card holder provided card
func (s *Service) IPinChange(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerChangeIPinEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerIPinFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//Status get transactions status from ebs
func (s *Service) Status(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerStatusEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerStatusFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//Transactions get transactions stored in our redis data store
func (s *Service) Transactions(c *gin.Context) {
	//TODO get the transaction from Redis instanc

	username := c.GetString("mobile")
	if username == "" {
		username = "invalid_key"
	}
	s.Redis.Get(username)

	// you should probably marshal these data
	c.JSON(http.StatusOK, gin.H{"transactions": username})
}

//QRPayment performs QR payment transaction
func (s *Service) QRPayment(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerQRPaymentEndpoint // EBS simulator endpoint url goes here.

	var fields = ebs_fields.ConsumerQRPaymentFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//QRRefund performs qr refund transaction
func (s *Service) QRTransactions(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.MerchantTransactionStatus // EBS simulator endpoint url goes here.

	var fields = ebs_fields.ConsumerQRStatus{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			// also store value to redis

			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			s.storeLastTransactions(fields.MerchantID, &res)
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//QRRefund performs qr refund transaction
func (s *Service) QRRefund(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerQRRefundEndpoint // EBS simulator endpoint url goes here.

	var fields = ebs_fields.ConsumerQRRefundFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//QRRefund performs qr refund transaction
func (s *Service) QRComplete(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerComplete // EBS simulator endpoint url goes here.

	var fields = ebs_fields.ConsumerQRCompleteFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//QRGeneration generates a qr token for the registered merchant
func (s *Service) QRGeneration(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerQRGenerationEndpoint // EBS simulator endpoint url goes here.

	var fields = ebs_fields.MerchantRegistrationFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//GenerateIpin generates a new ipin for card holder
func (s *Service) GenerateIpin(c *gin.Context) {
	url := s.NoebsConfig.QRIP + ebs_fields.IPinGeneration // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerGenerateIPin{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//CompleteIpin performs an otp check from ebs to complete ipin change transaction
func (s *Service) CompleteIpin(c *gin.Context) {
	url := s.NoebsConfig.QRIP + ebs_fields.IPinCompletion // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerGenerateIPinCompletion{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//CompleteIpin performs an otp check from ebs to complete ipin change transaction
func (s *Service) IPINKey(c *gin.Context) {
	url := s.NoebsConfig.QRIP + ebs_fields.QRPublicKey // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	s.Logger.Printf("EBS url is: %v", url)

	var fields = ebs_fields.ConsumerGenerateIPINFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//GeneratePaymentToken is used by noebs user to charge their customers.
func (s *Service) GeneratePaymentToken(c *gin.Context) {
	var token ebs_fields.PaymentToken
	mobile := c.GetString("mobile")
	user, err := ebs_fields.GetUserCards(mobile, s.Db)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": err.Error()})
		return
	}
	if err := c.ShouldBindWith(&token, binding.JSON); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": err.Error(), "message": "Invalid request"})
		return
	}

	if len(user.Cards) < 1 && token.ToCard == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": "no card found"})
		return
	}

	token.UUID = uuid.New().String()
	token.UserID = user.ID
	if token.ToCard == "" {
		// Only override card if the user didn't explicity specify a card
		token.ToCard = user.Cards[0].Pan
	}

	if err := user.SavePaymentToken(&token); err != nil {
		s.Logger.Printf("error in saving payment token: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"code": err.Error(), "message": "Unable to save payment token"})
		return
	}
	encoded, _ := ebs_fields.Encode(&token)
	s.Logger.Printf("token is: %v", encoded)
	c.JSON(http.StatusCreated, gin.H{"token": encoded, "result": encoded, "uuid": token.UUID})
}

//GetPaymentToken retrieves a generated payment token by UUID
// This service should be accessed via an authorization header
func (s *Service) GetPaymentToken(c *gin.Context) {
	username := c.GetString("mobile")
	if username == "" {
		ve := validationError{Message: "Empty payment id", Code: "empty_uuid"}
		c.JSON(http.StatusBadRequest, ve)
		return
	}
	user, err := ebs_fields.NewUserByMobile(username, s.Db)
	if err != nil {
		ve := validationError{Message: "user doesn't exist", Code: "record_not_found"}
		c.JSON(http.StatusBadRequest, ve)
		return
	}
	uuid, _ := c.GetQuery("uuid")
	if uuid == "" { // the user wants to enlist *all* tokens generated for them
		tokens, err := ebs_fields.GetUserTokens(user.Mobile, s.Db)
		if err != nil {
			ve := validationError{Message: "error in retrieving tokens", Code: "error_retrieving_tokens"}
			c.JSON(http.StatusBadRequest, ve)
			return
		}
		c.JSON(http.StatusOK, gin.H{"token": tokens, "count": len(tokens)})
		return
	}
	result, _ := ebs_fields.GetTokenByUUID(uuid, s.Db)
	c.JSON(http.StatusOK, gin.H{"token": result, "count": 1})
}

func (s *Service) NoebsQuickPayment(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCardTransferEndpoint

	var data ebs_fields.QuickPaymentFields
	if err := c.ShouldBindWith(&data, binding.JSON); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": err.Error(), "message": "bad_request"})
		return
	}
	paymentToken, err := ebs_fields.Decode(data.EncodedPaymentToken)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": err.Error(), "message": "bad_request"})
		return
	}
	storedToken, err := ebs_fields.GetTokenByUUID(paymentToken.UUID, s.Db)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": err.Error(), "message": "token_not_found"})
		return
	}
	if storedToken.Amount != 0 && storedToken.Amount != paymentToken.Amount {
		c.JSON(http.StatusBadRequest, gin.H{"code": "amount_mismatch", "message": "amount_mismatch"})
		return
	}

	code, res, ebsErr := ebs_fields.EBSHttpClient(url, data.MarshallP2pFields())

	storedToken.Transaction = res.EBSResponse
	storedToken.IsPaid = ebsErr == nil
	if err := storedToken.UpsertTransaction(res.EBSResponse, storedToken.UUID); err != nil {
		s.Logger.Printf("error in saving transaction: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"code": err.Error(), "message": "unable_to_save_transaction"})
		return
	}
	go pushMessage(fmt.Sprintf("Amount of: %v was added! Download noebs apps!", res.EBSResponse.TranAmount))
	if ebsErr != nil {
		payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
		c.JSON(code, payload)
	} else {
		c.JSON(code, storedToken)
	}
	billerChan <- billerForm{EBS: res.EBSResponse, IsSuccessful: ebsErr == nil, Token: data.UUID}
}

//EbsGetCardInfo get card holder name from pan. Currently is limited to telecos only
func (s *Service) EbsGetCardInfo(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCardInfo // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerCardInfoFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}

}

//GetMSISDNFromCard for ussd to get pan info from sim card
func (s *Service) GetMSISDNFromCard(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerPANFromMobile // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = ebs_fields.ConsumerPANFromMobileFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}

}

//QRPayment performs QR payment transaction
func (s *Service) RegisterCard(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerRegister // EBS simulator endpoint url goes here.

	var fields = ebs_fields.ConsumerRegistrationFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//CompleteRegistration step 2 in card issuance process
func (s *Service) CompleteRegistration(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCompleteRegistration // EBS simulator endpoint url goes here.

	var fields = ebs_fields.ConsumerCompleteRegistrationFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		// except for this api, don't mask the card!
		// res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}

//QRPayment performs QR payment transaction
func (s *Service) GenerateVoucher(c *gin.Context) {
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerGenerateVoucher // EBS simulator endpoint url goes here.

	var fields = ebs_fields.ConsumerGenerateVoucherFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		fields.ApplicationId = s.NoebsConfig.ConsumerID
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		s.Logger.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		res.Name = s.ToDatabasename(url)
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&res.EBSResponse); err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err,
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": bindingErr.Error()})
	}
}
