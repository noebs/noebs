package main

import (
	"encoding/json"
	"github.com/adonese/noebs/dashboard"
	"github.com/adonese/noebs/docs"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"github.com/swaggo/gin-swagger"
	"github.com/swaggo/gin-swagger/swaggerFiles"
	"gopkg.in/go-playground/validator.v9"
	"net/http"
	"os"
	"strings"
)

var UseMockServer = false
var log = logrus.New()

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
	route.POST("/isAlive", IsAlive)

	route.POST("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": true})
	})

	dashboardGroup := route.Group("/dashboard")
	{
		dashboardGroup.GET("/get_tid", dashboard.TransactionByTid)
		dashboardGroup.GET("/get", dashboard.TransactionByTid)
		dashboardGroup.GET("/create", dashboard.MakeDummyTransaction)
		dashboardGroup.GET("/all", dashboard.GetAll)
		dashboardGroup.GET("/count", dashboard.TransactionsCount)
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

	// logging and instrumentation
	file, err := os.OpenFile("/var/log/logrus.log", os.O_CREATE|os.O_WRONLY, 0666)
	if err == nil {
		log.Out = file
	} else {
		log.Out = os.Stderr
		log.Info("Failed to log to file, using default stderr: %v", err)
	}
	log.Level = logrus.TraceLevel

	docs.SwaggerInfo.Title = "noebs Docs"

	if local := os.Getenv("EBS_LOCAL_DEV"); local != "" {
		UseMockServer = true
		log.WithFields(logrus.Fields{
			"ebs_local_flag": local,
		}).Info("User has opted to use the development server")
	} else {
		UseMockServer = false
		log.WithFields(logrus.Fields{
			"ebs_local_flag": local,
		}).Info("User has opted to not use the development server")
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

// IsAlive godoc
// @Summary Get all transactions made by a specific terminal ID
// @Description get accounts
// @Accept  json
// @Produce  json
// @Param workingKey body ebs_fields.IsAliveFields true "Working Key Request Fields"
// @Success 200 {object} main.SuccessfulResponse
// @Failure 400 {integer} 400
// @Failure 404 {integer} 404
// @Failure 500 {integer} 500
// @Router /workingKey [post]
func IsAlive(c *gin.Context) {

	url := EBSMerchantIP + IsAliveEndpoint // EBS simulator endpoint url goes here.

	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.IsAliveFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ErrDetails

		for _, err := range bindingErr {

			details = append(details, ErrorToString(err))
		}

		payload := ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: BadRequest}

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
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		var successfulResponse SuccessfulResponse
		successfulResponse.EBSResponse = res

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res,
		}

		transaction.EBSServiceName = IsAliveTransaction

		// return a masked pan
		transaction.MaskPAN()

		// God please make it works.
		if err := db.Create(&transaction).Error; err != nil {
			log.WithFields(logrus.Fields{
				"error":   err.Error(),
				"details": "Error in writing to database",
			}).Info("Problem in transaction table committing")
		}

		db.Commit()

		if ebsErr != nil {
			// convert ebs res code to int
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, successfulResponse)
		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
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

	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.WorkingKeyFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ErrDetails

		for _, err := range bindingErr {

			details = append(details, ErrorToString(err))
		}

		payload := ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: BadRequest}

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
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

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
			// convert ebs res code to int
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
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

	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.PurchaseFields{}

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
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		var successfulResponse SuccessfulResponse
		successfulResponse.EBSResponse = res

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res,
		}

		transaction.EBSServiceName = PurchaseTransaction
		// return a masked pan
		transaction.MaskPAN()
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, successfulResponse)
		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
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

	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.CardTransferFields{}

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
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		var successfulResponse SuccessfulResponse
		successfulResponse.EBSResponse = res

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res,
		}

		transaction.EBSServiceName = CardTransferTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, successfulResponse)
		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
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

	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.BillInquiryFields{}

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
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		var successfulResponse SuccessfulResponse
		successfulResponse.EBSResponse = res

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res,
		}

		transaction.EBSServiceName = BillInquiryTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, successfulResponse)
		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
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

	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.BillPaymentFields{}
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
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		var successfulResponse SuccessfulResponse
		successfulResponse.EBSResponse = res

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res,
		}

		transaction.EBSServiceName = BillPaymentTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, successfulResponse)
		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
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

	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ChangePINFields{}
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
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		var successfulResponse SuccessfulResponse
		successfulResponse.EBSResponse = res

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res,
		}

		transaction.EBSServiceName = ChangePINTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, successfulResponse)
		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
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
	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.CashOutFields{}
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
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		var successfulResponse SuccessfulResponse
		successfulResponse.EBSResponse = res

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res,
		}

		transaction.EBSServiceName = CashOutTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, successfulResponse)
		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
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

	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.CashInFields{}
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
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		var successfulResponse SuccessfulResponse
		successfulResponse.EBSResponse = res

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res,
		}

		transaction.EBSServiceName = CashInTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, successfulResponse)
		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
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

	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.MiniStatementFields{}

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
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		var successfulResponse SuccessfulResponse
		successfulResponse.EBSResponse = res

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res,
		}

		transaction.EBSServiceName = MiniStatementTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, successfulResponse)
		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

func testAPI(c *gin.Context) {

	url := EBSMerchantIP + WorkingKeyEndpoint // EBS simulator endpoint url goes here.

	// create database function
	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.WorkingKeyFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ErrDetails

		for _, err := range bindingErr {

			details = append(details, ErrorToString(err))
		}

		payload := ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: BadRequest}

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
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

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
			// convert ebs res code to int
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, successfulResponse)
		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}
