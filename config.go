package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"

	firebase "firebase.google.com/go/v4"
	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/dashboard"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/merchant"
	"github.com/adonese/noebs/utils"
	"github.com/bradfitz/iter"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm/logger"

	"google.golang.org/api/option"
)

//go:embed .secrets.json
var secretsFile []byte

func parseConfig(data *ebs_fields.NoebsConfig) error {
	if err := json.Unmarshal(secretsFile, data); err != nil {
		logrusLogger.Printf("Error in parsing config files: %v", err)
		return err
	} else {
		logrusLogger.Printf("the data is: %#v", data)
		return nil
	}
}

func getFirebase() (*firebase.App, error) {
	opt := option.WithCredentialsFile("firebase-sdk.json")
	app, err := firebase.NewApp(context.Background(), nil, opt)
	if err != nil {
		return nil, fmt.Errorf("error initializing app: %v", err)
	}
	return app, nil
}

func verifyToken(f *firebase.App, token string) (string, error) {
	ctx := context.Background()
	fb, err := f.Auth(ctx)
	if err != nil {
		return "", err
	}
	idToken, err := fb.VerifyIDToken(ctx, token)
	if err != nil {
		return "", err
	}
	log.Printf("Verified ID token: %v\n", idToken)
	return idToken.Audience, nil
}

//GetMainEngine function responsible for getting all of our routes to be delivered for gin
func GetMainEngine() *gin.Engine {

	route := gin.Default()
	instrument := gateway.Instrumentation()
	route.Use(instrument)
	// route.Use(sentrygin.New(sentrygin.Options{}))
	route.HandleMethodNotAllowed = true
	route.POST("/ebs/*all", merchantServices.EBS)
	route.Use(gateway.OptionsMiddleware)
	route.SetFuncMap(template.FuncMap{"N": iter.N, "time": dashboard.TimeFormatter})
	route.LoadHTMLGlob("./dashboard/template/*")
	route.Static("/dashboard/assets", "./dashboard/template")
	route.POST("/generate_api_key", consumerService.GenerateAPIKey)
	route.POST("/workingKey", merchantServices.WorkingKey)
	route.POST("/cardTransfer", merchantServices.CardTransfer)
	route.POST("/voucher", merchantServices.GenerateVoucher)
	route.POST("/voucher/cash_in", merchantServices.VoucherCashIn)
	route.POST("/cashout", merchantServices.VoucherCashOut)
	route.POST("/purchase", merchantServices.Purchase)
	route.POST("/cashIn", merchantServices.CashIn)
	route.POST("/cashOut", merchantServices.CashOut)
	route.POST("/billInquiry", merchantServices.BillInquiry)
	route.POST("/billPayment", merchantServices.BillPayment)
	route.POST("/bills", merchantServices.TopUpPayment)
	route.POST("/changePin", merchantServices.ChangePIN)
	route.POST("/miniStatement", merchantServices.MiniStatement)
	route.POST("/isAlive", merchantServices.IsAlive)
	route.POST("/balance", merchantServices.Balance)
	route.POST("/refund", merchantServices.Refund)
	route.POST("/toAccount", merchantServices.ToAccount)
	route.POST("/statement", merchantServices.Statement)
	route.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": true})
	})

	route.GET("/wrk", merchantServices.IsAliveWrk)
	route.GET("/metrics", gin.WrapH(promhttp.Handler()))
	dashboardGroup := route.Group("/dashboard")
	{
		dashboardGroup.GET("/", dashService.BrowserDashboard)
		dashboardGroup.GET("/get_tid", dashService.TransactionByTid)
		dashboardGroup.GET("/get", dashService.TransactionByTid)
		dashboardGroup.GET("/create", dashService.MakeDummyTransaction)
		dashboardGroup.GET("/all", dashService.GetAll)
		dashboardGroup.GET("/all/:id", dashService.GetID)
		dashboardGroup.GET("/count", dashService.TransactionsCount)
		dashboardGroup.GET("/settlement", dashService.DailySettlement)
		dashboardGroup.GET("/merchant", dashService.MerchantTransactionsEndpoint)
		dashboardGroup.GET("/merchant/:id", dashService.MerchantViews)
		dashboardGroup.POST("/issues", dashService.ReportIssueEndpoint)
		dashboardGroup.GET("/status", dashService.QRStatus)
		dashboardGroup.GET("/test_browser", dashService.IndexPage)
		dashboardGroup.GET("/stream", dashService.Stream)
	}

	cons := route.Group("/consumer")

	{
		cons.POST("/register", consumerService.CreateUser)
		cons.POST("/refresh", consumerService.RefreshHandler)
		cons.POST("/balance", consumerService.Balance)
		cons.POST("/status", consumerService.TransactionStatus)
		cons.POST("/is_alive", consumerService.IsAlive)
		cons.POST("/bill_payment", consumerService.BillPayment)
		cons.POST("/bill_inquiry", consumerService.BillInquiry)
		cons.POST("/p2p", consumerService.CardTransfer)
		cons.POST("/cashIn", consumerService.CashIn)
		cons.POST("/cashOut", consumerService.CashOut)
		cons.POST("/account", consumerService.AccountTransfer)
		cons.POST("/purchase", consumerService.Purchase)
		cons.POST("/n/status", consumerService.Status)
		cons.POST("/key", consumerService.WorkingKey)
		cons.POST("/ipin", consumerService.IPinChange)
		cons.POST("/generate_qr", consumerService.QRMerchantRegistration)
		cons.POST("/qr_payment", consumerService.QRPayment)
		cons.POST("/qr_status", consumerService.QRTransactions)
		cons.POST("/ipin_key", consumerService.IPINKey)
		cons.POST("/generate_ipin", consumerService.GenerateIpin)
		cons.POST("/complete_ipin", consumerService.CompleteIpin)
		cons.POST("/qr_refund", consumerService.QRRefund)
		cons.POST("/qr_complete", consumerService.QRComplete)
		cons.POST("/card_info", consumerService.EbsGetCardInfo)
		cons.POST("/pan_from_mobile", consumerService.GetMSISDNFromCard)
		cons.GET("/mobile2pan", consumerService.CardFromNumber)
		cons.GET("/nec2name", consumerService.NecToName)
		cons.POST("/vouchers/generate", consumerService.GenerateVoucher)
		cons.POST("/cards/new", consumerService.RegisterCard)
		cons.POST("/cards/complete", consumerService.CompleteRegistration)
		cons.POST("/login", consumerService.LoginHandler)
		cons.POST("/change_password", consumerService.ChangePassword)
		cons.POST("/otp/generate", consumerService.GenerateSignInCode)
		cons.POST("/otp/login", consumerService.SingleLoginHandler)
		cons.POST("/verify_firebase", consumerService.VerifyFirebase)
		cons.POST("/test", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"message": true})
		})
		cons.Use(auth.AuthMiddleware())
		cons.GET("/get_cards", consumerService.GetCards)
		cons.POST("/add_card", consumerService.AddCards)
		cons.PUT("/edit_card", consumerService.EditCard)
		cons.DELETE("/delete_card", consumerService.RemoveCard)

		cons.POST("/payment_token", consumerService.GeneratePaymentToken)
		cons.POST("/payment_token/quick_pay", consumerService.NoebsQuickPayment)
		cons.GET("/payment_token/", consumerService.GetPaymentToken)
	}
	return route
}

func init() {
	var err error

	// Initialize database
	database, err = utils.Database("test.db")
	if err != nil {
		logrusLogger.Fatalf("error in connecting to db: %v", err)
	}

	logrusLogger.Level = logrus.DebugLevel
	logrusLogger.SetReportCaller(true)

	// Parse noebs system-level configurations
	if err = parseConfig(&noebsConfig); err != nil {
		logrusLogger.Printf("error in parsing file: %v", err)
	}
	noebsConfig.Defaults()
	logrusLogger.Printf("The final config file is: %#v", noebsConfig)

	// Initialize sentry
	// sentry.Init(sentry.ClientOptions{
	// 	Dsn: noebsConfig.Sentry,
	// 	// Set TracesSampleRate to 1.0 to capture 100%
	// 	// of transactions for performance monitoring.
	// 	// We recommend adjusting this value in production,
	// 	TracesSampleRate: 1.0,
	// })

	firebaseApp, err := getFirebase()
	// gorm debug-level logger
	database.Logger.LogMode(logger.Info)
	database.AutoMigrate(&ebs_fields.User{}, &ebs_fields.Card{}, &ebs_fields.EBSResponse{}, &ebs_fields.PaymentToken{})
	auth = gateway.JWTAuth{NoebsConfig: noebsConfig}

	auth.Init()
	binding.Validator = new(ebs_fields.DefaultValidator)
	consumerService = consumer.Service{Db: database, Redis: redisClient, NoebsConfig: noebsConfig, Logger: logrusLogger, FirebaseApp: firebaseApp, Auth: &auth}
	dashService = dashboard.Service{Redis: redisClient, Db: database}
	merchantServices = merchant.Service{Db: database, Redis: redisClient, Logger: logrusLogger, NoebsConfig: noebsConfig}
}
