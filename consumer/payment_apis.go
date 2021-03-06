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
	"fmt"
	"net/http"
	"strings"

	"github.com/adonese/noebs/dashboard"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/merchant"
	"github.com/adonese/noebs/utils"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

var log = logrus.New()

//Service consumer for utils.Service struct
type Service struct {
	utils.Service
}

//BillChan it is used to asyncronysly parses ebs response to get and assign values to the billers
// such as assigning the name to utility personal payment info
var BillChan = make(chan ebs_fields.EBSParserFields)

//Purchase performs special payment api from ebs consumer services
// It requires: card info (src), amount fields, specialPaymentId (destination)
// in order to complete the transaction
func (s *Service) Purchase(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerPurchaseEndpoint // EBS simulator endpoint url goes here.
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
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.PurchaseTransaction
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.GenericEBSResponseFields, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": bindingErr.Error()})
	}
}

//IsAlive performs isAlive request to inquire for ebs server availability
func (s *Service) IsAlive(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerIsAliveEndpoint // EBS simulator endpoint url goes here.
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

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		//// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		transaction.EBSServiceName = ebs_fields.PurchaseTransaction

		if err := s.Db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": bindingErr.Error()})
	}
}

//BillPayment is responsible for utility, telecos, e-government and other payment services
func (s *Service) BillPayment(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerBillPaymentEndpoint // EBS simulator endpoint url goes here.
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
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.PurchaseTransaction
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": bindingErr.Error()})
	}
}

//BillInquiry for telecos, utility and government (billers inquiries)
func (s *Service) BillInquiry(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerBillInquiryEndpoint // EBS simulator endpoint url goes here.
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

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.PurchaseTransaction

		if err := s.Db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
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
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": bindingErr.Error()})
	}
}

//Balance gets performs get balance transaction for the provided card info
func (s *Service) Balance(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerBalanceEndpoint // EBS simulator endpoint url goes here.
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

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.PurchaseTransaction
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": bindingErr.Error()})
	}
}

//WorkingKey get ebs working key for encrypting ipin for consumer transactions
func (s *Service) WorkingKey(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerWorkingKeyEndpoint // EBS simulator endpoint url goes here.
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

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.PurchaseTransaction
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": bindingErr.Error()})
	}
}

//CardTransfer performs p2p transactions
func (s *Service) CardTransfer(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerCardTransferEndpoint // EBS simulator endpoint url goes here.
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

		// save this to redis
		if mobile := fields.Mobile; mobile != "" {
			s.Redis.Set(fields.Mobile+":pan", fields.Pan, 0)
		}
		jsonBuffer := fields.MustMarshal()
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.PurchaseTransaction
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": bindingErr.Error()})
	}
}

//AccountTransfer performs p2p transactions
func (s *Service) AccountTransfer(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.AccountTransferEndpoint // EBS simulator endpoint url goes here.
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

		jsonBuffer := fields.MustMarshal()
		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.PurchaseTransaction
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": bindingErr.Error()})
	}
}

//IPinChange changes the ipin for the card holder provided card
func (s *Service) IPinChange(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerChangeIPinEndpoint // EBS simulator endpoint url goes here.
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

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.PurchaseTransaction
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": bindingErr.Error()})
	}
}

//Status get transactions status from ebs
func (s *Service) Status(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerStatusEndpoint // EBS simulator endpoint url goes here.
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

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.PurchaseTransaction
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": bindingErr.Error()})
	}
}

//Transactions get transactions stored in our redis data store
func (s *Service) Transactions(c *gin.Context) {
	//TODO get the transaction from Redis instanc

	username := c.GetString("username")
	if username == "" {
		username = "invalid_key"
	}
	s.Redis.Get(username)

	// you should probably marshal these data
	c.JSON(http.StatusOK, gin.H{"transactions": username})
}

//QRPayment performs QR payment transaction
func (s *Service) QRPayment(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerQRPaymentEndpoint // EBS simulator endpoint url goes here.

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

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.PurchaseTransaction
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": bindingErr.Error()})
	}
}

//QRRefund performs qr refund transaction
func (s *Service) QRRefund(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerQRRefundEndpoint // EBS simulator endpoint url goes here.

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

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.PurchaseTransaction
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": bindingErr.Error()})
	}
}

//QRGeneration generates a qr token for the registered merchant
func (s *Service) QRGeneration(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerQRGenerationEndpoint // EBS simulator endpoint url goes here.

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

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.PurchaseTransaction
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": bindingErr.Error()})
	}
}

//GenerateIpin generates a new ipin for card holder
func (s *Service) GenerateIpin(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.IPinGeneration // EBS simulator endpoint url goes here.
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
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.PurchaseTransaction
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": bindingErr.Error()})
	}
}

//CompleteIpin performs an otp check from ebs to complete ipin change transaction
func (s *Service) CompleteIpin(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.IPinCompletion // EBS simulator endpoint url goes here.
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
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.PurchaseTransaction
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": bindingErr.Error()})
	}
}

//GeneratePaymentToken generates a token
// BUG(adonese) we have to make it mandatory for biller id as well
//BUG(adonese) still not fixed -- but we really should fix it
//TODO(adonese) #94 API key should be mandatory
func (s *Service) GeneratePaymentToken(c *gin.Context) {
	var t paymentTokens
	t.redisClient = s.Redis

	// make authorization for which to make calls
	// if err := c.ShouldBindJSON(&t); err != nil {
	// 	ve := validationError{Message: err.Error(), Code: "required_fields_missing"}
	// 	c.JSON(http.StatusBadRequest, ve)
	// 	return
	// }

	// I'm gonna need to know and check namespace
	// This is a workaround for sahil payment. Will deprecated in the future. And all of sahil's specifics.

	var namespace string
	if namespace = c.Param("payment"); namespace == "" {
		namespace = "sahil_wallet"
	}

	if err := t.NewToken(namespace); err != nil {
		ve := validationError{Message: err.Error(), Code: "unable to get the result"}
		c.JSON(http.StatusBadRequest, ve)
		return
	}

	if err := t.addToken(namespace, t.UUID); err != nil {
		log.Printf("Error in addToken: %v", err)
		ve := validationError{Message: err.Error(), Code: "unable to get the result"}
		c.JSON(http.StatusBadRequest, ve)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"result": t, "uuid": t.UUID})
}

//UpdateCashout to register a new cashout biller in noebs
func (s *Service) UpdateCashout(c *gin.Context) {

	var t paymentTokens
	t.redisClient = s.Redis

	var csh cashoutFields

	if err := c.ShouldBindJSON(&csh); err != nil {
		ve := validationError{Message: err.Error(), Code: "csh_err"}
		c.JSON(http.StatusBadRequest, ve)
		return
	}
	log.Printf("the request is: %#v", csh)

	if csh.Name == "" {
		ve := validationError{Message: "Empty namespace", Code: "csh_err"}
		c.JSON(http.StatusBadRequest, ve)
		return
	}
	if err := t.UpdateCashOut(csh.Name, csh); err != nil {
		ve := validationError{Message: err.Error(), Code: "csh_err"}
		c.JSON(http.StatusBadRequest, ve)
		return
	}

	c.JSON(http.StatusOK, gin.H{"profile": csh})
	return
}

//RegisterCashout to register a new cashout biller in noebs
func (s *Service) RegisterCashout(c *gin.Context) {

	var t paymentTokens
	t.redisClient = s.Redis

	var csh cashoutFields

	if err := c.ShouldBindJSON(&csh); err != nil {
		ve := validationError{Message: err.Error(), Code: "required_fields_missing"}
		c.JSON(http.StatusBadRequest, ve)
		return
	}

	if err := t.AddCashOut(csh.Name, csh); err != nil {
		ve := validationError{Message: err.Error(), Code: "csh_err"}
		c.JSON(http.StatusBadRequest, ve)
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": "ok"})
	return
}

//GenerateCashoutClaim generates a new cashout claim. Used by merchant to issue a new claim
func (s *Service) GenerateCashoutClaim(c *gin.Context) {
	var t paymentTokens
	t.redisClient = s.Redis

	var csh cashout

	if err := c.ShouldBindJSON(&csh); err != nil {
		ve := validationError{Message: err.Error(), Code: "required_fields_missing"}
		c.JSON(http.StatusBadRequest, ve)
		return
	}

	namespace := c.Param("biller")
	if namespace == "" {
		ve := validationError{Message: "namespace not provided", Code: "required_fields_missing"}
		c.JSON(http.StatusBadRequest, ve)
		return
	}

	t.ID = csh.ID
	t.Amount = float32(csh.Amount)

	id, err := t.NewCashout(namespace)
	if err != nil {
		log.Printf("Error in addToken: %v", err)
		ve := validationError{Message: err.Error(), Code: "unable to get the result"}
		c.JSON(http.StatusBadRequest, ve)
		return
	}
	if err := t.setAmount(namespace, id, csh.Amount); err != nil {
		log.Printf("Error in addToken: %v", err)
		ve := validationError{Message: err.Error(), Code: "unable to set amount"}
		c.JSON(http.StatusBadRequest, ve)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"result": id, "uuid": id})
}

//CashoutClaims used by merchants to make a cashout
func (s *Service) CashoutClaims(c *gin.Context) {
	var t paymentTokens
	t.redisClient = s.Redis

	var csh cashout

	ns := c.Param("biller")
	if err := c.ShouldBindJSON(&csh); err != nil {
		ve := validationError{Message: err.Error(), Code: "required_fields_missing"}
		c.JSON(http.StatusBadRequest, ve)
		return
	}

	log.Printf("the id is: %v", csh.ID)
	t.ID = csh.ID
	t.Amount = float32(csh.Amount)
	// this block here can be made into a func
	{
		if !t.cshExists(ns, csh.ID) {
			ve := validationError{Message: "namespace/id doesn't exist", Code: "required_fields_missing"}
			c.JSON(http.StatusBadRequest, ve)
			return
		}

		if t.isDone(ns, t.ID) {
			ve := validationError{Message: "payment done", Code: "duplicate_claim"}
			c.JSON(http.StatusBadRequest, ve)
			return
		}

		t.markDone(ns, csh.ID)
	}

	// am gonna need to send in more data here
	// i don't want to take a whopping penis with me
	tt := paymentTokens{redisClient: t.redisClient}
	go tt.cashOutClaims(ns, csh.ID, csh.Card) // this should work. eh?

	c.JSON(http.StatusOK, gin.H{"result": "ok"})
}

//GetPaymentToken retrieves a generated payment token by ID (UUID)
func (s *Service) GetPaymentToken(c *gin.Context) {
	id := c.Param("uuid")
	if id == "" {
		ve := validationError{Message: "Empty payment id", Code: "empty_uuid"}
		c.JSON(http.StatusBadRequest, ve)
		return
	}

	//BUG(adonese) safe but unclean; should be fixed. As reliable as the holding handler is
	var t paymentTokens
	t.redisClient = s.Redis

	if ok, err := t.GetToken(id); !ok {
		ve := validationError{Message: err.Error(), Code: "payment_token_not_found"}
		c.JSON(http.StatusBadRequest, ve)
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": []paymentTokens{t}})

}

// SpecialPayment is a new api to allow for site users to securely request payment
// it is really just a payment request with:
// - tran amount
// - payee id
// later we can add more features such as expiration for the token and more.
func (s *Service) SpecialPayment(c *gin.Context) {
	// get token
	// get token service from redis
	// perform the payment
	// /consumer/payment/uuid

	// BUG(adonese) find a way to get hooks and to from here.
	url := ebs_fields.EBSIp + ebs_fields.ConsumerPurchaseEndpoint

	provider := c.Param("uuid")
	//why hardcoding it here?

	if strings.Contains(c.Request.URL.Host, "sahil.soluspay.net") {
		provider = "biller:sahil"
	}

	var queries specialPaymentQueries

	log.Printf(provider)

	if err := c.BindQuery(&queries); err != nil {
		if !queries.IsJSON {
			c.Redirect(http.StatusMovedPermanently, queries.Referer+"?fail=true&code=empty_refId")
			return
		}
		ve := validationError{Message: "Binding error", Code: "empty_refId"}
		c.JSON(http.StatusBadRequest, ve)
		return
	}

	log.Printf("What is isJson: %v", queries)

	var t paymentTokens
	t.redisClient = s.Redis

	// TODO(adonese): perform a check here and assign token to biller id
	if ok, err := t.ValidateToken(queries.Token, provider); !ok && err != nil {
		if !queries.IsJSON {
			c.Redirect(http.StatusMovedPermanently, queries.Referer+"?fail=true&code=token_not_found")
			return
		}
		ve := validationError{Message: "Invalid token", Code: err.Error()}
		c.JSON(http.StatusBadRequest, ve)
		return
	}

	m := merchant.New(s.Db)
	data, err := m.ByID(provider)
	if err != nil {
		if !queries.IsJSON {
			c.Redirect(http.StatusMovedPermanently, queries.Referer+"?fail=true&code=not_biller_provided")
			return
		}
		ve := validationError{Message: "Biller ID not activated", Code: err.Error()}
		c.JSON(http.StatusBadRequest, ve)
		return
	}

	var hooks, referer string

	if m.Hooks != "" {
		hooks = m.Hooks
	} else {
		hooks = queries.HooksURL
	}

	if m.URL != "" {
		referer = m.URL
	} else {
		referer = queries.Referer
	}

	var p interface{}
	var bill ebs_fields.ConsumerPurchaseFields

	var pp ebs_fields.ConsumerCardTransferFields

	if data.CardNumber == "" {
		bill.ServiceProviderId = data.EBSBiller
		p = bill
		url = ebs_fields.EBSIp + ebs_fields.ConsumerCardTransferEndpoint
	} else {
		pp.ToCard = data.CardNumber
		p = pp
	}

	log.Printf("the data in p is: %#v", p)
	if err := c.ShouldBindJSON(&p); err != nil {
		log.Printf("error in parsing: %v", err)
		if !queries.IsJSON {
			c.Redirect(http.StatusMovedPermanently, referer+"?fail=true&code="+err.Error())
			return
		}
		ve := validationError{Message: err.Error(), Code: "validation_error"}
		c.JSON(http.StatusBadRequest, ve)
		return
	}

	// necessary to invalidate key after issuance
	t.invalidate(queries.Token)

	// perform the payment
	req, _ := json.Marshal(&p)

	code, res, ebsErr := ebs_fields.EBSHttpClient(url, req)

	var isSuccess bool
	if ebsErr == nil {
		isSuccess = true
	}
	// mask the pan
	res.MaskPAN()
	pt := &billerForm{ID: queries.ID, EBS: res.GenericEBSResponseFields, Token: queries.Token, IsSuccessful: isSuccess}

	t.addTrans(provider, pt)

	transaction := dashboard.Transaction{
		GenericEBSResponseFields: res.GenericEBSResponseFields,
	}

	transaction.EBSServiceName = "special_payment"
	//FIXME #73 attempting to write a read-only database
	s.Db.Table("transactions").Create(&transaction)

	// we send a push request here...
	// TODO(adonese): make a function to generate texts here
	go t.pushMessage(fmt.Sprintf("Amount of: %v was added! Download Cashq!", transaction.TranAmount), m.PushID)
	if ebsErr != nil {

		// We don't return here since we gotta send the billerForm at the end of the function?
		//TODO
		/*
			could we do something like this:
			go func(){
				billerChan <- billerForm{EBS: res.GenericEBSResponseFields, ID: refID, IsSuccessful: isSuccess, Token: id}
			}()
		*/
		if !queries.IsJSON {
			c.Redirect(http.StatusMovedPermanently, referer+"?fail=true&code="+res.ResponseMessage)
			return
		}
		payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
		c.JSON(code, payload)

	} else {
		isSuccess = true
		if !queries.IsJSON {
			c.Redirect(http.StatusMovedPermanently, referer+"?fail=true&code="+res.ResponseMessage)
		}
		c.JSON(code, gin.H{"ebs_response": res})
	}
	// post request. Send the response to the handler
	// FIXME(adonese) #91 allow for generic use of noebs hooks.
	billerChan <- billerForm{to: hooks, EBS: res.GenericEBSResponseFields, ID: queries.ID, IsSuccessful: isSuccess, Token: queries.Token} //THIS BLOCKS IF THE goroutin is not listening
}

//EbsGetCardInfo get card holder name from pan. Currently is limited to telecos only
func (s *Service) EbsGetCardInfo(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerCardInfo // EBS simulator endpoint url goes here.
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

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.PurchaseTransaction
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": bindingErr.Error()})
	}

}

//GetMSISDNFromCard for ussd to get pan info from sim card
func (s *Service) GetMSISDNFromCard(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerPANFromMobile // EBS simulator endpoint url goes here.
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

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.PurchaseTransaction
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(s.Redis, username+":all_transactions", &res)

		if err := s.Db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": bindingErr.Error()})
	}

}

//CreateMerchant creates new noebs merchant
func (s *Service) CreateMerchant(c *gin.Context) {

	var m merchant.Merchant // we gonna need to initialize this dude
	if err := c.ShouldBind(&m); err != nil {
		verr := ebs_fields.ValidationError{Code: "request_error", Message: err.Error()}
		c.JSON(http.StatusBadRequest, verr)
		return
	}

	m.SetDB(s.Db)

	id := GetRandomName(0)
	log.Printf("the name is: %v", id)
	m.MerchantID = id

	password, err := bcrypt.GenerateFromPassword([]byte(m.Password), 8)
	if err != nil {
		verr := ebs_fields.ValidationError{Code: "db_err", Message: err.Error()}
		c.JSON(http.StatusBadRequest, verr)
		return
	}

	// store in redis first, then commit to DB..
	p := NewPayment(s.Redis) // this introduces issues...

	if err := p.FromMobile(id, m.Merchant); err != nil {
		verr := ebs_fields.ValidationError{Code: "billers_err", Message: err.Error()}
		c.JSON(http.StatusBadRequest, verr)
		return
	}

	m.Password = string(password)
	if err := m.Write(); err != nil {
		verr := ebs_fields.ValidationError{Code: "db_err", Message: err.Error()}
		c.JSON(http.StatusBadRequest, verr)
		return
	}

	link := fmt.Sprintf("%s/%s", "https://beta.soluspay.net/api/v1/payment", id)
	c.JSON(http.StatusCreated, gin.H{"biller_name": id, "profile": m.Merchant, "url": link, "result": "ok"})
}
