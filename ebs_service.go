/*
The main entry point for noebs services.
 */
package main

import (
	"encoding/json"
	"fmt"
	"github.com/adonese/noebs/dashboard"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/go-playground/validator.v9"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"
)

var UseMockServer = false


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

	route.POST("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": true})
	})

	route.GET("/get_tid", TransactionByTid)
	route.GET("/get", TransactionByTid)
	route.GET("/create", MakeDummyTransaction)
	route.GET("/metrics", gin.WrapH(promhttp.Handler()))

	return route
}

func init() {
	// register the new validator
	binding.Validator = new(ebs_fields.DefaultValidator)
}

func main() {
	// Logging to a file.

	f, _ := os.Create("gin.log") // not sure whether this is the right place to do it. Maybe env vars?
	gin.DefaultWriter = io.MultiWriter(f)

	if local := os.Getenv("EBS_LOCAL_DEV"); local != ""{
		UseMockServer = true
		log.Printf("The development flag is %s", local)
	} else{
		UseMockServer = false
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

	db, _ := gorm.Open("sqlite3", "test.db")

	env := &dashboard.Env{Db: db}

	defer env.Db.Close()

	db.AutoMigrate(&dashboard.Transaction{})


	db.LogMode(false)

	if err := db.AutoMigrate(&dashboard.Transaction{}).Error; err != nil {
		log.Printf("there is an error in migration %v. Msg: %s", err, err.Error)
	}

	var fields= ebs_fields.WorkingKeyFields{}

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

	var fields = ebs_fields.PurchaseFields{}

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

	var fields = ebs_fields.CardTransferFields{}

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

	var fields = ebs_fields.BillInquiryFields{}

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

	var fields = ebs_fields.BillPaymentFields{}

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

	var fields = ebs_fields.ChangePINFields{}

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

	var fields = ebs_fields.CashOutFields{}

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

	var fields = ebs_fields.CashInFields{}

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

	var fields = ebs_fields.MiniStatementFields{}

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

	db, _ := gorm.Open("sqlite3", "test.db")

	env := &dashboard.Env{Db: db}

	defer env.Db.Close()

	db.AutoMigrate(&dashboard.Transaction{})

	var tran dashboard.Transaction
	var count interface{}
	//id := c.Params.ByName("id")
	err := env.Db.Model(&tran).Count(&count).Error; if err != nil{
		c.AbortWithStatus(404)
	}
	c.JSON(200, gin.H{"result": count})
}


func MakeDummyTransaction(c *gin.Context){

	db, _ := gorm.Open("sqlite3", "test.db")

	env := &dashboard.Env{Db: db}

	if err := env.Db.AutoMigrate(&dashboard.Transaction{}).Error; err != nil{
		log.Fatalf("unable to automigrate: %s", err.Error())
	}

	tran := dashboard.Transaction{
		GenericEBSResponseFields: ebs_fields.GenericEBSResponseFields{
			ImportantEBSFields:     ebs_fields.ImportantEBSFields{},
			TerminalID:             "08000002",
			TranDateTime:           time.Now().UTC().String(),
			SystemTraceAuditNumber: rand.Intn(9999),
			ClientID:               "Morsa",
			PAN:                    "123457894647372",
			AdditionalData:         "",
			ServiceID:              "",
			TranFee:                0,
			AdditionalAmount:       0,
			TranAmount:             0,
			PhoneNumber:            "",
			FromAccount:            "",
			ToAccount:              "",
			FromCard:               "",
			ToCard:                 "",
			OTP:                    "",
			OTPID:                  "",
			TranCurrencyCode:       "",
			EBSServiceName:         "",
			WorkingKey:             "",
		},
	}

	if err := env.Db.Create(&tran).Error; err != nil{
		c.AbortWithStatusJSON(500, gin.H{"error": err.Error()})
	}else {
		c.JSON(200, gin.H{"message": "object create successfully."})
	}
}