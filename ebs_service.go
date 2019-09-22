package main

import (
	"encoding/json"
	gateway "github.com/adonese/noebs/apigateway"
	consumer2 "github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/dashboard"
	"github.com/adonese/noebs/docs"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/utils"
	"github.com/bradfitz/iter"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"github.com/swaggo/gin-swagger"
	"github.com/swaggo/gin-swagger/swaggerFiles"
	"github.com/zsais/go-gin-prometheus"
	"gopkg.in/go-playground/validator.v9"
	"html/template"
	"net/http"
	"os"
	"strings"
)

var log = logrus.New()
var billChan = make(chan ebs_fields.GenericEBSResponseFields)
var uChan = make(chan string)

func GetMainEngine() *gin.Engine {

	route := gin.Default()
	//metrics := Metrics()
	p := ginprometheus.NewPrometheus("gin")
	p.Use(route)

	route.HandleMethodNotAllowed = true

	route.Use(gateway.OptionsMiddleware)

	route.SetFuncMap(template.FuncMap{"N": iter.N})
	route.LoadHTMLFiles("./dashboard/template/table.html")

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
		dashboardGroup.GET("/", dashboard.BrowerDashboard)
		dashboardGroup.GET("/stream", dashboard.Stream)
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
	consumer.GET("/mobile2pan", consumer2.CardFromNumber)

	consumer.Use(gateway.AuthMiddleware())
	{
		consumer.GET("/get_cards", consumer2.GetCards)
		consumer.POST("/add_card", consumer2.AddCards)

		consumer.PUT("/edit_card", consumer2.EditCard)
		consumer.DELETE("/delete_card", consumer2.RemoveCard)

		consumer.GET("/get_mobile", consumer2.GetMobile)
		consumer.POST("/add_mobile", consumer2.AddMobile)

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
// @termsOfService http://soluspay.net/terms
// @contact.name API Support
// @contact.url https://soluspay.net/support
// @contact.email adonese@soluspay.net
// @license.name Apache 2.0
// @license.url https://github.com/adonese/noebs/LICENSE
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
	}
	log.Level = logrus.DebugLevel
	log.SetReportCaller(true) // get the method/function where the logging occured

	// go routines before blocking
	go handleChan(uChan)

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
	url := ebs_fields.EBSMerchantIP + ebs_fields.IsAliveEndpoint // EBS simulator endpoint url goes here.
	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.IsAliveFields{}

	// use bind to get free Form support rendering!
	// there is no practical need of using c.ShouldBindBodyWith;
	// Bind is more performant than ShouldBindBodyWith; the later copies the request body and reuse it
	// while Bind works directly on the responseBody stream.
	// More importantly, Bind smartly handles Forms rendering and validations; ShouldBindBodyWith forces you
	// into using only a *pre-specified* binding schema
	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, payload)

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, er)
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.IsAliveTransaction

		// return a masked pan
		transaction.MaskPAN()

		// God please make it works.
		if err := db.Table("transactions").Create(&transaction).Error; err != nil {
			log.WithFields(logrus.Fields{
				"error":   err.Error(),
				"details": "Error in writing to Database",
			}).Info("Problem in transaction table committing")
		}

		if ebsErr != nil {
			// convert ebs res code to int
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.GenericEBSResponseFields, Message: ebs_fields.EBSError}
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

	url := ebs_fields.EBSMerchantIP + ebs_fields.WorkingKeyEndpoint // EBS simulator endpoint url goes here.

	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.WorkingKeyFields{}

	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{payload})

	case nil:
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
			return
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.WorkingKeyTransaction
		// God please make it works.
		if err := db.Create(&transaction).Error; err != nil {
			log.WithFields(logrus.Fields{
				"error":   err.Error(),
				"details": "Error in writing to Database",
			}).Info("Problem in transaction table committing")
		}
		db.Commit()

		if ebsErr != nil {
			// convert ebs res code to int
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.GenericEBSResponseFields, Message: ebs_fields.EBSError}
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
	url := ebs_fields.EBSMerchantIP + ebs_fields.PurchaseEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.PurchaseFields{}
	bindingErr := c.ShouldBindWith(&fields, binding.JSON)
	if bindingErr == nil {
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
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
		if err := db.Table("transactions").Create(&transaction).Error; err != nil {
			logrus.WithFields(logrus.Fields{
				"error":   "unable to migrate purchase model",
				"message": err.Error(),
			}).Info("error in migrating purchase model")
		}
		redisClient := utils.GetRedis()

		redisClient.LPush(fields.TerminalID+":purchase", &res)
		redisClient.Incr(fields.TerminalID + ":number_purchase_transactions")

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.GenericEBSResponseFields, Message: ebs_fields.EBSError}
			redisClient.Incr(fields.TerminalID + ":failed_transactions")
			c.JSON(code, payload)
		} else {

			redisClient.Incr(fields.TerminalID + ":successful_transactions")
			c.JSON(code, gin.H{"ebs_response": res})
		}
	} else {
		if valErr, ok := bindingErr.(validator.ValidationErrors); ok {
			payload := validateRequest(valErr)
			c.JSON(http.StatusBadRequest, payload)
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"message": bindingErr.Error(), "code": "generic_error"})
		}
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
	url := ebs_fields.EBSMerchantIP + ebs_fields.BalanceEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.BalanceFields{}

	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.BalanceTransaction
		// return a masked pan

		// God please make it works.
		db.Table("transactions").Create(&transaction)

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.GenericEBSResponseFields, Message: ebs_fields.EBSError}
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

	url := ebs_fields.EBSMerchantIP + ebs_fields.CardTransferEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.CardTransferFields{}

	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.CardTransferTransaction
		// God please make it works.
		db.Table("transactions").Create(&transaction)

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.GenericEBSResponseFields, Message: ebs_fields.EBSError}
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

	url := ebs_fields.EBSMerchantIP + ebs_fields.BillInquiryEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.BillInquiryFields{}

	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.BillInquiryTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
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

	url := ebs_fields.EBSMerchantIP + ebs_fields.BillPaymentEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.BillPaymentFields{}
	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.BillPaymentTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.GenericEBSResponseFields, Message: ebs_fields.EBSError}
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

	url := ebs_fields.EBSMerchantIP + ebs_fields.ChangePINEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ChangePINFields{}
	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.ChangePINTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.GenericEBSResponseFields, Message: ebs_fields.EBSError}
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

	url := ebs_fields.EBSMerchantIP + ebs_fields.CashOutEndpoint // EBS simulator endpoint url goes here.
	// This function flow:
	// - open a DB connection (getDB)
	// - check for the binding errors
	//
	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.CashOutFields{}
	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.CashOutTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.GenericEBSResponseFields, Message: ebs_fields.EBSError}
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

	url := ebs_fields.EBSMerchantIP + ebs_fields.CashInEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.CashInFields{}
	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.CashInTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.GenericEBSResponseFields, Message: ebs_fields.EBSError}
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

	url := ebs_fields.EBSMerchantIP + ebs_fields.MiniStatementEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.MiniStatementFields{}

	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.MiniStatementTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

func testAPI(c *gin.Context) {

	url := ebs_fields.EBSMerchantIP + ebs_fields.WorkingKeyEndpoint // EBS simulator endpoint url goes here.

	// create Database function
	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.WorkingKeyFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{payload})
	case nil:
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.WorkingKeyTransaction
		// God please make it works.
		db.Create(&transaction)
		db.Commit()

		if ebsErr != nil {
			// convert ebs res code to int
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
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
	url := ebs_fields.EBSIp + ebs_fields.ConsumerPurchaseEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ConsumerPurchaseFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails
		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}
		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{payload})
	case nil:
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
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
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res.GenericEBSResponseFields, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})
		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

func ConsumerIsAlive(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerIsAliveEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ConsumerIsAliveFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		//// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		redisClient := utils.GetRedis()
		username, _ := utils.GetOrDefault(c.Keys, "username", "anon")
		utils.SaveRedisList(redisClient, username+":all_transactions", &res)

		transaction.EBSServiceName = ebs_fields.PurchaseTransaction

		if err := db.Table("transactions").Create(&transaction).Error; err != nil {
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
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

func ConsumerBillPayment(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerBillPaymentEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ConsumerBillPaymentFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{payload})

	case nil:
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		{
			// do some stuffs here regarding concurrency in GO
			if u := c.GetString("username"); u != "" {
				uChan <- u
			}
			billChan <- res.GenericEBSResponseFields
		}
		// mask the pan
		res.MaskPAN()

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.PurchaseTransaction

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
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

func ConsumerBillInquiry(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerBillInquiryEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ConsumerBillInquiryFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
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
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

func ConsumerBalance(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerBalanceEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ConsumerBalanceFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
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
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

func ConsumerWorkingKey(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerWorkingKeyEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ConsumerWorkingKeyFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
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
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

func ConsumerCardTransfer(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerCardTransferEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ConsumerCardTransferAndMobileFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{payload})

	case nil:

		// save this to redis
		if mobile := fields.Mobile; mobile != "" {
			redisClient := utils.GetRedis()
			redisClient.Set(fields.Mobile+":pan", fields.Pan, 0)
		}
		jsonBuffer, err := json.Marshal(fields.ConsumerCardTransferFields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
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
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

func ConsumerIPinChange(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerCardTransferEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ConsumerIPinFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
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
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
			c.JSON(code, payload)
		} else {
			c.JSON(code, gin.H{"ebs_response": res})

		}

	default:
		c.AbortWithStatusJSON(400, gin.H{"error": bindingErr.Error()})
	}
}

func ConsumerStatus(c *gin.Context) {
	url := ebs_fields.EBSIp + ebs_fields.ConsumerStatusEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db := Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.ConsumerStatusFields{}

	bindingErr := c.ShouldBindBodyWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: http.StatusBadRequest, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{er})
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
			payload := ebs_fields.ErrorDetails{Code: res.ResponseCode, Status: ebs_fields.EBSError, Details: res, Message: ebs_fields.EBSError}
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
