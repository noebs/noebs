package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/dashboard"
	"github.com/adonese/noebs/merchant"
	"github.com/adonese/noebs/store"
	"github.com/bradfitz/iter"
	"github.com/gofiber/adaptor/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/template/html/v2"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"

	chat "github.com/tutipay/ws"
	"gopkg.in/yaml.v3"
)

func isTestRun() bool {
	return strings.HasSuffix(os.Args[0], ".test")
}

func loadConfig() ([]byte, error) {
	configPath := firstExistingPath(defaultConfigPath, "./config.yaml", "../config.yaml")
	if configPath == "" {
		if isTestRun() {
			return []byte("{}"), nil
		}
		return nil, errors.New("config.yaml not found")
	}

	configData, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	configMap := map[string]interface{}{}
	if err := yaml.Unmarshal(configData, &configMap); err != nil {
		return nil, fmt.Errorf("parse config yaml: %w", err)
	}

	secretsMap := map[string]interface{}{}
	secretsPath := firstExistingPath(defaultSecretsPath, "./secrets.yaml", "../secrets.yaml")
	if secretsPath != "" {
		decrypted, err := decryptSopsFile(secretsPath)
		if err != nil {
			if isTestRun() {
				logrusLogger.Printf("Skipping secrets (%s): %v", secretsPath, err)
			} else {
				return nil, err
			}
		} else if err := yaml.Unmarshal(decrypted, &secretsMap); err != nil {
			return nil, fmt.Errorf("parse secrets yaml: %w", err)
		} else {
			logrusLogger.Printf("Loaded secrets from %s", secretsPath)
		}
	}

	merged, ok := mergeConfig(configMap, secretsMap).(map[string]interface{})
	if !ok {
		return nil, errors.New("merged config is not a map")
	}
	noebs := getMap(merged, "noebs")
	if noebs == nil {
		noebs = map[string]interface{}{}
	}

	payload, err := json.Marshal(noebs)
	if err != nil {
		return nil, fmt.Errorf("encode noebs config: %w", err)
	}

	logrusLogger.Printf("Loaded config from %s", configPath)
	return payload, nil
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
		if hub == nil {
			http.Error(w, "chat disabled", http.StatusServiceUnavailable)
			return
		}
		chat.ServeWs(hub, w, r)
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
			id := ctx.Query("uuid")
			tenantID := ctx.Get("X-Tenant-ID")
			if tenantID == "" {
				tenantID = noebsConfig.DefaultTenantID
			}
			if tenantID == "" {
				tenantID = store.DefaultTenantID
			}
			response, err := storeSvc.GetTransactionByUUID(ctx.UserContext(), tenantID, id)
			if err != nil {
				return ctx.Status(http.StatusBadRequest).JSON(fiber.Map{"code": "not_found", "message": err.Error()})
			}
			return ctx.Status(http.StatusOK).JSON(response)
		})
		cons.Get("/users/cards", func(ctx *fiber.Ctx) error {
			mobile := ctx.Query("mobile")
			tenantID := ctx.Get("X-Tenant-ID")
			if tenantID == "" {
				tenantID = noebsConfig.DefaultTenantID
			}
			if tenantID == "" {
				tenantID = store.DefaultTenantID
			}
			if response, err := storeSvc.GetCardsOrFail(ctx.UserContext(), tenantID, mobile); err != nil {
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
		cons.Post("/user/firebase", wrapHandler(consumerService.AddDeviceToken))
		cons.Post("/user/device", wrapHandler(consumerService.AddDeviceToken))
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
			if hub == nil {
				return c.Status(http.StatusServiceUnavailable).JSON(fiber.Map{"code": "chat_disabled", "message": "chat disabled"})
			}
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
	configData, err := loadConfig()
	if err != nil {
		logrusLogger.Fatalf("error loading config: %v", err)
	}
	logrusLogger.Printf("Loaded config (%d bytes)", len(configData))
	if err := json.Unmarshal(configData, &noebsConfig); err != nil {
		logrusLogger.Fatalf("error in unmarshaling config file: %v", err)
	}

	noebsConfig.Defaults()
	tenantID := noebsConfig.DefaultTenantID
	if tenantID == "" {
		tenantID = store.DefaultTenantID
	}
	dbpath := "test.db"
	if noebsConfig.DatabasePath != "" {
		dbpath = noebsConfig.DatabasePath
	}
	if isTestRun() {
		if tmp, err := os.CreateTemp("", "noebs-test-*.db"); err == nil {
			dbpath = tmp.Name()
			_ = tmp.Close()
		}
	}

	logrusLogger.Printf("The final database file is: %#v", dbpath)
	database, err = store.OpenFromConfig(noebsConfig.DatabaseURL, dbpath, noebsConfig.DatabaseDriver)
	if err != nil {
		logrusLogger.Fatalf("error in connecting to db: %v", err)
	}
	storeSvc = store.New(database)
	migrateCtx, cancelMigrate := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelMigrate()
	if err := store.Migrate(migrateCtx, database, tenantID); err != nil {
		logrusLogger.Fatalf("error in migrations: %v", err)
	}
	if err := storeSvc.EnsureTenant(migrateCtx, tenantID); err != nil {
		logrusLogger.Fatalf("error ensuring tenant: %v", err)
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
		logrusLogger.Printf("chat db unavailable: %v", err)
	} else {
		hub = chat.NewHub(chatDb)
	}

	auth = gateway.JWTAuth{NoebsConfig: noebsConfig}

	auth.Init()
	consumerService = consumer.Service{Store: storeSvc, NoebsConfig: noebsConfig, Logger: logrusLogger, Auth: &auth}
	dashService = dashboard.Service{Store: storeSvc, NoebsConfig: noebsConfig}
	merchantServices = merchant.Service{Store: storeSvc, Logger: logrusLogger, NoebsConfig: noebsConfig}
	dataConfigs.DB = database

}
