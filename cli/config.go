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
	"sync"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	consumerhandler "github.com/adonese/noebs/consumer/handler"
	"github.com/adonese/noebs/dashboard"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/merchant"
	merchanthandler "github.com/adonese/noebs/merchant/handler"
	"github.com/adonese/noebs/store"
	"github.com/adonese/noebs/wallet"
	walletactivity "github.com/adonese/noebs/wallet/activity"
	wallethandler "github.com/adonese/noebs/wallet/handler"
	walletpsp "github.com/adonese/noebs/wallet/psp"
	walletpsphttpjson "github.com/adonese/noebs/wallet/psp/httpjson"
	walletpspnoop "github.com/adonese/noebs/wallet/psp/noop"
	walletworker "github.com/adonese/noebs/wallet/worker"
	walletworkflow "github.com/adonese/noebs/wallet/workflow"
	"github.com/gofiber/adaptor/v2"
	"github.com/gofiber/contrib/otelfiber"
	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	temporalworker "go.temporal.io/sdk/worker"

	chat "github.com/tutipay/ws"
	"gopkg.in/yaml.v3"
)

func isTestRun() bool {
	return strings.HasSuffix(os.Args[0], ".test")
}

func loadConfig() ([]byte, error) {
	configPath := firstExistingPath(defaultConfigPath, "./config.yaml", "../config.yaml")
	if isTestRun() {
		configPath = firstExistingPath("./config.test.yaml", "../config.test.yaml", "./config.yaml", "../config.yaml", defaultConfigPath)
	}
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
	if serviceConfigPath := firstExistingPath(defaultServiceConfigPath, "./service.yaml", "../service.yaml"); serviceConfigPath != "" {
		serviceConfigData, err := os.ReadFile(serviceConfigPath)
		if err != nil {
			return nil, fmt.Errorf("read service config: %w", err)
		}
		serviceConfigMap := map[string]interface{}{}
		if err := yaml.Unmarshal(serviceConfigData, &serviceConfigMap); err != nil {
			return nil, fmt.Errorf("parse service config yaml: %w", err)
		}
		configMap = mergeConfig(configMap, serviceConfigMap).(map[string]interface{})
	}

	secretsMap := map[string]interface{}{}
	secretsPath := firstExistingPath(defaultSecretsPath, "./secrets.yaml", "../secrets.yaml")
	if secretsPath != "" {
		decrypted, err := decryptSopsFile(secretsPath, firstString(getMap(configMap, "noebs"), "sops_age_key_file"))
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
	rawSecrets = secretsMap

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

func buildPSPDeps(service *wallet.Service, secrets map[string]interface{}) (*walletpsp.Registry, *walletpsp.Loader) {
	if service == nil || service.Store == nil {
		return nil, nil
	}
	pspRegistry := walletpsp.NewRegistry()
	if err := pspRegistry.Register("noop", func(cfg *walletpsp.Config) (walletpsp.Provider, error) {
		_ = cfg
		return &walletpspnoop.Provider{}, nil
	}); err != nil {
		logrusLogger.Printf("error registering noop PSP provider: %v", err)
	}
	if err := pspRegistry.Register("httpjson", func(cfg *walletpsp.Config) (walletpsp.Provider, error) {
		return walletpsphttpjson.NewProvider(cfg)
	}); err != nil {
		logrusLogger.Printf("error registering httpjson PSP provider: %v", err)
	}

	var secretResolver walletpsp.SecretResolver = walletpsp.SecretResolverFunc(func(ctx context.Context, tenantID, providerCode string) (walletpsp.SecretBundle, error) {
		return walletpsp.SecretBundle{}, walletpsp.ErrPSPSecretMissing
	})
	if mapResolver := walletpsp.NewMapSecretResolver(secrets); mapResolver != nil {
		secretResolver = mapResolver
	}
	pspLoader := &walletpsp.Loader{
		Store:   service.Store,
		Secrets: secretResolver,
	}
	return pspRegistry, pspLoader
}

func startCronWorkflow(ctx context.Context, temporalClient client.Client, workflowID, cron string, taskQueue string, workflowFn interface{}, args ...interface{}) {
	if temporalClient == nil || workflowID == "" || cron == "" || taskQueue == "" {
		return
	}
	_, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:           workflowID,
		TaskQueue:    taskQueue,
		CronSchedule: cron,
	}, workflowFn, args...)
	if err == nil {
		logrusLogger.Printf("started cron workflow %s (%s)", workflowID, cron)
		return
	}
	if _, ok := err.(*serviceerror.WorkflowExecutionAlreadyStarted); ok {
		return
	}
	logrusLogger.Printf("error starting cron workflow %s: %v", workflowID, err)
}

func startWalletCronWorkflows(ctx context.Context, temporalClient client.Client, tenants []string, cfg ebs_fields.NoebsConfig) {
	if temporalClient == nil {
		return
	}
	taskQueue := string(walletworker.TaskQueueMain)
	for _, tenantID := range tenants {
		if tenantID == "" {
			continue
		}
		pollerID := fmt.Sprintf("wallet-psp-poller-%s", tenantID)
		pollerParams := walletworkflow.PSPStatusPollerParams{
			TenantID:            tenantID,
			Limit:               cfg.WalletPSPPollerBatchSize,
			PollIntervalSeconds: cfg.WalletPSPPollerIntervalSeconds,
		}
		startCronWorkflow(ctx, temporalClient, pollerID, cfg.WalletPSPPollerCron, taskQueue, walletworkflow.PSPStatusPoller, pollerParams)

		reconID := fmt.Sprintf("wallet-reconciliation-%s", tenantID)
		reconParams := walletworkflow.ReconciliationParams{
			TenantID:      tenantID,
			Status:        "success",
			Limit:         cfg.WalletReconciliationBatchSize,
			LookbackHours: cfg.WalletReconciliationLookbackHours,
		}
		startCronWorkflow(ctx, temporalClient, reconID, cfg.WalletReconciliationCron, taskQueue, walletworkflow.Reconciliation, reconParams)
	}
}

var pspTemporalClient client.Client

func closePSPTemporalClient() {
	if pspTemporalClient != nil {
		pspTemporalClient.Close()
	}
}

func registerAdminReportingRoutes(route *fiber.App, adminGuard fiber.Handler, templateDir string) {
	route.Static("/dashboard/assets", templateDir)
	dashboardGroup := route.Group("/dashboard", adminGuard)
	{
		dashboardGroup.Get("/", wrapHandler(dashService.BrowserDashboard))
		dashboardGroup.Get("/get_tid", wrapHandler(dashService.TransactionByTid))
		dashboardGroup.Get("/get", wrapHandler(dashService.TransactionByTid))
		dashboardGroup.Get("/all", wrapHandler(dashService.GetAll))
		dashboardGroup.Get("/all/:id", wrapHandler(dashService.GetID))
		dashboardGroup.Get("/count", wrapHandler(dashService.TransactionsCount))
		dashboardGroup.Get("/settlement", wrapHandler(dashService.DailySettlement))
		dashboardGroup.Get("/merchant", wrapHandler(dashService.MerchantTransactionsEndpoint))
		dashboardGroup.Get("/merchant/:id", wrapHandler(dashService.MerchantViews))
		dashboardGroup.Get("/status", wrapHandler(dashService.QRStatus))
		dashboardGroup.Get("/test_browser", wrapHandler(dashService.IndexPage))
		dashboardGroup.Get("/stream", wrapHandler(dashService.Stream))
	}
}

func registerNotificationChatRoutes(route *fiber.App, auth gateway.JWTAuth, consumerHandler *consumerhandler.Handler) {
	route.Get("/ws", adaptor.HTTPHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chat.ServeWs(hub, w, r)
	}))

	cons := route.Group("/consumer", auth.AuthMiddleware())
	consumerhandler.RegisterNotificationRoutes(cons, consumerHandler)
	cons.Post("/submit_contacts", func(c *fiber.Ctx) error {
		mobile, ok := c.Locals("mobile").(string)
		if !ok || strings.TrimSpace(mobile) == "" {
			return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"code": "missing_mobile", "message": "missing mobile claim"})
		}
		return adaptor.HTTPHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			chat.SubmitContacts(mobile, database.DB, w, r)
		})(c)
	})
}

func registerIdentityAuthRoutes(route *fiber.App, auth gateway.JWTAuth, adminGuard fiber.Handler, consumerHandler *consumerhandler.Handler) {
	route.Post("/generate_api_key", adminGuard, consumerHandler.GenerateAPIKey)

	cons := route.Group("/consumer")
	consumerhandler.RegisterIdentityPublicRoutes(cons, consumerHandler)
	consumerhandler.RegisterIdentityAuthedRoutes(cons.Group("", auth.AuthMiddleware()), consumerHandler)
}

func registerCardVaultRoutes(route *fiber.App, auth gateway.JWTAuth, consumerHandler *consumerhandler.Handler) {
	cons := route.Group("/consumer")
	consumerhandler.RegisterCardVaultPublicRoutes(cons, consumerHandler)
	consumerhandler.RegisterCardVaultAuthedRoutes(cons.Group("", auth.AuthMiddleware()), consumerHandler)
}

// GetMainEngine function responsible for getting all of our routes to be delivered for fiber
func GetMainEngine() *fiber.App {
	ensureInit()
	role, err := currentServiceRole()
	if err != nil {
		logrusLogger.Fatalf("error in runtime service role: %v", err)
	}
	templateDir := resolveDashboardTemplateDir()
	route := fiber.New(fiber.Config{})
	route.Use(gateway.RequestID())
	if otelEnabled {
		route.Use(otelfiber.Middleware(
			otelfiber.WithServerName(noebsConfig.OtelServiceName),
			otelfiber.WithSpanNameFormatter(func(ctx *fiber.Ctx) string {
				if r := ctx.Route(); r != nil && r.Path != "" {
					return ctx.Method() + " " + r.Path
				}
				return ctx.Method() + " " + ctx.Path()
			}),
		))
	}
	route.Use(gateway.Instrumentation())
	route.Use(gateway.RequestLogger(logrusLogger, logSampling))
	route.Use(gateway.NoebsCors(noebsConfig.Cors))

	adminGuard := gateway.RequireAdmin(gateway.AdminAuthConfig{
		Key:      noebsConfig.AdminKey,
		User:     noebsConfig.AdminUser,
		Password: noebsConfig.AdminPassword,
		Debug:    noebsConfig.IsDebug,
	})

	consumerHandler := consumerhandler.New(&consumerService)
	merchantHandler := merchanthandler.New(&merchantServices)

	route.Get("/test", func(c *fiber.Ctx) error {
		return c.Status(http.StatusOK).JSON(fiber.Map{"message": true})
	})
	route.Get("/metrics", adminGuard, adaptor.HTTPHandler(promhttp.Handler()))

	if role == serviceRolePSPWebhook {
		walletWebhookHandler := wallethandler.NewPSPWebhookHandler(walletService, walletPSPLoader, walletPSPRegistry, pspTemporalClient)
		wallethandler.RegisterWebhookRoutes(route, walletWebhookHandler)
		return route
	}
	if role == serviceRoleIdentityAuth {
		registerIdentityAuthRoutes(route, auth, adminGuard, consumerHandler)
		return route
	}
	if role == serviceRoleCardVault {
		registerCardVaultRoutes(route, auth, consumerHandler)
		return route
	}
	if role == serviceRoleAdminReporting {
		registerAdminReportingRoutes(route, adminGuard, templateDir)
		return route
	}
	if role == serviceRoleNotification {
		registerNotificationChatRoutes(route, auth, consumerHandler)
		return route
	}
	if role != serviceRoleAPIGateway {
		logrusLogger.Fatalf("service role %s does not own HTTP routes", role)
	}

	merchanthandler.RegisterRoutes(route, merchantHandler)

	route.Get("/app/config", appConfigHandler)

	cons := route.Group("/consumer")

	{
		consumerhandler.RegisterPublicRoutes(cons, consumerHandler)
		cons.Post("/test", func(c *fiber.Ctx) error {
			return c.Status(http.StatusOK).JSON(fiber.Map{"message": true})
		})

		authedCons := cons.Group("", auth.AuthMiddleware())
		consumerhandler.RegisterAuthedRoutes(authedCons, consumerHandler)
	}

	if walletService != nil {
		var temporalClient wallethandler.TemporalClient
		if walletWorker != nil {
			temporalClient = walletWorker.Client
		}
		walletUserHandler := wallethandler.NewUserHandler(walletService)
		walletAdminHandler := wallethandler.NewAdminHandler(walletService, temporalClient)
		userWalletGroup := route.Group("/wallet", auth.AuthMiddleware())
		adminWalletGroup := route.Group("/admin/wallet", adminGuard)
		wallethandler.RegisterUserRoutes(userWalletGroup, walletUserHandler)
		wallethandler.RegisterAdminRoutes(adminWalletGroup, walletAdminHandler)
	}
	if grpcGatewayHandler != nil {
		route.All("/wallet", adaptor.HTTPHandler(grpcGatewayHandler))
		route.All("/wallet/*", adaptor.HTTPHandler(grpcGatewayHandler))
	}
	return route
}

var initOnce sync.Once

func ensureInit() {
	initOnce.Do(initConfig)
}

func init() {
	if isRenderConfigCommand() {
		return
	}
	if isTestRun() {
		return
	}
	ensureInit()
}

func initConfig() {
	var err error

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
	role, err := currentServiceRole()
	if err != nil {
		logrusLogger.Fatalf("error in runtime service role: %v", err)
	}
	tenantID, err := configuredTenantID(noebsConfig)
	if err != nil {
		logrusLogger.Fatalf("error in runtime config: %v", err)
	}
	ebs_fields.ConfigureEBSHTTPClient(noebsConfig)
	configureLogger(noebsConfig)
	initOTel(context.Background(), noebsConfig, logrusLogger)
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
	storeSvc = store.New(database, store.WithDataKey(noebsConfig.DataKey))
	migrateCtx, cancelMigrate := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelMigrate()
	if role.runsMigrations() {
		if err := store.Migrate(migrateCtx, database, tenantID); err != nil {
			logrusLogger.Fatalf("error in migrations: %v", err)
		}
		if err := storeSvc.EnsureTenant(migrateCtx, tenantID); err != nil {
			logrusLogger.Fatalf("error ensuring tenant: %v", err)
		}
		if err := ensureNoReservedTenant(migrateCtx, storeSvc); err != nil {
			logrusLogger.Fatalf("error validating tenants: %v", err)
		}
	} else {
		logrusLogger.Printf("Migrations are owned by service role %s; current role is %s", serviceRoleMigrate, role)
	}

	logrusLogger.Printf(
		"Runtime config loaded: role=%s driver=%s tenant=%s port=%s temporal=%t grpc=%t wallet=%t",
		role,
		noebsConfig.DatabaseDriver,
		noebsConfig.DefaultTenantID,
		noebsConfig.Port,
		noebsConfig.TemporalEnabled,
		noebsConfig.GRPCEnabled,
		noebsConfig.WalletEnabled,
	)
	if role == serviceRoleMigrate {
		dataConfigs.DB = database
		return
	}

	// Initialize sentry
	// sentry.Init(sentry.ClientOptions{
	// 	Dsn: noebsConfig.Sentry,
	// 	// Set TracesSampleRate to 1.0 to capture 100%
	// 	// of transactions for performance monitoring.
	// 	// We recommend adjusting this value in production,
	// 	TracesSampleRate: 1.0,
	// })
	auth = gateway.JWTAuth{NoebsConfig: noebsConfig}
	auth.Init()
	if role.startsChat() && database != nil && database.DB != nil {
		chatCfg := chat.DefaultHubConfig()
		chatCfg.MaxUnreadMessages = 1000
		chatCfg.UnreadBatchSize = 200
		chatCfg.PersistBatchSize = 128
		chatCfg.PersistFlushInterval = 10 * time.Millisecond
		chatCfg.ClientIDFromRequest = func(r *http.Request) (string, error) {
			token := strings.TrimSpace(r.Header.Get("Authorization"))
			if strings.HasPrefix(strings.ToLower(token), "bearer ") {
				token = strings.TrimSpace(token[7:])
			}
			if token == "" {
				return "", chat.ErrUnauthorized
			}
			claims, err := auth.VerifyJWT(token)
			if err != nil {
				return "", chat.ErrUnauthorized
			}
			if claims.Mobile == "" {
				return "", chat.ErrUnauthorized
			}
			return claims.Mobile, nil
		}
		hub = chat.NewHubWithConfig(database.DB, chatCfg)
	}
	consumerService = consumer.Service{Store: storeSvc, NoebsConfig: noebsConfig, Logger: logrusLogger, Auth: &auth}
	dashService = dashboard.Service{Store: storeSvc, NoebsConfig: noebsConfig}
	merchantServices = merchant.Service{Store: storeSvc, Logger: logrusLogger, NoebsConfig: noebsConfig}
	walletService = wallet.NewService(database, noebsConfig)
	walletPSPRegistry, walletPSPLoader = buildPSPDeps(walletService, rawSecrets)
	if role == serviceRolePSPWebhook && !noebsConfig.WalletEnabled {
		logrusLogger.Fatalf("psp-webhook role requires wallet_enabled")
	}
	if role == serviceRolePSPWebhook && !noebsConfig.TemporalEnabled {
		logrusLogger.Fatalf("psp-webhook role requires temporal_enabled")
	}
	if role == serviceRolePSPWebhook {
		pspTemporalClient, err = walletworker.NewClient(walletworker.Options{
			Host:      noebsConfig.TemporalHost,
			Port:      noebsConfig.TemporalPort,
			Namespace: noebsConfig.TemporalNamespace,
			TaskQueue: walletworker.TaskQueueMain,
		})
		if err != nil {
			logrusLogger.Fatalf("error creating psp temporal client: %v", err)
		}
	}
	if role == serviceRoleWalletWorker && !noebsConfig.TemporalEnabled {
		logrusLogger.Fatalf("wallet-worker role requires temporal_enabled")
	}
	if role.startsWalletWorker() && noebsConfig.TemporalEnabled {
		if walletPSPRegistry == nil || walletPSPLoader == nil {
			logrusLogger.Printf("wallet PSP dependencies not initialized; PSP activities disabled")
		}
		pspActivities := walletactivity.NewPSPActivities(walletPSPLoader, walletPSPRegistry)
		workerOpts := walletworker.Options{
			Host:      noebsConfig.TemporalHost,
			Port:      noebsConfig.TemporalPort,
			Namespace: noebsConfig.TemporalNamespace,
			TaskQueue: walletworker.TaskQueueMain,
		}
		register := func(w temporalworker.Worker) {
			walletworker.RegisterWallet(w, walletworker.RegisterDeps{
				Store:         walletService.Store,
				PSPActivities: pspActivities,
				UserStore:     storeSvc,
			})
		}
		runner, err := walletworker.NewRunner(context.Background(), workerOpts, register)
		if err != nil {
			logrusLogger.Fatalf("error creating wallet worker: %v", err)
		}
		if err := runner.Start(); err != nil {
			logrusLogger.Fatalf("error starting wallet worker: %v", err)
		}
		walletWorker = runner
		var tenants []string
		if storeSvc != nil {
			if list, err := storeSvc.ListTenants(context.Background()); err == nil {
				tenants = list
			} else {
				logrusLogger.Printf("error listing tenants for wallet schedules: %v", err)
			}
		}
		if len(tenants) == 0 && noebsConfig.DefaultTenantID != "" {
			tenants = []string{noebsConfig.DefaultTenantID}
		}
		startWalletCronWorkflows(context.Background(), runner.Client, tenants, noebsConfig)
	}
	if role == serviceRoleWalletLedger && !noebsConfig.GRPCEnabled {
		logrusLogger.Fatalf("wallet-ledger role requires grpc_enabled")
	}
	if role.startsGRPC() {
		if err := initGRPCServers(); err != nil {
			logrusLogger.Fatalf("error initializing grpc servers: %v", err)
		}
	}
	dataConfigs.DB = database

}
