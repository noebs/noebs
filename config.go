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

	chat "github.com/tutipay/ws"
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

// GetMainEngine function responsible for getting all of our routes to be delivered for gin
func GetMainEngine() *gin.Engine {

	if !noebsConfig.IsDebug {
		gin.SetMode(gin.ReleaseMode)
	}
	route := gin.Default()
	instrument := gateway.Instrumentation()
	route.Use(instrument)
	// route.Use(sentrygin.New(sentrygin.Options{}))
	route.HandleMethodNotAllowed = true
	route.POST("/ebs/*all", merchantServices.EBS)
	route.GET("/ws", wsAdapter(hub))
	route.Use(gateway.NoebsCors(noebsConfig.Cors))
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
		cons.POST("/register_with_card", consumerService.RegisterWithCard)
		cons.POST("/refresh", consumerService.RefreshHandler)
		cons.POST("/balance", consumerService.Balance)
		cons.POST("/status", consumerService.TransactionStatus)
		cons.POST("/is_alive", consumerService.IsAlive)
		cons.POST("/bill_payment", consumerService.BillPayment)
		cons.POST("/bills", consumerService.GetBills)
		cons.GET("/guess_biller", consumerService.GetBiller)
		cons.POST("/bill_inquiry", consumerService.BillInquiry)
		cons.POST("/p2p", consumerService.CardTransfer)
		cons.POST("/cashIn", consumerService.CashIn)
		cons.POST("/cashOut", consumerService.CashOut)
		cons.POST("/account", consumerService.AccountTransfer)
		cons.POST("/purchase", consumerService.Purchase)
		cons.POST("/n/status", consumerService.Status)
		cons.POST("/key", consumerService.WorkingKey)
		cons.POST("/ipin", consumerService.IPinChange)
		cons.POST("/update_card_registartion", consumerService.UpdateCardRegistration)
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
		cons.GET("/transaction", gin.HandlerFunc(func(ctx *gin.Context) {
			var res ebs_fields.EBSResponse
			id := ctx.Query("uuid")
			if response, err := res.GetByUUID(id, database); err != nil {
				response, err = res.GetEBSUUID(id, database, &noebsConfig)
				if err != nil {
					ctx.JSON(http.StatusBadRequest, gin.H{"code": "not_found", "message": err.Error()})
					return
				}
			} else {
				ctx.JSON(http.StatusOK, response)
			}
		}))
		cons.GET("/users/cards", gin.HandlerFunc(func(ctx *gin.Context) {
			mobile := ctx.Query("mobile")
			if response, err := ebs_fields.GetCardsOrFail(mobile, database); err != nil {
				ctx.JSON(http.StatusBadRequest, gin.H{"code": "not_found", "message": err.Error()})
				return
			} else {
				ctx.JSON(http.StatusOK, response)
			}
		}))
		cons.POST("/otp/generate",
			gin.HandlerFunc(func(c *gin.Context) {
				consumerService.GenerateSignInCode(c, false)
			}))
		cons.POST("/otp/generate_insecure",
			gin.HandlerFunc(func(c *gin.Context) {
				consumerService.GenerateSignInCode(c, true)
			}))
		cons.GET("/notifications", consumerService.Notifications)
		cons.POST("/otp/login", consumerService.SingleLoginHandler)
		cons.POST("/otp/verify", consumerService.VerifyOTP)
		cons.POST("/otp/balance", consumerService.BalanceStep)
		cons.POST("/verify_firebase", consumerService.VerifyFirebase)
		cons.POST("/test", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"message": true})
		})
		cons.POST("/check_user", consumerService.CheckUser)

		cons.Use(auth.AuthMiddleware())
		cons.POST("/p2p_mobile", consumerService.MobileTransfer)
		cons.POST("/cards/set_main", consumerService.SetMainCard)
		cons.POST("/user/firebase", consumerService.AddFirebaseID)
		cons.Any("/beneficiary", consumerService.Beneficiaries)
		cons.POST("/change_password", consumerService.ChangePassword)
		cons.GET("/get_cards", consumerService.GetCards)
		cons.POST("/add_card", consumerService.AddCards)
		cons.PUT("/edit_card", consumerService.EditCard)
		cons.DELETE("/delete_card", consumerService.RemoveCard)
		cons.GET("/payment_token", consumerService.GetPaymentToken)
		cons.POST("/payment_token", consumerService.GeneratePaymentToken)
		cons.POST("/payment_token/quick_pay", consumerService.NoebsQuickPayment)
		cons.POST("/request_funds", consumerService.RequestFunds)
		cons.POST("/submit_contacts", func() gin.HandlerFunc {
			return func(c *gin.Context) {
				chat.SubmitContacts(c.GetString("mobile"), consumerService.NoebsConfig.DatabasePath, c.Writer, c.Request)
			}
		}())
	}
	return route
}

func init() {
	var err error

	logrusLogger.Level = logrus.DebugLevel
	logrusLogger.SetReportCaller(true)

	// Parse noebs system-level configurations
	if err = parseConfig(&noebsConfig); err != nil {
		logrusLogger.Printf("error in parsing file: %v", err)
	}
	noebsConfig.Defaults()
	path := "test.db"
	if noebsConfig.DatabasePath != "" {
		path = noebsConfig.DatabasePath
	}

	// Initialize database
	database, err = utils.Database(path)
	if err != nil {
		logrusLogger.Fatalf("error in connecting to db: %v", err)
	}

	logrusLogger.Printf("The final config file is: %#v", noebsConfig)

	// Initialize sentry
	// sentry.Init(sentry.ClientOptions{
	// 	Dsn: noebsConfig.Sentry,
	// 	// Set TracesSampleRate to 1.0 to capture 100%
	// 	// of transactions for performance monitoring.
	// 	// We recommend adjusting this value in production,
	// 	TracesSampleRate: 1.0,
	// })
	chatDb, err := chat.OpenDb("test.db")
	if err != nil {
		logrusLogger.Printf("The final config file is: %#v", err)
	}

	hub = *chat.NewHub(chatDb)

	firebaseApp, err := getFirebase()
	// gorm debug-level logger
	database.Logger.LogMode(logger.Info)

	// check database foreign key for user & credit_cards exists or not
	database.Migrator().DropConstraint(&consumer.PushData{}, "Transactions")
	database.Migrator().DropConstraint(&consumer.PushData{}, "fk_push_data_ebs_data")
	if err := database.Debug().AutoMigrate(&consumer.PushData{}, &ebs_fields.User{},
		&ebs_fields.Card{}, &ebs_fields.EBSResponse{}, &ebs_fields.Token{},
		&ebs_fields.CacheBillers{}, &ebs_fields.CacheCards{}, &ebs_fields.Beneficiary{}); err != nil {
		logrusLogger.Fatalf("error in migration: %v", err)
	}
	// check database foreign key for user & credit_cards exists or not
	database.Migrator().HasConstraint(&consumer.PushData{}, "Transactions")
	database.Migrator().HasConstraint(&consumer.PushData{}, "fk_push_data_ebs_data")

	auth = gateway.JWTAuth{NoebsConfig: noebsConfig}

	auth.Init()
	binding.Validator = new(ebs_fields.DefaultValidator)
	consumerService = consumer.Service{Db: database, Redis: redisClient, NoebsConfig: noebsConfig, Logger: logrusLogger, FirebaseApp: firebaseApp, Auth: &auth}
	dashService = dashboard.Service{Redis: redisClient, Db: database}
	merchantServices = merchant.Service{Db: database, Redis: redisClient, Logger: logrusLogger, NoebsConfig: noebsConfig}
	dataConfigs.DB = database
}

func wsAdapter(msg chat.Hub) gin.HandlerFunc {
	return func(c *gin.Context) {
		chat.ServeWs(&msg, c.Writer, c.Request)
	}
}
