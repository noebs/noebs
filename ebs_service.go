package main

import (
	"encoding/json"
	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/dashboard"
	"github.com/adonese/noebs/docs"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/utils"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-redis/redis"
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

var log = logrus.New()

func GetMainEngine() *gin.Engine {

	route := gin.Default()

	route.HandleMethodNotAllowed = true

	route.Use(gateway.OptionsMiddleware)

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
	route.POST("/balance", Balance)

	route.POST("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": true})
	})

	dashboardGroup := route.Group("/dashboard")
	//dashboardGroup.Use(gateway.CORSMiddleware())
	{
		dashboardGroup.GET("/get_tid", dashboard.TransactionByTid)
		dashboardGroup.GET("/get", dashboard.TransactionByTid)
		dashboardGroup.GET("/create", dashboard.MakeDummyTransaction)
		dashboardGroup.GET("/all", dashboard.GetAll)
		dashboardGroup.GET("/count", dashboard.TransactionsCount)
		dashboardGroup.GET("/settlement", dashboard.DailySettlement)
		dashboardGroup.GET("/metrics", gin.WrapH(promhttp.Handler()))
		dashboardGroup.GET("/merchant", dashboard.MerchantTransactionsEndpoint)
	}

	route.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	consumer := route.Group("/consumer")

	//consumer.Use(gateway.OptionsMiddleware)

	consumer.POST("/login", gateway.LoginHandler)
	consumer.POST("/register", gateway.CreateUser)
	consumer.POST("/refresh", gateway.RefreshHandler)
	consumer.POST("/logout", gateway.LogOut)

	consumer.POST("/balance", ConsumerBalance)
	consumer.POST("/is_alive", ConsumerIsAlive)
	consumer.POST("/bill_payment", ConsumerBillPayment)
	consumer.POST("/bill_inquiry", ConsumerBillInquiry)
	consumer.POST("/p2p", ConsumerCardTransfer)
	consumer.POST("/purchase", ConsumerPurchase)
	consumer.POST("/status", ConsumerStatus)
	consumer.POST("/key", ConsumerWorkingKey)
	consumer.POST("/ipin", ConsumerIPinChange)

	consumer.Use(gateway.AuthMiddleware())
	{
		consumer.GET("/get_cards", GetCards)
		consumer.POST("/add_card", AddCards)

		consumer.PUT("/edit_card", EditCard)
		consumer.DELETE("/delete_card", RemoveCard)

		consumer.GET("/get_mobile", GetMobile)
		consumer.POST("/add_mobile", AddMobile)

		consumer.POST("/test", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"message": true})
		})
	}

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
// @contact.email adonese@soluspay/net
// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html
// @host beta.soluspay.net
// @BasePath /api/
// @securityDefinitions.basic BasicAuth
// @in header
func main() {

	// logging and instrumentation
	file, err := os.OpenFile("logrus.log", os.O_CREATE|os.O_WRONLY, 0666)
	if err == nil {
		log.Out = file
	} else {
		log.Out = os.Stderr
		log.Info("Failed to log to file, using default stderr: %v", err)
	}
	log.Level = logrus.DebugLevel
	log.SetReportCaller(true) // get the method/function where the logging occured

	docs.SwaggerInfo.Title = "noebs Docs"
	gin.SetMode(gin.ReleaseMode)

	if env := os.Getenv("PORT"); env != "" {
		if !strings.HasPrefix(env, ":") {
			env += ":"
		} else {
			GetMainEngine().RunTLS(env, ".certs/server.pem", ".certs/server.key")
		}
	} else {
		err := GetMainEngine().Run(":8080")
		if err != nil {
			panic(err)
		}
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

	// use bind to get free Form support rendering!
	// there is no practical need of using c.ShouldBindBodyWith;
	// Bind is more perfomant than ShouldBindBodyWith; the later copies the request body and reuse it
	// while Bind works directly on the responseBody stream.
	// More importantly, Bind smartly handles Forms rendering and validations; ShouldBindBodyWith forces you
	// into using only a *pre-specified* binding schema
	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ErrDetails

		for _, err := range bindingErr {

			details = append(details, ErrorToString(err))
		}

		payload := ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: BadRequest}

		c.JSON(http.StatusBadRequest, payload)

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ParsingError}
			c.AbortWithStatusJSON(400, er)
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = IsAliveTransaction

		// return a masked pan
		transaction.MaskPAN()

		// God please make it works.
		if err := db.Table("transactions").Create(&transaction).Error; err != nil {
			log.WithFields(logrus.Fields{
				"error":   err.Error(),
				"details": "Error in writing to database",
			}).Info("Problem in transaction table committing")
		}

		if ebsErr != nil {
			// convert ebs res code to int
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res.GenericEBSResponseFields, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
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

	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

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

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = WorkingKeyTransaction
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
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res.GenericEBSResponseFields, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
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

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = PurchaseTransaction

		if err := db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		redisClient := utils.GetRedis()
		pTran := dashboard.ToPurchase(fields)
		redisClient.LPush(fields.TerminalID+":purchase", &pTran)

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res.GenericEBSResponseFields, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

// Balance godoc
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
func Balance(c *gin.Context) {
	url := EBSMerchantIP + BalanceEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.BalanceFields{}

	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

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

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = BalanceTransaction
		// return a masked pan

		// God please make it works.
		db.Table("transactions").Create(&transaction)

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res.GenericEBSResponseFields, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
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

	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

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

		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = CardTransferTransaction
		// God please make it works.
		db.Table("transactions").Create(&transaction)

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res.GenericEBSResponseFields, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
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

	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

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

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = BillInquiryTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
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
	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

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

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = BillPaymentTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res.GenericEBSResponseFields, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
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
	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

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

		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ChangePINTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res.GenericEBSResponseFields, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
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
	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

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

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = CashOutTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res.GenericEBSResponseFields, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
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
	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

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

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = CashInTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res.GenericEBSResponseFields, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

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

	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

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

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = MiniStatementTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
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

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
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
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

// Consumer Services

func ConsumerPurchase(c *gin.Context) {
	url := EBSIp + ConsumerPurchaseEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ConsumerPurchaseFields{}

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

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = PurchaseTransaction

		redisClient := utils.GetRedis()
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(redisClient, username+":all_transactions", &res)

		if err := db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res.GenericEBSResponseFields, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

func ConsumerIsAlive(c *gin.Context) {
	url := EBSIp + ConsumerIsAliveEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ConsumerIsAliveFields{}

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

		//// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		redisClient := utils.GetRedis()
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(redisClient, username+":all_transactions", &res)

		transaction.EBSServiceName = PurchaseTransaction

		if err := db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

func ConsumerBillPayment(c *gin.Context) {
	url := EBSIp + ConsumerBillPaymentEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ConsumerBillPaymentFields{}

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

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = PurchaseTransaction

		redisClient := utils.GetRedis()
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(redisClient, username+":all_transactions", &res)

		if err := db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

func ConsumerBillInquiry(c *gin.Context) {
	url := EBSIp + ConsumerBillInquiryEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ConsumerBillInquiryFields{}

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

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = PurchaseTransaction

		if err := db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		// Save to Redis list
		redisClient := utils.GetRedis()
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(redisClient, username+":all_transactions", &res)

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

func ConsumerBalance(c *gin.Context) {
	url := EBSIp + ConsumerBalanceEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ConsumerBalanceFields{}

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

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = PurchaseTransaction

		redisClient := utils.GetRedis()
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(redisClient, username+":all_transactions", &res)

		if err := db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

func ConsumerWorkingKey(c *gin.Context) {
	url := EBSIp + ConsumerWorkingKeyEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ConsumerWorkingKeyFields{}

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

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = PurchaseTransaction
		//
		//// store the transaction into redis (for more performance gain)
		//redisClient := utils.GetRedis()
		//var d ebs_fields.DisputeFields
		//d.New(res)
		//// later, the username should be added username + ":all_transactions"
		//// this will never work for now
		//// this might work, as we have added a new wg stuff
		//// it will work
		//key := "all_transactions"
		//var wg sync.WaitGroup
		//wg.Add(1)
		//go utils.MarshalIntoRedis(d, redisClient, key)
		//wg.Done()

		redisClient := utils.GetRedis()
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(redisClient, username+":all_transactions", &res)

		if err := db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

func ConsumerCardTransfer(c *gin.Context) {
	url := EBSIp + ConsumerCardTransferEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ConsumerCardTransferFields{}

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

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = PurchaseTransaction

		redisClient := utils.GetRedis()
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(redisClient, username+":all_transactions", &res)

		if err := db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

func ConsumerIPinChange(c *gin.Context) {
	url := EBSIp + ConsumerCardTransferEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ConsumerIPinFields{}

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

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = PurchaseTransaction
		redisClient := utils.GetRedis()
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(redisClient, username+":all_transactions", &res)

		if err := db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

func ConsumerStatus(c *gin.Context) {
	url := EBSIp + ConsumerStatusEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ConsumerStatusFields{}

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

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = PurchaseTransaction
		redisClient := utils.GetRedis()
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(redisClient, username+":all_transactions", &res)

		if err := db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}

		if ebsErr != nil {
			payload := ErrorDetails{Code: res.ResponseCode, Status: EBSError, Details: res, Message: EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

func ConsumerTransactions(c *gin.Context) {
	//TODO get the transaction from Redis instance!
	redisClient := utils.GetRedis()

	username := c.GetString("username")
	if username == "" {
		username = "invalid_key"
	}
	redisClient.Get(username)

	// you should probably marshal these data
	c.JSON(http.StatusOK, gin.H{"transactions": username})
}

func GetCards(c *gin.Context) {
	redisClient := utils.GetRedis()

	username := c.GetString("username")
	if username == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
	} else {
		cards, err := redisClient.ZRange(username+":cards", 0, -1).Result()
		if err != nil {
			// handle the error somehow
			logrus.WithFields(logrus.Fields{
				"error":   "unable to get results from redis",
				"message": err.Error(),
			}).Info("unable to get results from redis")
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "message": "error in redis"})
		}

		// unmrshall cards and send them back to the user
		// they should be a []Cards

		var cb ebs_fields.CardsRedis
		var cardBytes []ebs_fields.CardsRedis
		var id = 1
		for _, v := range cards {
			json.Unmarshal([]byte(v), &cb)
			cb.ID = id
			cardBytes = append(cardBytes, cb)
			id++
		}
		c.JSON(http.StatusOK, gin.H{"cards": cardBytes})
	}

}

func AddCards(c *gin.Context) {
	redisClient := utils.GetRedis()

	var fields ebs_fields.CardsRedis
	err := c.ShouldBindWith(&fields, binding.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "unmarshalling_error"})
	} else {
		buf, _ := json.Marshal(fields)
		username := c.GetString("username")
		if username == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		} else {
			// make sure the length of the card and expDate is valid
			z := &redis.Z{
				Member: buf,
			}
			if fields.IsMain {
				// refactor me, please!
				redisClient.HSet(username, "main_card", buf)

				redisClient.ZAdd(username+":cards", z)
			} else {
				_, err := redisClient.ZAdd(username+":cards", z).Result()
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
					return
				}
			}
			c.JSON(http.StatusCreated, gin.H{"username": username, "cards": fields})
		}
	}

}

func EditCard(c *gin.Context) {
	redisClient := utils.GetRedis()

	var fields ebs_fields.CardsRedis

	err := c.ShouldBindWith(&fields, binding.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "unmarshalling_error"})
		// there is no error in the request body
	} else {
		buf, _ := json.Marshal(fields)

		username := c.GetString("username")
		if username == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		} else if fields.ID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"message": "card id not provided", "code": "card_id_not_provided"})
			return
		}
		// core functionality
		id := fields.ID
		{
			// step 1: removing the card; copied from RemoveCard
			if fields.IsMain {
				redisClient.HDel(username+":cards", "main_card")
			} else {
				_, err := redisClient.ZRemRangeByRank(username+":cards", int64(id-1), int64(id-1)).Result()
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "message": "unable_to_delete"})
					return
				}
			}
		}

		// step 2: Add the card; copied from AddCard

		{
			z := &redis.Z{
				Member: buf,
			}
			if fields.IsMain {
				// refactor me, please!
				redisClient.HSet(username, "main_card", buf)

				redisClient.ZAdd(username+":cards", z)
			} else {
				_, err := redisClient.ZAdd(username+":cards", z).Result()
				if err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
					return
				}
			}
		}
		c.JSON(http.StatusOK, gin.H{"username": username, "cards": fields})

	}

}

// this will work, but it is quite unpredictable
func RemoveCard(c *gin.Context) {
	redisClient := utils.GetRedis()

	var fields ebs_fields.ItemID
	err := c.ShouldBindWith(&fields, binding.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "unmarshalling_error"})
		// there is no error in the request body
	} else {
		username := c.GetString("username")
		if username == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		} else if fields.ID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"message": "card id not provided", "code": "card_id_not_provided"})
			return
		}
		// core functionality
		id := fields.ID

		if fields.IsMain {
			redisClient.HDel(username+":cards", "main_card")
		} else {
			_, err := redisClient.ZRemRangeByRank(username+":cards", int64(id-1), int64(id-1)).Result()
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "message": "unable_to_delete"})
				return
			}
		}

		c.JSON(http.StatusOK, gin.H{"username": username, "cards": fields})

	}

}
func AddMobile(c *gin.Context) {
	redisClient := utils.GetRedis()

	var fields ebs_fields.MobileRedis
	err := c.ShouldBindWith(&fields, binding.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "unmarshalling_error"})
	} else {
		buf, _ := json.Marshal(fields)
		username := c.GetString("username")
		if username == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		} else {
			if fields.IsMain {
				redisClient.HSet(username, "main_mobile", buf)
				redisClient.SAdd(username+":cards", buf)
			} else {
				redisClient.SAdd(username+":mobile_numbers", buf)
			}

			c.JSON(http.StatusCreated, gin.H{"username": username, "cards": string(buf)})
		}
	}

}

func GetMobile(c *gin.Context) {
	redisClient := utils.GetRedis()

	var fields ebs_fields.CardsRedis
	err := c.ShouldBindWith(&fields, binding.JSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "unmarshalling_error"})
	} else {
		buf, _ := json.Marshal(fields)
		username := c.GetString("username")
		if username == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"message": "unauthorized access", "code": "unauthorized_access"})
		} else {
			if fields.IsMain {
				redisClient.HSet(username, "main_mobile", buf)
				redisClient.SAdd(username+":mobile_numbers", buf)
			} else {
				redisClient.SAdd(username+":mobile_numbers", buf)
			}

			c.JSON(http.StatusCreated, gin.H{"username": username, "mobile_numbers": string(buf)})
		}
	}

}
