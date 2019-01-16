package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
	"gopkg.in/go-playground/validator.v9"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"noebs/dashboard"
	"noebs/validations"
	"os"
	"reflect"
	"time"
)

func GetMainEngine() *gin.Engine {

	route := gin.Default()

	route.HandleMethodNotAllowed = true

	// TODO
	// Add the rest of EBS merchant services.
	route.POST("/workingKey", WorkingKey)
	route.POST("/cardTransfer", CardTransfer)
	route.POST("/purchase", Purchase)

	route.POST("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": true})
	})
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

	if env := os.Getenv("PORT"); env != "" {
		GetMainEngine().Run(env)
	} else {
		GetMainEngine().Run(":8080")
	}
}

func WorkingKey(c *gin.Context) {

	db, err := gorm.Open("sqlite3", "test1.db")

	if err != nil {
		log.Fatalf("There's an erron in DB connection, %v", err)
	}

	defer db.Close()

	db.LogMode(false)

	if err := db.AutoMigrate(&dashboard.Transaction{}); err != nil {
		log.Printf("there is an error in migration %v", err.Error)
	}

	url := EBSMerchantIP + PurchaseEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = validations.WorkingKeyFields{}

	reqBodyErr := c.ShouldBindBodyWith(&fields, binding.JSON)
	switch {
	case reqBodyErr == io.EOF:
		er := ErrorDetails{Details: nil, Code: 400, Message: "Empty request body", Status: "EMPTY_REQUEST_BODY"}
		c.JSON(http.StatusBadRequest, ErrorResponse{er})

	case reqBodyErr != nil:

		var details []ErrDetails

		for _, err := range reqBodyErr.(validator.ValidationErrors) {

			details = append(details, ErrorToString(err))
		}

		payload := ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: BadRequest}

		c.JSON(http.StatusBadRequest, ErrorResponse{payload})


		if err != nil {
			// make it onto error one
			var listDetails []ErrDetails
			details := make(ErrDetails)

			details[res.ResponseMessage] = res.ResponseCode

			listDetails = append(listDetails, details)

			payload := ErrorDetails{Code:code, Status:EBSError, Details:listDetails, Message:EBSError}
			c.JSON(code, payload)

		} else {
			c.JSON(code, successfulResponse)
		}
	}
}

func Purchase(c *gin.Context) {
	db, err := gorm.Open("sqlite3", "test1.db")

	if err != nil {
		log.Fatalf("There's an erron in DB connection, %v", err)
	}

	defer db.Close()

	db.LogMode(false)

	if err := db.AutoMigrate(&dashboard.Transaction{}); err != nil {
		log.Printf("there is an error in migration %v", err.Error)
	}

	url := EBSMerchantIP + PurchaseEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	var fields = validations.PurchaseFields{}

	reqBodyErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch {

	case reqBodyErr == io.EOF:
		er := ErrorDetails{Details: nil, Code: 400, Message: "Empty request body", Status: "EMPTY_REQUEST_BODY"}
		c.JSON(http.StatusBadRequest, ErrorResponse{er})

	case reqBodyErr != nil:

		var details []ErrDetails

		fields, _ := reflect.TypeOf(fields).FieldByName("json")
		fmt.Printf("The field name is %s", fields.Tag)

		for _, err := range reqBodyErr.(validator.ValidationErrors) {

			details = append(details, ErrorToString(err))
		}

		payload := ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: BadRequest}

		c.JSON(http.StatusBadRequest, ErrorResponse{payload})

	case reqBodyErr == nil:
		// request body was already consumed here. But the request
		// body was bounded to fields struct.
		// Now, decode the struct into a json, or bytes buffer.

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ParsingError}
			log.Fatalf("there is an error. Request is %v", string(jsonBuffer))
			c.AbortWithStatusJSON(400, ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, err := EBSHttpClient(url, jsonBuffer)

		var successfulResponse SuccessfulResponse
		successfulResponse.EBSResponse = res

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res,
		}

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

		var details []ErrDetails

		fields, _ := reflect.TypeOf(fields).FieldByName("json")
		fmt.Printf("The field name is %s", fields.Tag)

		for _, err := range reqBodyErr.(validator.ValidationErrors) {

			details = append(details, ErrorToString(err))
		}

		payload := ErrorDetails{Details: details, Code: 400, Message: "Request fields valiation error", Status: BadRequest}

		c.JSON(http.StatusBadRequest, ErrorResponse{payload})

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

		if err == ebsGatewayConnectivityErr{
			// we are unable to connect..
			er := ErrorDetails{Details:nil, Message:err.Error(), Status:ebsGatewayConnectivityErr.status, Code:code}
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


func EBSHttpClient(url string, req []byte) (int, validations.GenericEBSResponseFields, error) {

	verifyTLS := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	ebsClient := http.Client{
		Timeout:   30 * time.Second,
		Transport: verifyTLS,
	}

	reqBuffer := bytes.NewBuffer(req)
	var ebsGenericResponse validations.GenericEBSResponseFields

	reqHandler, err := http.NewRequest(http.MethodPost, url, reqBuffer)

	if err != nil {
		fmt.Println(err.Error())
		return 500, ebsGenericResponse, err
	}
	reqHandler.Header.Set("Content-Type", "application/json")
	reqHandler.Header.Set("API-Key", "removeme") // For Morsal case only.
	// EBS doesn't impose any sort of API-keys or anything. Typical EBS.

	ebsResponse, err := ebsClient.Do(reqHandler)
	if err != nil {
		return 500, ebsGenericResponse, ebsGatewayConnectivityErr
	}

	defer ebsResponse.Body.Close()



	responseBody, err := ioutil.ReadAll(ebsResponse.Body)
	if err != nil {
		return 500, ebsGenericResponse, ebsGatewayConnectivityErr
	}
	log.Println(string(responseBody))

	// check Content-type is application json, if not, panic!
	if ebsResponse.Header.Get("Content-Type") != "application/json"{
		// panic
		return 500, ebsGenericResponse, contentTypeErr
	}
	if err := json.Unmarshal(responseBody, ebsGenericResponse); err == nil {
		// there's no problem in Unmarshalling
		if ebsGenericResponse.ResponseCode == 0 {
			// the transaction is successful
			return 200, ebsGenericResponse, nil

		} else {
			// there is an error in the transaction
			err := errors.New(ebsGenericResponse.ResponseMessage)
			return 400, ebsGenericResponse, err
		}

	} else {
		// there is an error in handling the incoming EBS's ebsResponse
		return 500, ebsGenericResponse, err
	}

}
