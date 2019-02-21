package main

import (
	"encoding/json"
	"fmt"
	"github.com/adonese/noebs/dashboard"
	"github.com/adonese/noebs/validations"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
	"gopkg.in/go-playground/validator.v9"
	"io"
	"log"
	"net/http"
	"os"
	"reflect"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"strings"
)

var TEST = false

var server dashboard.Server

func GetMainEngine() *gin.Engine {

	route := gin.Default()

	route.HandleMethodNotAllowed = true

	// TODO
	// Add the rest of EBS merchant services.
	route.POST("/workingKey", WorkingKey)
	route.POST("/cardTransfer", CardTransfer)
	route.POST("/purchase", Purchase)
	route.POST("/cashIn", CashIn)
	route.POST("/cashOut", CashOut)
	route.POST("/billInquiry", BillInquiry)
	route.POST("/billPayment", BillPayment)
	route.POST("/changePin", ChangePIN)
	route.POST("/miniStatement", MiniStatement)
	//add
	// -miniStatement
	// -voucherCashIn
	// -voucherCashOut

	route.POST("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": true})
	})

	route.GET("/get_tid", TransactionByTid)
	route.GET("/metrics", gin.WrapH(promhttp.Handler()))
	return route
}

func init() {
	// register the new validator
	binding.Validator = new(validations.DefaultValidator)
}

func main() {
	// Logging to a file.

	f, _ := os.Create("gin.log") // not sure whether this is the right place to do it. Maybe env vars?
	gin.DefaultWriter = io.MultiWriter(f)

	if local := os.Getenv("EBS_LOCAL_DEV"); local != ""{
		TEST = true
		log.Printf("The development flag is %s", local)
	} else{
		TEST = false
		log.Printf("The development flag is %s", local)

	}

	if env := os.Getenv("PORT"); env != "" {
		if !strings.HasPrefix(env, ":"){
			env += ":"
		}else {
			GetMainEngine().Run(env)
		}
	} else {
		GetMainEngine().Run(":8080")
	}
}

func WorkingKey(c *gin.Context) {

	url := EBSMerchantIP + WorkingKeyEndpoint // EBS simulator endpoint url goes here.

	db, err := server.GetDB()

	if err != nil {
		log.Fatalf("Unable to connect to DB: %v", err)
	}

	defer db.Close()

	db.LogMode(false)

	if err := db.AutoMigrate(&dashboard.Transaction{}).Error; err != nil {
		log.Printf("there is an error in migration %v. Msg: %s", err, err.Error)
	}

	var fields= validations.WorkingKeyFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ErrDetails

		for _, err := range bindingErr {

			details = append(details, ErrorToString(err))
		}

		payload := ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: BadRequest}

		c.JSON(http.StatusBadRequest, ErrorResponse{payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ParsingError}
			log.Fatalf("unable to parse the request %v, error: %v", string(jsonBuffer), bindingErr)
			c.AbortWithStatusJSON(400, ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := EBSHttpClient(url, jsonBuffer)

		var successfulResponse SuccessfulResponse
		successfulResponse.EBSResponse = res

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res,
		}

		transaction.EBSServiceName = PurchaseTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			var listDetails []ErrDetails
			details := make(ErrDetails)

			details[res.ResponseMessage] = res.ResponseCode

			listDetails = append(listDetails, details)

			payload := ErrorDetails{Code: code, Status: EBSError, Details: listDetails, Message: EBSError}
			c.JSON(code, payload)
		}else {
			c.JSON(code, successfulResponse)
		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}
func Purchase(c *gin.Context) {
	url := EBSMerchantIP + PurchaseEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db, err := gorm.Open("sqlite3", "test1.db")

	if err != nil {
		log.Fatalf("There's an erron in DB connection, %v", err)
	}

	defer db.Close()

	db.LogMode(false)

	if err := db.AutoMigrate(&dashboard.Transaction{}).Error; err != nil {
		log.Printf("there is an error in migration %v", err.Error)
	}

	var fields = validations.PurchaseFields{}

	reqBodyErr := c.ShouldBindBodyWith(&fields, binding.JSON)


	switch {

	case reqBodyErr == io.EOF:
		er := ErrorDetails{Details: nil, Code: 400, Message: reqBodyErr.Error(), Status: "EMPTY_REQUEST_BODY"}
		c.JSON(http.StatusBadRequest, ErrorResponse{er})

	case reqBodyErr != nil:

		_, ok := reqBodyErr.(validator.ValidationErrors)
		if !ok{
			c.AbortWithStatusJSON(400, gin.H{"test_error": reqBodyErr.Error()})
		}else{

		var details []ErrDetails

		for _, err := range reqBodyErr.(validator.ValidationErrors) {

			details = append(details, ErrorToString(err))
		}

		payload := ErrorDetails{Details: details, Code: 400, Message: "Request fields valiation error", Status: BadRequest}

		c.JSON(http.StatusBadRequest, ErrorResponse{payload})
		}

	case reqBodyErr == nil:
		// request body was already consumed here. But the request
		// body was bounded to fields struct.
		// Now, decode the struct into a json, or bytes buffer.

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ParsingError}
			log.Fatalf("unable to parse the request %v, error: %v", string(jsonBuffer), err)
			c.AbortWithStatusJSON(400, ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, err := EBSHttpClient(url, jsonBuffer)

		if err == ebsGatewayConnectivityErr {
			// we are unable to connect..
			er := ErrorDetails{Details: nil, Message: err.Error(), Status: ebsGatewayConnectivityErr.status, Code: code}
			c.AbortWithStatusJSON(code, er)

		}
		//FIXME this is not a successful response! Yes, it came off of EBS
		// But you have to check the returned error first:
		// if its ebsConnectivity error, then panic
		// if its ebsWebServiceErr (e.g., the response will have a responseCode, and responseMessage, parse it
		// onto the successfulResponse struct.

		var successfulResponse SuccessfulResponse
		successfulResponse.EBSResponse = res

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res,
		}
		// there are, indeed, different approaches to tackle this problem:
		// you could have just created a table for each service/endpoint; that would work really well (we used it in Morsal)
		// but, when you come to filtering using TerminalID, the lies in the problem! It is not easy!

		transaction.EBSServiceName = PurchaseTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if err != nil {
			// make it onto error one
			var listDetails []ErrDetails
			details := make(ErrDetails)

			details[res.ResponseMessage] = res.ResponseCode

			listDetails = append(listDetails, details)

			payload := ErrorDetails{Code: code, Status: EBSError, Details: listDetails, Message: EBSError}
			c.JSON(code, payload)

		} else {
			c.JSON(code, successfulResponse)
		}
	}
}

func CardTransfer(c *gin.Context) {

	url := EBSMerchantIP + CardTransferEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db, err := gorm.Open("sqlite3", "test1.db")

	if err != nil {
		log.Fatalf("There's an erron in DB connection, %v", err)
	}

	defer db.Close()

	db.LogMode(false)

	if err := db.AutoMigrate(&dashboard.Transaction{}); err != nil {
		log.Printf("there is an error in migration %v", err.Error)
	}

	var fields = validations.CardTransferFields{}

	reqBodyErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch {

	case reqBodyErr == io.EOF:
		er := ErrorDetails{Details: nil, Code: 400, Message: reqBodyErr.Error(), Status: "EMPTY_REQUEST_BODY"}
		c.JSON(http.StatusBadRequest, ErrorResponse{er})

	case reqBodyErr != nil:

		_, ok := reqBodyErr.(validator.ValidationErrors)
		if !ok{
			c.AbortWithStatusJSON(400, gin.H{"test_error": reqBodyErr.Error()})
		}else{

			var details []ErrDetails

			fields, _ := reflect.TypeOf(fields).FieldByName("json")
			fmt.Printf("The field name is %s", fields.Tag)

			for _, err := range reqBodyErr.(validator.ValidationErrors) {

				details = append(details, ErrorToString(err))
			}

			payload := ErrorDetails{Details: details, Code: 400, Message: "Request fields valiation error", Status: BadRequest}

			c.JSON(http.StatusBadRequest, ErrorResponse{payload})
		}

	case reqBodyErr == nil:
		// request body was already consumed here. But the request
		// body was bounded to fields struct.
		// Now, decode the struct into a json, or bytes buffer.

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ParsingError}
			log.Fatalf("unable to parse the request %v, error: %v", string(jsonBuffer), err)
			c.AbortWithStatusJSON(400, ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, err := EBSHttpClient(url, jsonBuffer)

		if err == ebsGatewayConnectivityErr {
			// we are unable to connect..
			er := ErrorDetails{Details: nil, Message: err.Error(), Status: ebsGatewayConnectivityErr.status, Code: code}
			c.AbortWithStatusJSON(code, er)

		}
		//FIXME this is not a successful response! Yes, it came off of EBS
		// But you have to check the returned error first:
		// if its ebsConnectivity error, then panic
		// if its ebsWebServiceErr (e.g., the response will have a responseCode, and responseMessage, parse it
		// onto the successfulResponse struct.

		var successfulResponse SuccessfulResponse
		successfulResponse.EBSResponse = res

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res,
		}
		// there are, indeed, different approaches to tackle this problem:
		// you could have just created a table for each service/endpoint; that would work really well (we used it in Morsal)
		// but, when you come to filtering using TerminalID, the lies in the problem! It is not easy!

		transaction.EBSServiceName = CardTransferTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if err != nil {
			// make it onto error one
			var listDetails []ErrDetails
			details := make(ErrDetails)

			details[res.ResponseMessage] = res.ResponseCode

			listDetails = append(listDetails, details)

			payload := ErrorDetails{Code: code, Status: EBSError, Details: listDetails, Message: EBSError}
			c.JSON(code, payload)

		} else {
			c.JSON(code, successfulResponse)
		}
	}

}

func BillInquiry(c *gin.Context) {

	url := EBSMerchantIP + BillInquiryEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db, err := gorm.Open("sqlite3", "test1.db")

	if err != nil {
		log.Fatalf("There's an erron in DB connection, %v", err)
	}

	defer db.Close()

	db.LogMode(false)

	if err := db.AutoMigrate(&dashboard.Transaction{}); err != nil {
		log.Printf("there is an error in migration %v", err.Error)
	}

	var fields = validations.BillInquiryFields{}

	reqBodyErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch {

	case reqBodyErr == io.EOF:
		er := ErrorDetails{Details: nil, Code: 400, Message: reqBodyErr.Error(), Status: "EMPTY_REQUEST_BODY"}
		c.JSON(http.StatusBadRequest, ErrorResponse{er})

	case reqBodyErr != nil:
		_, ok := reqBodyErr.(validator.ValidationErrors)
		if !ok{
			c.AbortWithStatusJSON(400, gin.H{"test_error": reqBodyErr.Error()})
		}else{

			var details []ErrDetails

			fields, _ := reflect.TypeOf(fields).FieldByName("json")
			fmt.Printf("The field name is %s", fields.Tag)

			for _, err := range reqBodyErr.(validator.ValidationErrors) {

				details = append(details, ErrorToString(err))
			}

			payload := ErrorDetails{Details: details, Code: 400, Message: "Request fields valiation error", Status: BadRequest}

			c.JSON(http.StatusBadRequest, ErrorResponse{payload})
		}

	case reqBodyErr == nil:
		// request body was already consumed here. But the request
		// body was bounded to fields struct.
		// Now, decode the struct into a json, or bytes buffer.

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ParsingError}
			log.Fatalf("unable to parse the request %v, error: %v", string(jsonBuffer), err)
			c.AbortWithStatusJSON(400, ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, err := EBSHttpClient(url, jsonBuffer)

		if err == ebsGatewayConnectivityErr {
			// we are unable to connect..
			er := ErrorDetails{Details: nil, Message: err.Error(), Status: ebsGatewayConnectivityErr.status, Code: code}
			c.AbortWithStatusJSON(code, er)

		}
		//FIXME this is not a successful response! Yes, it came off of EBS
		// But you have to check the returned error first:
		// if its ebsConnectivity error, then panic
		// if its ebsWebServiceErr (e.g., the response will have a responseCode, and responseMessage, parse it
		// onto the successfulResponse struct.

		var successfulResponse SuccessfulResponse
		successfulResponse.EBSResponse = res

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res,
		}
		// there are, indeed, different approaches to tackle this problem:
		// you could have just created a table for each service/endpoint; that would work really well (we used it in Morsal)
		// but, when you come to filtering using TerminalID, the lies in the problem! It is not easy!

		transaction.EBSServiceName = BillInquiryTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if err != nil {
			// make it onto error one
			var listDetails []ErrDetails
			details := make(ErrDetails)

			details[res.ResponseMessage] = res.ResponseCode

			listDetails = append(listDetails, details)

			payload := ErrorDetails{Code: code, Status: EBSError, Details: listDetails, Message: EBSError}
			c.JSON(code, payload)

		} else {
			c.JSON(code, successfulResponse)
		}
	}

}

func BillPayment(c *gin.Context) {

	url := EBSMerchantIP + BillPaymentEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db, err := gorm.Open("sqlite3", "test1.db")

	if err != nil {
		log.Fatalf("There's an erron in DB connection, %v", err)
	}

	defer db.Close()

	db.LogMode(false)

	if err := db.AutoMigrate(&dashboard.Transaction{}); err != nil {
		log.Printf("there is an error in migration %v", err.Error)
	}

	var fields = validations.BillPaymentFields{}

	reqBodyErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch {

	case reqBodyErr == io.EOF:
		er := ErrorDetails{Details: nil, Code: 400, Message: reqBodyErr.Error(), Status: "EMPTY_REQUEST_BODY"}
		c.JSON(http.StatusBadRequest, ErrorResponse{er})

	case reqBodyErr != nil:

		_, ok := reqBodyErr.(validator.ValidationErrors)
		if !ok{
			c.AbortWithStatusJSON(400, gin.H{"test_error": reqBodyErr.Error()})
		}else{

			var details []ErrDetails

			fields, _ := reflect.TypeOf(fields).FieldByName("json")
			fmt.Printf("The field name is %s", fields.Tag)

			for _, err := range reqBodyErr.(validator.ValidationErrors) {

				details = append(details, ErrorToString(err))
			}

			payload := ErrorDetails{Details: details, Code: 400, Message: "Request fields valiation error", Status: BadRequest}

			c.JSON(http.StatusBadRequest, ErrorResponse{payload})
		}

	case reqBodyErr == nil:
		// request body was already consumed here. But the request
		// body was bounded to fields struct.
		// Now, decode the struct into a json, or bytes buffer.

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ParsingError}
			log.Fatalf("unable to parse the request %v, error: %v", string(jsonBuffer), err)
			c.AbortWithStatusJSON(400, ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, err := EBSHttpClient(url, jsonBuffer)

		if err == ebsGatewayConnectivityErr {
			// we are unable to connect..
			er := ErrorDetails{Details: nil, Message: err.Error(), Status: ebsGatewayConnectivityErr.status, Code: code}
			c.AbortWithStatusJSON(code, er)

		}
		//FIXME this is not a successful response! Yes, it came off of EBS
		// But you have to check the returned error first:
		// if its ebsConnectivity error, then panic
		// if its ebsWebServiceErr (e.g., the response will have a responseCode, and responseMessage, parse it
		// onto the successfulResponse struct.

		var successfulResponse SuccessfulResponse
		successfulResponse.EBSResponse = res

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res,
		}
		// there are, indeed, different approaches to tackle this problem:
		// you could have just created a table for each service/endpoint; that would work really well (we used it in Morsal)
		// but, when you come to filtering using TerminalID, the lies in the problem! It is not easy!

		transaction.EBSServiceName = BillPaymentTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if err != nil {
			// make it onto error one
			var listDetails []ErrDetails
			details := make(ErrDetails)

			details[res.ResponseMessage] = res.ResponseCode

			listDetails = append(listDetails, details)

			payload := ErrorDetails{Code: code, Status: EBSError, Details: listDetails, Message: EBSError}
			c.JSON(code, payload)

		} else {
			c.JSON(code, successfulResponse)
		}
	}

}

func ChangePIN(c *gin.Context) {

	url := EBSMerchantIP + ChangePINEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db, err := gorm.Open("sqlite3", "test1.db")

	if err != nil {
		log.Fatalf("There's an erron in DB connection, %v", err)
	}

	defer db.Close()

	db.LogMode(false)

	if err := db.AutoMigrate(&dashboard.Transaction{}); err != nil {
		log.Printf("there is an error in migration %v", err.Error)
	}

	var fields = validations.ChangePINFields{}

	reqBodyErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch {

	case reqBodyErr == io.EOF:
		er := ErrorDetails{Details: nil, Code: 400, Message: reqBodyErr.Error(), Status: "EMPTY_REQUEST_BODY"}
		c.JSON(http.StatusBadRequest, ErrorResponse{er})

	case reqBodyErr != nil:

		_, ok := reqBodyErr.(validator.ValidationErrors)
		if !ok{
			c.AbortWithStatusJSON(400, gin.H{"test_error": reqBodyErr.Error()})
		}else{

			var details []ErrDetails

			fields, _ := reflect.TypeOf(fields).FieldByName("json")
			fmt.Printf("The field name is %s", fields.Tag)

			for _, err := range reqBodyErr.(validator.ValidationErrors) {

				details = append(details, ErrorToString(err))
			}

			payload := ErrorDetails{Details: details, Code: 400, Message: "Request fields valiation error", Status: BadRequest}

			c.JSON(http.StatusBadRequest, ErrorResponse{payload})
		}

	case reqBodyErr == nil:
		// request body was already consumed here. But the request
		// body was bounded to fields struct.
		// Now, decode the struct into a json, or bytes buffer.

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ParsingError}
			log.Fatalf("unable to parse the request %v, error: %v", string(jsonBuffer), err)
			c.AbortWithStatusJSON(400, ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, err := EBSHttpClient(url, jsonBuffer)

		if err == ebsGatewayConnectivityErr {
			// we are unable to connect..
			er := ErrorDetails{Details: nil, Message: err.Error(), Status: ebsGatewayConnectivityErr.status, Code: code}
			c.AbortWithStatusJSON(code, er)

		}

		var successfulResponse SuccessfulResponse
		successfulResponse.EBSResponse = res

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res,
		}
		// there are, indeed, different approaches to tackle this problem:
		// you could have just created a table for each service/endpoint; that would work really well (we used it in Morsal)
		// but, when you come to filtering using TerminalID, the lies in the problem! It is not easy!

		transaction.EBSServiceName = ChangePINTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if err != nil {
			// make it onto error one
			var listDetails []ErrDetails
			details := make(ErrDetails)

			details[res.ResponseMessage] = res.ResponseCode

			listDetails = append(listDetails, details)

			payload := ErrorDetails{Code: code, Status: EBSError, Details: listDetails, Message: EBSError}
			c.JSON(code, payload)

		} else {
			c.JSON(code, successfulResponse)
		}
	}

}

func CashOut(c *gin.Context) {

	url := EBSMerchantIP + CashOutEndpoint // EBS simulator endpoint url goes here.
	// This function flow:
	// - open a DB connection (getDB)
	// - check for the binding errors
	//
	db, err := gorm.Open("sqlite3", "test1.db")

	if err != nil {
		log.Fatalf("There's an erron in DB connection, %v", err)
	}

	defer db.Close()

	db.LogMode(false)

	if err := db.AutoMigrate(&dashboard.Transaction{}); err != nil {
		log.Printf("there is an error in migration %v", err.Error)
	}

	var fields = validations.CashOutFields{}

	reqBodyErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch {

	case reqBodyErr == io.EOF:
		er := ErrorDetails{Details: nil, Code: 400, Message: reqBodyErr.Error(), Status: "EMPTY_REQUEST_BODY"}
		c.JSON(http.StatusBadRequest, ErrorResponse{er})

	case reqBodyErr != nil:

		_, ok := reqBodyErr.(validator.ValidationErrors)
		if !ok{
			c.AbortWithStatusJSON(400, gin.H{"test_error": reqBodyErr.Error()})
		}else{

			var details []ErrDetails

			fields, _ := reflect.TypeOf(fields).FieldByName("json")
			fmt.Printf("The field name is %s", fields.Tag)

			for _, err := range reqBodyErr.(validator.ValidationErrors) {

				details = append(details, ErrorToString(err))
			}

			payload := ErrorDetails{Details: details, Code: 400, Message: "Request fields valiation error", Status: BadRequest}

			c.JSON(http.StatusBadRequest, ErrorResponse{payload})
		}

	case reqBodyErr == nil:
		// request body was already consumed here. But the request
		// body was bounded to fields struct.
		// Now, decode the struct into a json, or bytes buffer.

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ParsingError}
			log.Fatalf("unable to parse the request %v, error: %v", string(jsonBuffer), err)
			c.AbortWithStatusJSON(400, ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, err := EBSHttpClient(url, jsonBuffer)

		if err == ebsGatewayConnectivityErr {
			// we are unable to connect..
			er := ErrorDetails{Details: nil, Message: err.Error(), Status: ebsGatewayConnectivityErr.status, Code: code}
			c.AbortWithStatusJSON(code, er)

		}
		//FIXME this is not a successful response! Yes, it came off of EBS
		// But you have to check the returned error first:
		// if its ebsConnectivity error, then panic
		// if its ebsWebServiceErr (e.g., the response will have a responseCode, and responseMessage, parse it
		// onto the successfulResponse struct.

		var successfulResponse SuccessfulResponse
		successfulResponse.EBSResponse = res

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res,
		}
		// there are, indeed, different approaches to tackle this problem:
		// you could have just created a table for each service/endpoint; that would work really well (we used it in Morsal)
		// but, when you come to filtering using TerminalID, the lies in the problem! It is not easy!

		transaction.EBSServiceName = CashOutTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if err != nil {
			// make it onto error one
			var listDetails []ErrDetails
			details := make(ErrDetails)

			details[res.ResponseMessage] = res.ResponseCode

			listDetails = append(listDetails, details)

			payload := ErrorDetails{Code: code, Status: EBSError, Details: listDetails, Message: EBSError}
			c.JSON(code, payload)

		} else {
			c.JSON(code, successfulResponse)
		}
	}

}

func CashIn(c *gin.Context) {

	url := EBSMerchantIP + CashInEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db, err := gorm.Open("sqlite3", "test1.db")

	if err != nil {
		log.Fatalf("There's an erron in DB connection, %v", err)
	}

	defer db.Close()

	db.LogMode(false)

	if err := db.AutoMigrate(&dashboard.Transaction{}); err != nil {
		log.Printf("there is an error in migration %v", err.Error)
	}

	var fields = validations.CashInFields{}

	reqBodyErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch {

	case reqBodyErr == io.EOF:
		er := ErrorDetails{Details: nil, Code: 400, Message: reqBodyErr.Error(), Status: "EMPTY_REQUEST_BODY"}
		c.JSON(http.StatusBadRequest, ErrorResponse{er})

	case reqBodyErr != nil:

		_, ok := reqBodyErr.(validator.ValidationErrors)
		if !ok{
			c.AbortWithStatusJSON(400, gin.H{"test_error": reqBodyErr.Error()})
		}else{

			var details []ErrDetails

			fields, _ := reflect.TypeOf(fields).FieldByName("json")
			fmt.Printf("The field name is %s", fields.Tag)

			for _, err := range reqBodyErr.(validator.ValidationErrors) {

				details = append(details, ErrorToString(err))
			}

			payload := ErrorDetails{Details: details, Code: 400, Message: "Request fields valiation error", Status: BadRequest}

			c.JSON(http.StatusBadRequest, ErrorResponse{payload})
		}

	case reqBodyErr == nil:
		// request body was already consumed here. But the request
		// body was bounded to fields struct.
		// Now, decode the struct into a json, or bytes buffer.

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ParsingError}
			log.Fatalf("unable to parse the request %v, error: %v", string(jsonBuffer), err)
			c.AbortWithStatusJSON(400, ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, err := EBSHttpClient(url, jsonBuffer)

		if err == ebsGatewayConnectivityErr {
			// we are unable to connect..
			er := ErrorDetails{Details: nil, Message: err.Error(), Status: ebsGatewayConnectivityErr.status, Code: code}
			c.AbortWithStatusJSON(code, er)

		}
		//FIXME this is not a successful response! Yes, it came off of EBS
		// But you have to check the returned error first:
		// if its ebsConnectivity error, then panic
		// if its ebsWebServiceErr (e.g., the response will have a responseCode, and responseMessage, parse it
		// onto the successfulResponse struct.

		var successfulResponse SuccessfulResponse
		successfulResponse.EBSResponse = res

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res,
		}
		// there are, indeed, different approaches to tackle this problem:
		// you could have just created a table for each service/endpoint; that would work really well (we used it in Morsal)
		// but, when you come to filtering using TerminalID, the lies in the problem! It is not easy!

		transaction.EBSServiceName = CardTransferTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if err != nil {
			// make it onto error one
			var listDetails []ErrDetails
			details := make(ErrDetails)

			details[res.ResponseMessage] = res.ResponseCode

			listDetails = append(listDetails, details)

			payload := ErrorDetails{Code: code, Status: EBSError, Details: listDetails, Message: EBSError}
			c.JSON(code, payload)

		} else {
			c.JSON(code, successfulResponse)
		}
	}

}

func MiniStatement(c *gin.Context) {

	url := EBSMerchantIP + MiniStatementEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db, err := gorm.Open("sqlite3", "test1.db")

	if err != nil {
		log.Fatalf("There's an erron in DB connection, %v", err)
	}

	defer db.Close()

	db.LogMode(false)

	if err := db.AutoMigrate(&dashboard.Transaction{}); err != nil {
		log.Printf("there is an error in migration %v", err.Error)
	}

	var fields = validations.MiniStatementFields{}

	reqBodyErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch {

	case reqBodyErr == io.EOF:
		er := ErrorDetails{Details: nil, Code: 400, Message: reqBodyErr.Error(), Status: "EMPTY_REQUEST_BODY"}
		c.JSON(http.StatusBadRequest, ErrorResponse{er})

	case reqBodyErr != nil:

		_, ok := reqBodyErr.(validator.ValidationErrors)
		if !ok{
			c.AbortWithStatusJSON(400, gin.H{"test_error": reqBodyErr.Error()})
		}else{

			var details []ErrDetails

			fields, _ := reflect.TypeOf(fields).FieldByName("json")
			fmt.Printf("The field name is %s", fields.Tag)

			for _, err := range reqBodyErr.(validator.ValidationErrors) {

				details = append(details, ErrorToString(err))
			}

			payload := ErrorDetails{Details: details, Code: 400, Message: "Request fields valiation error", Status: BadRequest}

			c.JSON(http.StatusBadRequest, ErrorResponse{payload})
		}

	case reqBodyErr == nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ParsingError}
			log.Fatalf("unable to parse the request %v, error: %v", string(jsonBuffer), err)
			c.AbortWithStatusJSON(400, ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, err := EBSHttpClient(url, jsonBuffer)

		if err == ebsGatewayConnectivityErr {
			// we are unable to connect..
			er := ErrorDetails{Details: nil, Message: err.Error(), Status: ebsGatewayConnectivityErr.status, Code: code}
			c.AbortWithStatusJSON(code, er)

		}
		//FIXME this is not a successful response! Yes, it came off of EBS
		// But you have to check the returned error first:
		// if its ebsConnectivity error, then panic
		// if its ebsWebServiceErr (e.g., the response will have a responseCode, and responseMessage, parse it
		// onto the successfulResponse struct.

		var successfulResponse SuccessfulResponse
		successfulResponse.EBSResponse = res

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res,
		}

		transaction.EBSServiceName = MiniStatementTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if err != nil {
			// make it onto error one
			var listDetails []ErrDetails
			details := make(ErrDetails)

			details[res.ResponseMessage] = res.ResponseCode

			listDetails = append(listDetails, details)

			payload := ErrorDetails{Code: code, Status: EBSError, Details: listDetails, Message: EBSError}
			c.JSON(code, payload)

		} else {
			c.JSON(code, successfulResponse)
		}
	}

}


func TransactionByTid(c *gin.Context){

	db, err := gorm.Open("sqlite3", "test.db")
	if err != nil{
		log.Fatalf("there is an error: %v", err)
	}
	defer db.Close()

	db.AutoMigrate(&dashboard.Transaction{})
	var res dashboard.Transaction

	query := c.Request.URL.Query()

	if id, ok := query["tid"]; ok {
		// he has sent something

		if err := db.Where(&dashboard.Transaction{
			GenericEBSResponseFields: validations.GenericEBSResponseFields{TerminalID:id[0]},
		}).Find(&res).Error; err != nil{
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"object_not_found": id, "error": err.Error()})
		}else{
			c.JSON(200, gin.H{"result": res.TerminalID})
		}

	}else{
		// or, maybe change to into something like value not provided.
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not_found"})
	}
}
