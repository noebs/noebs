package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	firebase "firebase.google.com/go/v4"
	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/dashboard"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/merchant"
	"github.com/adonese/noebs/utils"
	"github.com/bradfitz/iter"
	"github.com/gofiber/adaptor/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/template/html/v2"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm/logger"

	chat "github.com/tutipay/ws"
	"google.golang.org/api/option"
)

var configFile = []byte("{}")

func loadConfig() []byte {
	secretsPath := ".secrets.json"

	if data, err := os.ReadFile(secretsPath); err == nil && len(data) > 0 {
		logrusLogger.Printf("Loaded config from %s", secretsPath)
		return data
	} else if err != nil && !os.IsNotExist(err) {
		logrusLogger.Printf("Failed to read config file %s: %v (falling back to embedded)", secretsPath, err)
	}

	return configFile
}

func resolveDashboardTemplateDir() string {
	candidates := []string{
		"./dashboard/template",
		"../dashboard/template",
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return filepath.Clean(candidate)
		}
	}
	return "./dashboard/template"
}

func resolveFirebaseCredentialsPath() (string, error) {
	candidates := []string{
		"firebase-sdk.json",
		"../firebase-sdk.json",
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return filepath.Clean(candidate), nil
		}
	}
	return "", fmt.Errorf("firebase credentials file not found")
}

func getFirebase() (*firebase.App, error) {
	credPath, err := resolveFirebaseCredentialsPath()
	if err != nil {
		return nil, err
	}
	opt := option.WithCredentialsFile(credPath)
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

// GetMainEngine function responsible for getting all of our routes to be delivered for fiber
func GetMainEngine() *fiber.App {
	templateDir := resolveDashboardTemplateDir()
	engine := html.New(templateDir, ".html")
	engine.AddFunc("N", iter.N)
	engine.AddFunc("time", dashboard.TimeFormatter)

	route := fiber.New(fiber.Config{Views: engine, ViewsLayout: "base"})
	route.Use(gateway.Instrumentation())
	route.Use(gateway.NoebsCors(noebsConfig.Cors))

	route.Post("/ebs/*", wrapHandler(merchantServices.EBS))
	route.Get("/ws", adaptor.HTTPHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chat.ServeWs(&hub, w, r)
	}))

	route.Static("/dashboard/assets", templateDir)
	route.Post("/generate_api_key", wrapHandler(consumerService.GenerateAPIKey))
	route.Post("/workingKey", wrapHandler(merchantServices.WorkingKey))
	route.Post("/cardTransfer", wrapHandler(merchantServices.CardTransfer))
	route.Post("/voucher", wrapHandler(merchantServices.GenerateVoucher))
	route.Post("/voucher/cash_in", wrapHandler(merchantServices.VoucherCashIn))
	route.Post("/cashout", wrapHandler(merchantServices.VoucherCashOut))
	route.Post("/purchase", wrapHandler(merchantServices.Purchase))
	route.Post("/cashIn", wrapHandler(merchantServices.CashIn))
	route.Post("/cashOut", wrapHandler(merchantServices.CashOut))
	route.Post("/billInquiry", wrapHandler(merchantServices.BillInquiry))
	route.Post("/billPayment", wrapHandler(merchantServices.BillPayment))
	route.Post("/bills", wrapHandler(merchantServices.TopUpPayment))
	route.Post("/changePin", wrapHandler(merchantServices.ChangePIN))
	route.Post("/miniStatement", wrapHandler(merchantServices.MiniStatement))
	route.Post("/isAlive", wrapHandler(merchantServices.IsAlive))
	route.Post("/balance", wrapHandler(merchantServices.Balance))
	route.Post("/refund", wrapHandler(merchantServices.Refund))
	route.Post("/toAccount", wrapHandler(merchantServices.ToAccount))
	route.Post("/statement", wrapHandler(merchantServices.Statement))
	route.Get("/test", func(c *fiber.Ctx) error {
		return c.Status(http.StatusOK).JSON(fiber.Map{"message": true})
	})

	route.Get("/wrk", wrapHandler(merchantServices.IsAliveWrk))
	route.Get("/metrics", adaptor.HTTPHandler(promhttp.Handler()))

	dashboardGroup := route.Group("/dashboard")
	{
		dashboardGroup.Get("/", wrapHandler(dashService.BrowserDashboard))
		dashboardGroup.Get("/get_tid", wrapHandler(dashService.TransactionByTid))
		dashboardGroup.Get("/get", wrapHandler(dashService.TransactionByTid))
		dashboardGroup.Get("/create", wrapHandler(dashService.MakeDummyTransaction))
		dashboardGroup.Get("/all", wrapHandler(dashService.GetAll))
		dashboardGroup.Get("/all/:id", wrapHandler(dashService.GetID))
		dashboardGroup.Get("/count", wrapHandler(dashService.TransactionsCount))
		dashboardGroup.Get("/settlement", wrapHandler(dashService.DailySettlement))
		dashboardGroup.Get("/merchant", wrapHandler(dashService.MerchantTransactionsEndpoint))
		dashboardGroup.Get("/merchant/:id", wrapHandler(dashService.MerchantViews))
		dashboardGroup.Post("/issues", wrapHandler(dashService.ReportIssueEndpoint))
		dashboardGroup.Get("/status", wrapHandler(dashService.QRStatus))
		dashboardGroup.Get("/test_browser", wrapHandler(dashService.IndexPage))
		dashboardGroup.Get("/stream", wrapHandler(dashService.Stream))
	}

	cons := route.Group("/consumer")

	{
		cons.Post("/register", wrapHandler(consumerService.CreateUser))
		cons.Post("/register_with_card", wrapHandler(consumerService.RegisterWithCard))
		cons.Post("/refresh", wrapHandler(consumerService.RefreshHandler))
		cons.Post("/balance", wrapHandler(consumerService.Balance))
		cons.Post("/status", wrapHandler(consumerService.TransactionStatus))
		cons.Post("/is_alive", wrapHandler(consumerService.IsAlive))
		cons.Post("/bill_payment", wrapHandler(consumerService.BillPayment))
		cons.Post("/bills", wrapHandler(consumerService.GetBills))
		cons.Get("/guess_biller", wrapHandler(consumerService.GetBiller))
		cons.Post("/bill_inquiry", wrapHandler(consumerService.BillInquiry))
		cons.Post("/p2p", wrapHandler(consumerService.CardTransfer))
		cons.Post("/cashIn", wrapHandler(consumerService.CashIn))
		cons.Post("/cashOut", wrapHandler(consumerService.CashOut))
		cons.Post("/account", wrapHandler(consumerService.AccountTransfer))
		cons.Post("/purchase", wrapHandler(consumerService.Purchase))
		cons.Post("/n/status", wrapHandler(consumerService.Status))
		cons.Post("/key", wrapHandler(consumerService.WorkingKey))
		cons.Post("/ipin", wrapHandler(consumerService.IPinChange))
		cons.Post("/generate_qr", wrapHandler(consumerService.QRMerchantRegistration))
		cons.Post("/qr_payment", wrapHandler(consumerService.QRPayment))
		cons.Post("/qr_status", wrapHandler(consumerService.QRTransactions))
		cons.Post("/ipin_key", wrapHandler(consumerService.IPINKey))
		cons.Post("/generate_ipin", wrapHandler(consumerService.GenerateIpin))
		cons.Post("/complete_ipin", wrapHandler(consumerService.CompleteIpin))
		cons.Post("/qr_refund", wrapHandler(consumerService.QRRefund))
		cons.Post("/qr_complete", wrapHandler(consumerService.QRComplete))
		cons.Post("/card_info", wrapHandler(consumerService.EbsGetCardInfo))
		cons.Post("/pan_from_mobile", wrapHandler(consumerService.GetMSISDNFromCard))
		cons.Get("/mobile2pan", wrapHandler(consumerService.CardFromNumber))
		cons.Get("/nec2name", wrapHandler(consumerService.NecToName))
		cons.Post("/vouchers/generate", wrapHandler(consumerService.GenerateVoucher))
		cons.Post("/cards/new", wrapHandler(consumerService.RegisterCard))
		cons.Post("/cards/complete", wrapHandler(consumerService.CompleteRegistration))
		cons.Post("/login", wrapHandler(consumerService.LoginHandler))
		cons.Post("/kyc", wrapHandler(consumerService.KYC))
		cons.Get("/transaction", func(ctx *fiber.Ctx) error {
			var res ebs_fields.EBSResponse
			id := ctx.Query("uuid")
			if response, err := res.GetByUUID(id, database); err != nil {
				response, err = res.GetEBSUUID(id, database, &noebsConfig)
				if err != nil {
					return ctx.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "not_found", "message": err.Error()})
				}
			} else {
				return ctx.Status(http.StatusOK).JSON(response)
			}
			return nil
		})
		cons.Get("/users/cards", func(ctx *fiber.Ctx) error {
			mobile := ctx.Query("mobile")
			if response, err := ebs_fields.GetCardsOrFail(mobile, database); err != nil {
				return ctx.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "not_found", "message": err.Error()})
			} else {
				return ctx.Status(http.StatusOK).JSON(response)
			}
		})
		cons.Post("/otp/generate", func(c *fiber.Ctx) error {
			return consumerService.GenerateSignInCode(c, false)
		})
		cons.Post("/otp/generate_insecure", func(c *fiber.Ctx) error {
			return consumerService.GenerateSignInCode(c, true)
		})
		cons.Post("/otp/login", wrapHandler(consumerService.SingleLoginHandler))
		cons.Post("/otp/verify", wrapHandler(consumerService.VerifyOTP))
		cons.Post("/otp/balance", wrapHandler(consumerService.BalanceStep))
		cons.Post("/verify_firebase", wrapHandler(consumerService.VerifyFirebase))
		cons.Post("/test", func(c *fiber.Ctx) error {
			return c.Status(http.StatusOK).JSON(fiber.Map{"message": true})
		})
		cons.Post("/check_user", wrapHandler(consumerService.CheckUser))

		// New auth routes (email/social)
		cons.Post("/auth/google", wrapHandler(consumerService.GoogleAuth))

		cons.Use(auth.AuthMiddleware())
		cons.Post("/auth/complete_profile", wrapHandler(consumerService.CompleteProfile))
		cons.Get("/auth/me", wrapHandler(consumerService.AuthMe))
		cons.Get("/user", wrapHandler(consumerService.GetUser))
		cons.Put("/user", wrapHandler(consumerService.UpdateUser))
		cons.Get("/user/lang", wrapHandler(consumerService.GetUserLanguage))
		cons.Put("/user/lang", wrapHandler(consumerService.SetUserLanguage))
		cons.Get("/notifications", wrapHandler(consumerService.Notifications))
		cons.Get("/transactions", wrapHandler(consumerService.GetTransactions))
		cons.Post("/p2p_mobile", wrapHandler(consumerService.MobileTransfer))
		cons.Post("/cards/set_main", wrapHandler(consumerService.SetMainCard))
		cons.Post("/user/firebase", wrapHandler(consumerService.AddFirebaseID))
		cons.All("/beneficiary", wrapHandler(consumerService.Beneficiaries))
		cons.Post("/change_password", wrapHandler(consumerService.ChangePassword))
		cons.Get("/get_cards", wrapHandler(consumerService.GetCards))
		cons.Post("/add_card", wrapHandler(consumerService.AddCards))
		cons.Put("/edit_card", wrapHandler(consumerService.EditCard))
		cons.Delete("/delete_card", wrapHandler(consumerService.RemoveCard))
		cons.Get("/payment_token", wrapHandler(consumerService.GetPaymentToken))
		cons.Post("/payment_token", wrapHandler(consumerService.GeneratePaymentToken))
		cons.Post("/payment_request", wrapHandler(consumerService.PaymentRequest))
		cons.Post("/payment_token/quick_pay", wrapHandler(consumerService.NoebsQuickPayment))
		cons.Post("/submit_contacts", func(c *fiber.Ctx) error {
			mobile := c.Locals("mobile")
			var m string
			if mobile != nil {
				if s, ok := mobile.(string); ok {
					m = s
				}
			}
			return adaptor.HTTPHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				chat.SubmitContacts(m, consumerService.NoebsConfig.DatabasePath, w, r)
			})(c)
		})
	}
	return route
}

func init() {
	if isRenderConfigCommand() {
		return
	}
	var err error

	logrusLogger.Level = logrus.DebugLevel
	logrusLogger.SetReportCaller(true)
	logrusLogger.Out = os.Stderr

	// load the secrets file
	configData := loadConfig()
	logrusLogger.Printf("Loaded config (%d bytes)", len(configData))
	if err := json.Unmarshal(configData, &noebsConfig); err != nil {
		logrusLogger.Printf("error in unmarshaling config file: %v", err)
	}

	noebsConfig.Defaults()
	dbpath := "test.db"
	if noebsConfig.DatabasePath != "" {
		dbpath = noebsConfig.DatabasePath
	}

	logrusLogger.Printf("The final database file is: %#v", dbpath)
	// Initialize database
	database, err = utils.Database(dbpath)
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
	// FIXME we should pass on the same database here
	chatDb, err := chat.OpenDb(dbpath)
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
		&ebs_fields.AuthAccount{}, &ebs_fields.Card{}, &ebs_fields.EBSResponse{}, &ebs_fields.Token{},
		&ebs_fields.CacheBillers{}, &ebs_fields.CacheCards{}, &ebs_fields.Beneficiary{}, &ebs_fields.KYC{}, &ebs_fields.Passport{}); err != nil {
		logrusLogger.Fatalf("error in migration: %v", err)
	}
	// check database foreign key for user & credit_cards exists or not
	database.Migrator().HasConstraint(&consumer.PushData{}, "Transactions")
	database.Migrator().HasConstraint(&consumer.PushData{}, "fk_push_data_ebs_data")

	auth = gateway.JWTAuth{NoebsConfig: noebsConfig}

	auth.Init()
	consumerService = consumer.Service{Db: database, Redis: redisClient, NoebsConfig: noebsConfig, Logger: logrusLogger, FirebaseApp: firebaseApp, Auth: &auth}
	dashService = dashboard.Service{Redis: redisClient, Db: database}
	merchantServices = merchant.Service{Db: database, Redis: redisClient, Logger: logrusLogger, NoebsConfig: noebsConfig}
	dataConfigs.DB = database

}
