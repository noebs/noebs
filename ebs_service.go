package main

import (
	"encoding/json"
	"fmt"
	"github.com/adonese/noebs/dashboard"
	"github.com/adonese/noebs/docs"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/swaggo/gin-swagger"
	"github.com/swaggo/gin-swagger/swaggerFiles"
	"gopkg.in/go-playground/validator.v9"
	"io"
	"log"
	"net/http"
	"os"
	"reflect"
	"strings"
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

	dashboardGroup := route.Group("/dashboard")
	{
		dashboardGroup.GET("/get_tid", dashboard.TransactionByTid)
		dashboardGroup.GET("/get", dashboard.TransactionByTid)
		dashboardGroup.GET("/create", dashboard.MakeDummyTransaction)
		dashboardGroup.GET("/all", dashboard.GetAll)
		dashboardGroup.GET("/metrics", gin.WrapH(promhttp.Handler()))
	}
	route.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	return route
}

func init() {
	// register the new validator
	binding.Validator = new(ebs_fields.DefaultValidator)
}

// @title noebs Example API
// @version 1.0
// @description This is a sample server celler server.
// @termsOfService http://swagger.io/terms/

// @contact.name API Support
// @contact.url http://www.swagger.io/support
// @contact.email support@swagger.io

// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html

// @host localhost:8080
// @BasePath /api/v1

// @securityDefinitions.basic BasicAuth

// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name Authorization

// @securitydefinitions.oauth2.application OAuth2Application
// @tokenUrl https://example.com/oauth/token
// @scope.write Grants write access
// @scope.admin Grants read and write access to administrative information

// @securitydefinitions.oauth2.implicit OAuth2Implicit
// @authorizationurl https://example.com/oauth/authorize
// @scope.write Grants write access
// @scope.admin Grants read and write access to administrative information

// @securitydefinitions.oauth2.password OAuth2Password
// @tokenUrl https://example.com/oauth/token
// @scope.read Grants read access
// @scope.write Grants write access
// @scope.admin Grants read and write access to administrative information

// @securitydefinitions.oauth2.accessCode OAuth2AccessCode
// @tokenUrl https://example.com/oauth/token
// @authorizationurl https://example.com/oauth/authorize
// @scope.admin Grants read and write access to administrative information

func main() {

	docs.SwaggerInfo.Title = "noebs Docs"
	// Logging to a file.
	f, _ := os.Create("gin.log") // not sure whether this is the right place to do it. Maybe env vars?
	gin.DefaultWriter = io.MultiWriter(f)

	if local := os.Getenv("EBS_LOCAL_DEV"); local != "" {
		UseMockServer = true
		log.Printf("The development flag is %s", local)
	} else {
		UseMockServer = false
		log.Printf("The development flag is %s", local)

	}

	if env := os.Getenv("PORT"); env != "" {
		if !strings.HasPrefix(env, ":") {
			env += ":"
		} else {
			GetMainEngine().Run(env)
		}
	} else {
		GetMainEngine().Run(":8080")
	}
}

// WorkingKey godoc
// @Summary Get all transactions made by a specific terminal ID
// @Description get accounts
// @Accept  json
// @Produce  json
// @Param workingKey body ebs_fields.WorkingKeyFields true "Working Key Request Fields"
// @Success 200 {object} main.SuccessfulResponse
// @Failure 400 {integer} 400
// @Failure 404 {integer} 404
// @Failure 500 {integer} 500
// @Router /workingKey [post]
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

	var fields = ebs_fields.WorkingKeyFields{}

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

		transaction.EBSServiceName = WorkingKeyTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			// log the transaction
			log.Printf("a transaction was made: %v\tEBS Response:%v, \tResponse code:%v", jsonBuffer, res, code)
			var listDetails []ErrDetails
			details := make(ErrDetails)

			details[res.ResponseMessage] = res.ResponseMessage

			listDetails = append(listDetails, details)

			payload := ErrorDetails{Code: code, Status: EBSError, Details: listDetails, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, successfulResponse)
		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

// Purchase godoc
// @Summary Get all transactions made by a specific terminal ID
// @Description get accounts
// @Accept  json
// @Produce  json
// @Param purchase body ebs_fields.PurchaseFields true "Purchase Request Fields"
// @Success 200 {object} main.SuccessfulResponse
// @Failure 400 {integer} 400
// @Failure 404 {integer} 404
// @Failure 500 {integer} 500
// @Router /purchase [post]
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
		if !ok {
			c.AbortWithStatusJSON(400, gin.H{"test_error": reqBodyErr.Error()})
		} else {

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

// CardTransfer godoc
// @Summary Get all transactions made by a specific terminal ID
// @Description get accounts
// @Accept  json
// @Produce  json
// @Param cardTransfer body ebs_fields.CardTransferFields true "Card Transfer Request Fields"
// @Success 200 {object} main.SuccessfulResponse
// @Failure 400 {integer} 400
// @Failure 404 {integer} 404
// @Failure 500 {integer} 500
// @Router /cardTransfer [post]
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
		if !ok {
			c.AbortWithStatusJSON(400, gin.H{"test_error": reqBodyErr.Error()})
		} else {

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

// BillInquiry godoc
// @Summary Get all transactions made by a specific terminal ID
// @Description get accounts
// @Accept  json
// @Produce  json
// @Param billInquiry body ebs_fields.BillInquiryFields true "Bill Inquiry Request Fields"
// @Success 200 {object} main.SuccessfulResponse
// @Failure 400 {integer} 400
// @Failure 404 {integer} 404
// @Failure 500 {integer} 500
// @Router /billInquiry [post]
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
		if !ok {
			c.AbortWithStatusJSON(400, gin.H{"test_error": reqBodyErr.Error()})
		} else {

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

// BillPayment godoc
// @Summary Get all transactions made by a specific terminal ID
// @Description get accounts
// @Accept  json
// @Produce  json
// @Param billPayment body ebs_fields.BillPaymentFields true "Bill Payment Request Fields"
// @Success 200 {object} main.SuccessfulResponse
// @Failure 400 {integer} 400
// @Failure 404 {integer} 404
// @Failure 500 {integer} 500
// @Router /billPayment [post]
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
		if !ok {
			c.AbortWithStatusJSON(400, gin.H{"test_error": reqBodyErr.Error()})
		} else {

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

// ChangePIN godoc
// @Summary Get all transactions made by a specific terminal ID
// @Description get accounts
// @Accept  json
// @Produce  json
// @Param changePIN body ebs_fields.ChangePINFields true "Change PIN Request Fields"
// @Success 200 {object} main.SuccessfulResponse
// @Failure 400 {integer} 400
// @Failure 404 {integer} 404
// @Failure 500 {integer} 500
// @Router /changePin [post]
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
		if !ok {
			c.AbortWithStatusJSON(400, gin.H{"test_error": reqBodyErr.Error()})
		} else {

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

// CashOut godoc
// @Summary Get all transactions made by a specific terminal ID
// @Description get accounts
// @Accept  json
// @Produce  json
// @Param cashOut body ebs_fields.CashOutFields true "Cash Out Request Fields"
// @Success 200 {object} main.SuccessfulResponse
// @Failure 400 {integer} 400
// @Failure 404 {integer} 404
// @Failure 500 {integer} 500
// @Router /cashOut [post]
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
		if !ok {
			c.AbortWithStatusJSON(400, gin.H{"test_error": reqBodyErr.Error()})
		} else {

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

// CashIn godoc
// @Summary Get all transactions made by a specific terminal ID
// @Description get accounts
// @Accept  json
// @Produce  json
// @Param cashOut body ebs_fields.CashInFields true "Cash In Request Fields"
// @Success 200 {object} main.SuccessfulResponse
// @Failure 400 {integer} 400
// @Failure 404 {integer} 404
// @Failure 500 {integer} 500
// @Router /cashIn [post]
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
		if !ok {
			c.AbortWithStatusJSON(400, gin.H{"test_error": reqBodyErr.Error()})
		} else {

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

// MiniStatement godoc
// @Summary Get all transactions made by a specific terminal ID
// @Description get accounts
// @Accept  json
// @Produce  json
// @Param miniStatement body ebs_fields.MiniStatementFields true "Mini Statement Request Fields"
// @Success 200 {object} main.SuccessfulResponse
// @Failure 400 {integer} 400
// @Failure 404 {integer} 404
// @Failure 500 {integer} 500
// @Router /miniStatement [post]
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
		if !ok {
			c.AbortWithStatusJSON(400, gin.H{"test_error": reqBodyErr.Error()})
		} else {

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
