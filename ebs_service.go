package main

import (
	"encoding/json"
	"html/template"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/cards"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/dashboard"
	"github.com/adonese/noebs/docs"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/merchant"
	"github.com/adonese/noebs/utils"
	"github.com/bradfitz/iter"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	ginSwagger "github.com/swaggo/gin-swagger"
	"github.com/swaggo/gin-swagger/swaggerFiles"
)

var log = logrus.New()
var redisClient = utils.GetRedisClient("")
var database, _ = utils.Database("sqlite3", "test.db")

var auth gateway.JWTAuth
var service = utils.Service{Db: database, Redis: redisClient}
var consumerService = consumer.Service{Service: service}
var cardService = cards.Service{Redis: redisClient}
var dashService = dashboard.Service{Redis: redisClient}
var state = consumer.State{}
var merchantServices = merchant.Merchant{}

//GetMainEngine function responsible for getting all of our routes to be delivered for gin
func GetMainEngine() *gin.Engine {

	route := gin.Default()
	//metrics := Metrics()
	//p := ginprometheus.NewPrometheus("gin")
	instrument := gateway.Instrumentation()

	route.Use(instrument)

	route.HandleMethodNotAllowed = true
	route.POST("/ebs/*all", EBS)

	route.Use(gateway.OptionsMiddleware)

	route.SetFuncMap(template.FuncMap{"N": iter.N, "time": dashboard.TimeFormatter})

	route.LoadHTMLGlob("./dashboard/template/*")

	route.Static("/dashboard/assets", "./dashboard/template")

	route.POST("/generate_api_key", state.GenerateAPIKey)
	route.POST("/workingKey", WorkingKey)
	route.POST("/cardTransfer", CardTransfer)
	route.POST("/voucher", GenerateVoucher)
	route.POST("/voucher/cash_in", VoucherCashIn)
	route.POST("/cashout", VoucherCashOut)
	route.POST("/purchase", Purchase)
	route.POST("/cashIn", CashIn)
	route.POST("/cashOut", CashOut)
	route.POST("/billInquiry", BillInquiry)
	route.POST("/billPayment", BillPayment)
	route.POST("/bills", TopUpPayment)
	route.POST("/changePin", ChangePIN)
	route.POST("/miniStatement", MiniStatement)
	route.POST("/isAlive", IsAlive)
	route.POST("/balance", Balance)
	route.POST("/refund", Refund)
	route.POST("/toAccount", ToAccount)
	route.POST("/statement", Statement)

	route.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": true})
	})

	route.GET("/wrk", isAliveWrk)

	route.GET("/metrics", gin.WrapH(promhttp.Handler()))

	dashboardGroup := route.Group("/dashboard")
	//dashboardGroup.Use(gateway.CORSMiddleware())
	{
		dashboardGroup.GET("/get_tid", dashService.TransactionByTid)
		dashboardGroup.GET("/get", dashService.TransactionByTid)
		dashboardGroup.GET("/create", dashService.MakeDummyTransaction)
		dashboardGroup.GET("/all", dashService.GetAll)
		dashboardGroup.GET("/count", dashService.TransactionsCount)
		dashboardGroup.GET("/settlement", dashService.DailySettlement)
		dashboardGroup.GET("/merchant", dashService.MerchantTransactionsEndpoint)
		dashboardGroup.GET("/merchant/:id", dashService.MerchantViews)

		dashboardGroup.POST("/issues", dashService.ReportIssueEndpoint)

		dashboardGroup.GET("/", dashService.BrowserDashboard)
		dashboardGroup.GET("/test_browser", dashService.IndexPage)
		dashboardGroup.Any("/hearout", dashService.LandingPage)
		dashboardGroup.GET("/stream", dashService.Stream)
		dashboardGroup.Any("/merchants", dashService.MerchantRegistration)
	}

	route.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	cons := route.Group("/consumer")

	//cons.Use(gateway.OptionsMiddleware)
	// we want to use /v2 for consumer and merchant services
	{
		//cons.GET("/rate", gin.WrapF(consumer.Rate))
		cons.POST("/register", state.CreateUser)
		cons.POST("/refresh", state.RefreshHandler)
		cons.POST("/logout", state.LogOut)

		cons.POST("/balance", consumerService.Balance)
		cons.POST("/is_alive", consumerService.IsAlive)
		cons.POST("/bill_payment", consumerService.BillPayment)
		cons.POST("/bill_inquiry", consumerService.BillInquiry)
		cons.POST("/p2p", consumerService.CardTransfer)
		cons.POST("/account", consumerService.AccountTransfer)
		cons.POST("/purchase", consumerService.Purchase)
		cons.POST("/status", consumerService.Status)
		cons.POST("/key", consumerService.WorkingKey)
		cons.POST("/ipin", consumerService.IPinChange)
		cons.POST("/generate_qr", consumerService.QRGeneration)
		cons.POST("/qr_payment", consumerService.QRPayment)
		cons.POST("/generate_ipin", consumerService.GenerateIpin)
		cons.POST("/complete_ipin", consumerService.CompleteIpin)

		cons.POST("/qr_refund", consumerService.QRRefund)
		cons.POST("/card_info", consumerService.EbsGetCardInfo)
		cons.POST("/pan_from_mobile", consumerService.GetMSISDNFromCard)
		cons.GET("/mobile2pan", consumerService.CardFromNumber)
		cons.GET("/nec2name", consumerService.NecToName)
		cons.POST("/tokenize", cardService.Tokenize)

		cons.POST("/login", state.LoginHandler)
		cons.Use(auth.AuthMiddleware())
		cons.GET("/get_cards", consumerService.GetCards)
		cons.POST("/add_card", consumerService.AddCards)

		cons.PUT("/edit_card", consumerService.EditCard)
		cons.DELETE("/delete_card", consumerService.RemoveCard)

		cons.GET("/get_mobile", consumerService.GetMobile)
		cons.POST("/add_mobile", consumerService.AddMobile)

		cons.POST("/test", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"message": true})
		})
	}

	mGroup := route.Group("/merchant")
	mGroup.GET("/", merchantServices.GetMerchant)
	mGroup.POST("/new", consumerService.CreateMerchant)
	mGroup.POST("/login", merchantServices.Login)
	mGroup.POST("/m", merchantServices.AddBilling)
	mGroup.PUT("/update", merchantServices.Update)
	consumer.Routes("/v1", route, database, redisClient)
	return route
}

func init() {
	database.LogMode(true)
	database.AutoMigrate(&dashboard.Transaction{})
	binding.Validator = new(ebs_fields.DefaultValidator)
	auth.Init()
	state = consumer.State{Db: database, Redis: redisClient, Auth: &auth, UserModel: gateway.UserModel{}, UserLogin: gateway.UserLogin{}}
	merchantServices.Init(database, log)
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

	csh := consumer.NewCashout(redisClient)

	go csh.CashoutPub() // listener for noebs cashouts.
	go consumer.BillerHooks()

	go handleChan(redisClient)
	//FIXME #65 handle errors in go routine

	// logging and instrumentation
	file, err := os.OpenFile("logrus.log", os.O_CREATE|os.O_WRONLY, 0666)
	if err == nil {
		log.Out = file
	} else {
		log.Out = os.Stderr
	}
	log.Level = logrus.DebugLevel
	log.SetReportCaller(true) // get the method/function where the logging occured

	docs.SwaggerInfo.Title = "noebs Docs"
	//gin.SetMode(gin.ReleaseMode)
	log.Fatal(GetMainEngine().Run(":8080"))

}

// IsAlive godoc
// @Summary Get all transactions made by a specific terminal ID
// @Description get accounts
// @Accept json
// @Produce json
// @Param workingKey body ebs_fields.IsAliveFields true "Working Key Request Fields"
// @Success 200 {object} ebs_fields.GenericEBSResponseFields
// @Failure 400 {object} http.StatusBadRequest
// @Failure 404 {object} http.StatusNotFound
// @Failure 500 {object} http.InternalServerError
// @Router /workingKey [post]
//FIXME #68 make all merchant routers in an Env or struct
func IsAlive(c *gin.Context) {
	url := ebs_fields.EBSMerchantIP + ebs_fields.IsAliveEndpoint // EBS simulator endpoint url goes here.
	db, _ := utils.Database("sqlite3", "test.db")
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

// isAliveWrk is for testing only. We want to bypass our middleware checks and move
// up directly to ebs
//FIXME #68
func isAliveWrk(c *gin.Context) {
	//FIXME #69 make url embedded from struct
	url := ebs_fields.EBSMerchantIP + ebs_fields.IsAliveEndpoint
	req := strings.NewReader(`{"clientId": "ACTS", "systemTraceAuditNumber": 79, "tranDateTime": "200419085611", "terminalId": "18000377"}`)
	b, _ := json.Marshal(&req)
	ebs_fields.EBSHttpClient(url, b) // let that sink in
	c.JSON(http.StatusOK, gin.H{"result": true})

}

// WorkingKey godoc
// @Summary Get all transactions made by a specific terminal ID
// @Description get accounts
// @Accept  json
// @Produce  json
// @Param workingKey body ebs_fields.WorkingKeyFields true "Working Key Request Fields"
// @Success 200 {object} ebs_fields.GenericEBSResponseFields
// @Failure 400 {object} http.StatusBadRequest
// @Failure 404 {object} http.StatusNotFound
// @Failure 500 {object} http.InternalServerError
// @Router /workingKey [post]
//FIXME #68
func WorkingKey(c *gin.Context) {

	url := ebs_fields.EBSMerchantIP + ebs_fields.WorkingKeyEndpoint // EBS simulator endpoint url goes here.

	db, _ := utils.Database("sqlite3", "test.db")
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
		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{ErrorDetails: er})
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
// @Success 200 {object} ebs_fields.GenericEBSResponseFields
// @Failure 400 {object} http.StatusBadRequest
// @Failure 404 {object} http.StatusNotFound
// @Failure 500 {object} http.InternalServerError
// @Router /purchase [post]
//FIXME #68
func Purchase(c *gin.Context) {
	url := ebs_fields.EBSMerchantIP + ebs_fields.PurchaseEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db, _ := utils.Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.PurchaseFields{}
	bindingErr := c.ShouldBindWith(&fields, binding.JSON)
	if bindingErr == nil {
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{ErrorDetails: er})
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

		uid := generateUUID()
		redisClient.HSet(fields.TerminalID+":purchase", uid, &res)

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
// @Success 200 {object} ebs_fields.GenericEBSResponseFields
// @Failure 400 {object} http.StatusBadRequest
// @Failure 404 {object} http.StatusNotFound
// @Failure 500 {object} http.InternalServerError
// @Router /purchase [post]
//FIXME issue #68
func Balance(c *gin.Context) {
	url := ebs_fields.EBSMerchantIP + ebs_fields.BalanceEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db, _ := utils.Database("sqlite3", "test.db")
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

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{ErrorDetails: er})
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
// @Success 200 {object} ebs_fields.GenericEBSResponseFields
// @Failure 400 {object} http.StatusBadRequest
// @Failure 404 {object} http.StatusNotFound
// @Failure 500 {object} http.InternalServerError
// @Router /cardTransfer [post]
//FIXME issue #68
func CardTransfer(c *gin.Context) {
	url := ebs_fields.EBSMerchantIP + ebs_fields.CardTransferEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db, _ := utils.Database("sqlite3", "test.db")
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
		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{ErrorDetails: er})
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
// @Success 200 {object} ebs_fields.GenericEBSResponseFields
// @Failure 400 {object} http.StatusBadRequest
// @Failure 404 {object} http.StatusNotFound
// @Failure 500 {object} http.InternalServerError
// @Router /billInquiry [post]
//FIXME issue #68
func BillInquiry(c *gin.Context) {

	url := ebs_fields.EBSMerchantIP + ebs_fields.BillInquiryEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db, _ := utils.Database("sqlite3", "test.db")
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

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{ErrorDetails: er})
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
// @Success 200 {object} ebs_fields.GenericEBSResponseFields
// @Failure 400 {object} http.StatusBadRequest
// @Failure 404 {object} http.StatusNotFound
// @Failure 500 {object} http.InternalServerError
// @Router /billPayment [post]
//FIXME issue #68
func BillPayment(c *gin.Context) {

	url := ebs_fields.EBSMerchantIP + ebs_fields.BillPaymentEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db, _ := utils.Database("sqlite3", "test.db")
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

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.BillPaymentTransaction

		res.MaskPAN()

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

//TopUpPayment to perform electricity and telecos topups
func TopUpPayment(c *gin.Context) {

	url := ebs_fields.EBSMerchantIP + ebs_fields.BillPrepaymentEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db, _ := utils.Database("sqlite3", "test.db")
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

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: er})
		}

		// the only part left is fixing EBS errors. Formalizing them per se.
		code, res, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
		log.Printf("response is: %d, %+v, %v", code, res, ebsErr)

		transaction := dashboard.Transaction{
			GenericEBSResponseFields: res.GenericEBSResponseFields,
		}

		transaction.EBSServiceName = ebs_fields.BillPaymentTransaction

		res.MaskPAN()

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
// @Success 200 {object} ebs_fields.GenericEBSResponseFields
// @Failure 400 {object} http.StatusBadRequest
// @Failure 404 {object} http.StatusNotFound
// @Failure 500 {object} http.InternalServerError
// @Router /changePin [post]
//FIXME issue #68
func ChangePIN(c *gin.Context) {

	url := ebs_fields.EBSMerchantIP + ebs_fields.ChangePINEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db, _ := utils.Database("sqlite3", "test.db")
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

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{ErrorDetails: er})
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
// @Success 200 {object} ebs_fields.GenericEBSResponseFields
// @Failure 400 {object} http.StatusBadRequest
// @Failure 404 {object} http.StatusNotFound
// @Failure 500 {object} http.InternalServerError
// @Router /cashOut [post]
//FIXME issue #68
func CashOut(c *gin.Context) {

	url := ebs_fields.EBSMerchantIP + ebs_fields.CashOutEndpoint // EBS simulator endpoint url goes here.
	db, _ := utils.Database("sqlite3", "test.db")
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

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{ErrorDetails: er})
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

//VoucherCashOut for non-card based transactions
func VoucherCashOut(c *gin.Context) {

	url := ebs_fields.EBSMerchantIP + ebs_fields.VoucherCashOutWithAmountEndpoint // EBS simulator endpoint url goes here.
	db, _ := utils.Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.VoucherCashOutFields{}
	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{ErrorDetails: er})
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

//VoucherCashIn for non-card based transactions
func VoucherCashIn(c *gin.Context) {

	url := ebs_fields.EBSMerchantIP + ebs_fields.VoucherCashInEndpoint // EBS simulator endpoint url goes here.
	db, _ := utils.Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.VoucherCashInFields{}
	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{ErrorDetails: er})
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

//Statement for non-card based transactions
func Statement(c *gin.Context) {

	url := ebs_fields.EBSMerchantIP + ebs_fields.MiniStatementEndpoint // EBS simulator endpoint url goes here.
	db, _ := utils.Database("sqlite3", "test.db")
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

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{ErrorDetails: er})
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

//GenerateVoucher for non-card based transactions
func GenerateVoucher(c *gin.Context) {

	url := ebs_fields.EBSMerchantIP + ebs_fields.GenerateVoucherEndpoint // EBS simulator endpoint url goes here.
	db, _ := utils.Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.GenerateVoucherFields{}
	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{ErrorDetails: er})
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
// @Success 200 {object} ebs_fields.GenericEBSResponseFields
// @Failure 400 {object} http.StatusBadRequest
// @Failure 404 {object} http.StatusNotFound
// @Failure 500 {object} http.InternalServerError
// @Router /cashIn [post]
//FIXME issue #68
func CashIn(c *gin.Context) {

	url := ebs_fields.EBSMerchantIP + ebs_fields.CashInEndpoint // EBS simulator endpoint url goes here.
	db, _ := utils.Database("sqlite3", "test.db")
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

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{ErrorDetails: er})
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

// CashIn godoc
// @Summary Get all transactions made by a specific terminal ID
// @Description get accounts
// @Accept  json
// @Produce  json
// @Param cashOut body ebs_fields.CashInFields true "Cash In Request Fields"
// @Success 200 {object} ebs_fields.GenericEBSResponseFields
// @Failure 400 {object} http.StatusBadRequest
// @Failure 404 {object} http.StatusNotFound
// @Failure 500 {object} http.InternalServerError
// @Router /cashIn [post]
//FIXME issue #68
func ToAccount(c *gin.Context) {

	url := ebs_fields.EBSMerchantIP + ebs_fields.AccountTransferEndpoint // EBS simulator endpoint url goes here.
	db, _ := utils.Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.AccountTransferFields{}
	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {

	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {

			details = append(details, ebs_fields.ErrorToString(err))
		}

		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{ErrorDetails: er})
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
// @Success 200 {object} ebs_fields.GenericEBSResponseFields
// @Failure 400 {object} http.StatusBadRequest
// @Failure 404 {object} http.StatusNotFound
// @Failure 500 {object} http.InternalServerError
// @Router /miniStatement [post]
//FIXME issue #68
func MiniStatement(c *gin.Context) {

	url := ebs_fields.EBSMerchantIP + ebs_fields.MiniStatementEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db, _ := utils.Database("sqlite3", "test.db")
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

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{ErrorDetails: er})
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
	db, _ := utils.Database("sqlite3", "test.db")
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

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})
	case nil:
		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{ErrorDetails: er})
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

//Refund requests a refund for supported refund services in ebs merchant. Currnetly, it is not working
//FIXME issue #68
func Refund(c *gin.Context) {
	url := ebs_fields.EBSMerchantIP + ebs_fields.RefundEndpoint // EBS simulator endpoint url goes here.
	//FIXME instead of hardcoding it here, maybe offer it in the some struct that handles everything about the application configurations.
	// consume the request here and pass it over onto the EBS.
	// marshal the request
	// fuck. This shouldn't be here at all.

	db, _ := utils.Database("sqlite3", "test.db")
	defer db.Close()

	var fields = ebs_fields.RefundFields{}
	bindingErr := c.ShouldBindWith(&fields, binding.JSON)

	switch bindingErr := bindingErr.(type) {
	case validator.ValidationErrors:
		var details []ebs_fields.ErrDetails

		for _, err := range bindingErr {
			details = append(details, ebs_fields.ErrorToString(err))
		}
		payload := ebs_fields.ErrorDetails{Details: details, Code: 400, Message: "Request fields validation error", Status: ebs_fields.BadRequest}

		c.JSON(http.StatusBadRequest, ebs_fields.ErrorResponse{ErrorDetails: payload})

	case nil:

		jsonBuffer, err := json.Marshal(fields)
		if err != nil {
			// there's an error in parsing the struct. Server error.
			er := ebs_fields.ErrorDetails{Details: nil, Code: 400, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
			c.AbortWithStatusJSON(400, ebs_fields.ErrorResponse{ErrorDetails: er})
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

//EBS is an EBS compatible endpoint! Well.
// it really just works as a reverse proxy with db and nothing more!
func EBS(c *gin.Context) {
	url := c.Request.URL.Path
	endpoint := strings.Split(url, "/")[2]
	ebsURL := ebs_fields.EBSMerchantIP + endpoint
	log.Printf("the url is: %v", url)

	db, _ := utils.Database("sqlite3", "test.db")
	defer db.Close()

	jsonBuffer, err := ioutil.ReadAll(c.Request.Body)
	if err != nil {
		panic(err)
	}
	_, res, _ := ebs_fields.EBSHttpClient(ebsURL, jsonBuffer)
	// now write it to the DB :)
	transaction := dashboard.Transaction{
		GenericEBSResponseFields: res.GenericEBSResponseFields,
	}

	transaction.EBSServiceName = endpoint
	// God please make it works.
	db.Create(&transaction)
	c.JSON(http.StatusOK, res)
}
