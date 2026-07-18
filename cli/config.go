package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adonese/noebs/adminreporting"
	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	consumerhandler "github.com/adonese/noebs/consumer/handler"
	"github.com/adonese/noebs/dashboard"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/eventing"
	"github.com/adonese/noebs/internal/httpclient"
	"github.com/adonese/noebs/merchant"
	"github.com/adonese/noebs/store"
	"github.com/adonese/noebs/wallet"
	walletactivity "github.com/adonese/noebs/wallet/activity"
	wallethandler "github.com/adonese/noebs/wallet/handler"
	walletpsp "github.com/adonese/noebs/wallet/psp"
	walletpsphttpjson "github.com/adonese/noebs/wallet/psp/httpjson"
	walletpspnoop "github.com/adonese/noebs/wallet/psp/noop"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletworker "github.com/adonese/noebs/wallet/worker"
	walletworkflow "github.com/adonese/noebs/wallet/workflow"
	"github.com/gofiber/adaptor/v2"
	"github.com/gofiber/contrib/otelfiber"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	temporalworker "go.temporal.io/sdk/worker"

	chat "github.com/tutipay/ws"
	"gopkg.in/yaml.v3"
)

type chatGatewayIdentityContextKey struct{}

type chatGatewayIdentity struct {
	gateway.UserIdentity
	Token string
}

const chatSessionValidationInterval = 5 * time.Second

func isTestRun() bool {
	return strings.HasSuffix(os.Args[0], ".test")
}

func loadConfig() ([]byte, error) {
	configPath := defaultConfigPath
	if isTestRun() {
		configPath = "./config.test.yaml"
	}
	if _, err := requiredExistingPath("config", configPath); err != nil {
		return nil, err
	}

	configData, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	configMap := map[string]interface{}{}
	if err := yaml.Unmarshal(configData, &configMap); err != nil {
		return nil, fmt.Errorf("parse config yaml: %w", err)
	}
	serviceConfigPath := defaultServiceConfigPath
	if isTestRun() {
		serviceConfigPath = "./service.yaml"
	}
	if !isTestRun() {
		if _, err := requiredExistingPath("service config", serviceConfigPath); err != nil {
			return nil, err
		}
	}
	if serviceConfigPath, err := optionalExistingPath(serviceConfigPath); err != nil {
		return nil, err
	} else if serviceConfigPath != "" {
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
	secretsPath := defaultSecretsPath
	if isTestRun() {
		secretsPath = "./secrets.yaml"
	}
	if !isTestRun() {
		if _, err := requiredExistingPath("secrets", secretsPath); err != nil {
			return nil, err
		}
	}
	if secretsPath, err := optionalExistingPath(secretsPath); err != nil {
		return nil, err
	} else if secretsPath != "" {
		decrypted, err := decryptSopsFile(secretsPath, firstString(getMap(configMap, "noebs"), "sops_age_key_file"))
		if err != nil {
			return nil, err
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
	if err := applyServiceDatabaseURL(noebs); err != nil {
		return nil, err
	}
	if err := rejectLegacyDatabasePath(noebs); err != nil {
		return nil, err
	}

	payload, err := json.Marshal(noebs)
	if err != nil {
		return nil, fmt.Errorf("encode noebs config: %w", err)
	}

	logrusLogger.Printf("Loaded config from %s", configPath)
	return payload, nil
}

func buildPSPDeps(pspStore *walletstore.Store, secrets map[string]interface{}) (*walletpsp.Registry, *walletpsp.Loader, error) {
	if pspStore == nil {
		return nil, nil, errMissingPSPStore
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
		Store:   pspStore,
		Secrets: secretResolver,
	}
	return pspRegistry, pspLoader, nil
}

var (
	errMissingWalletWorkflowClient = errors.New("missing wallet workflow client")
	errMissingWalletWorkflowID     = errors.New("missing wallet workflow id")
	errMissingWalletWorkflowCron   = errors.New("missing wallet workflow cron")
	errMissingWalletWorkflow       = errors.New("missing wallet workflow")
	errMissingWalletTenants        = errors.New("missing wallet tenants")
	errMissingWalletPSPDeps        = errors.New("missing wallet PSP dependencies")
	errMissingPSPStore             = errors.New("missing PSP store")
)

func startCronWorkflow(ctx context.Context, temporalClient client.Client, workflowID, cron string, taskQueue walletworker.TaskQueue, workflowFn interface{}, args ...interface{}) error {
	workflowID = strings.TrimSpace(workflowID)
	cron = strings.TrimSpace(cron)
	if workflowID == "" {
		return errMissingWalletWorkflowID
	}
	if cron == "" {
		return errMissingWalletWorkflowCron
	}
	if taskQueue == "" {
		return walletworker.ErrMissingTaskQueue
	}
	if workflowFn == nil {
		return errMissingWalletWorkflow
	}
	if temporalClient == nil {
		return errMissingWalletWorkflowClient
	}
	_, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:           workflowID,
		TaskQueue:    string(taskQueue),
		CronSchedule: cron,
	}, workflowFn, args...)
	if err == nil {
		logrusLogger.Printf("started cron workflow %s (%s)", workflowID, cron)
		return nil
	}
	if _, ok := err.(*serviceerror.WorkflowExecutionAlreadyStarted); ok {
		return nil
	}
	return err
}

func startWalletCronWorkflows(ctx context.Context, temporalClient client.Client, tenants []string, cfg ebs_fields.NoebsConfig, taskQueue walletworker.TaskQueue) error {
	if len(tenants) == 0 {
		return errMissingWalletTenants
	}
	if taskQueue == "" {
		return walletworker.ErrMissingTaskQueue
	}
	if strings.TrimSpace(cfg.WalletPSPPollerCron) == "" {
		return fmt.Errorf("%w: wallet_psp_poller_cron", errMissingWalletWorkflowCron)
	}
	if strings.TrimSpace(cfg.WalletReconciliationCron) == "" {
		return fmt.Errorf("%w: wallet_reconciliation_cron", errMissingWalletWorkflowCron)
	}
	normalizedTenants := make([]string, 0, len(tenants))
	for _, tenantID := range tenants {
		tenantID, err := store.ValidateTenantID(tenantID)
		if err != nil {
			return err
		}
		normalizedTenants = append(normalizedTenants, tenantID)
	}
	if temporalClient == nil {
		return errMissingWalletWorkflowClient
	}
	for _, tenantID := range normalizedTenants {
		pollerID := fmt.Sprintf("wallet-psp-poller-%s", tenantID)
		pollerParams := walletworkflow.PSPStatusPollerParams{
			TenantID:            tenantID,
			Limit:               cfg.WalletPSPPollerBatchSize,
			PollIntervalSeconds: cfg.WalletPSPPollerIntervalSeconds,
		}
		if err := startCronWorkflow(ctx, temporalClient, pollerID, cfg.WalletPSPPollerCron, taskQueue, walletworkflow.PSPStatusPoller, pollerParams); err != nil {
			return fmt.Errorf("start %s: %w", pollerID, err)
		}

		reconID := fmt.Sprintf("wallet-reconciliation-%s", tenantID)
		reconParams := walletworkflow.ReconciliationParams{
			TenantID:      tenantID,
			Status:        "success",
			Limit:         cfg.WalletReconciliationBatchSize,
			LookbackHours: cfg.WalletReconciliationLookbackHours,
		}
		if err := startCronWorkflow(ctx, temporalClient, reconID, cfg.WalletReconciliationCron, taskQueue, walletworkflow.Reconciliation, reconParams); err != nil {
			return fmt.Errorf("start %s: %w", reconID, err)
		}
	}
	return nil
}

var walletWorkflowClient wallethandler.TemporalSignaler
var walletWorkflowCloser interface{ Close() }

func closeWalletWorkflowClient() {
	if walletWorkflowCloser != nil {
		walletWorkflowCloser.Close()
	}
}

var errRoleDatabaseNotInitialized = errors.New("role database not initialized")

func initRoleServices(role serviceRole) error {
	consumerService = consumer.Service{}
	adminReportingService = adminreporting.Service{}
	dashService = dashboard.Service{}
	merchantServices = merchant.Service{}
	walletService = nil
	pspWebhookStore = nil
	walletPSPRegistry = nil
	walletPSPLoader = nil

	if roleNeedsConsumerService(role) || roleNeedsAdminReportingService(role) || roleNeedsDashboardService(role) || roleNeedsMerchantService(role) {
		if storeSvc == nil {
			return fmt.Errorf("%w: %s", errRoleDatabaseNotInitialized, role)
		}
	}
	if roleNeedsWalletService(role) {
		if database == nil {
			return fmt.Errorf("%w: %s", errRoleDatabaseNotInitialized, role)
		}
	}
	if roleNeedsPSPWebhookStore(role) {
		if database == nil {
			return fmt.Errorf("%w: %s", errRoleDatabaseNotInitialized, role)
		}
	}

	if roleNeedsConsumerService(role) {
		consumerService = consumer.Service{Store: storeSvc, NoebsConfig: noebsConfig, Logger: logrusLogger, Auth: &auth, HTTPClient: httpclient.Default(), WorkloadSigners: workloadSigners}
	}
	if roleNeedsAdminReportingService(role) {
		adminReportingService = adminreporting.Service{Store: storeSvc}
	}
	if roleNeedsDashboardService(role) {
		dashService = dashboard.Service{Store: storeSvc, NoebsConfig: noebsConfig}
	}
	if roleNeedsMerchantService(role) {
		merchantServices = merchant.Service{Store: storeSvc, Logger: logrusLogger, NoebsConfig: noebsConfig, HTTPClient: httpclient.Default()}
	}
	if roleNeedsWalletService(role) {
		walletService = wallet.NewService(database, noebsConfig)
	}
	if roleNeedsPSPWebhookStore(role) {
		pspWebhookStore = walletstore.New(database)
	}
	if roleNeedsWalletPSPDeps(role) {
		pspStore, err := pspStoreForRole(role)
		if err != nil {
			return err
		}
		walletPSPRegistry, walletPSPLoader, err = buildPSPDeps(pspStore, rawSecrets)
		if err != nil {
			return err
		}
	}
	return nil
}

func roleNeedsConsumerService(role serviceRole) bool {
	return role == serviceRoleIdentityAuth ||
		role == serviceRoleCardVault ||
		role == serviceRoleEBSAdapter ||
		role == serviceRoleNotification ||
		role == serviceRoleBeneficiary
}

func roleNeedsDashboardService(role serviceRole) bool {
	return role == serviceRoleAdminReporting
}

func roleNeedsAdminReportingService(role serviceRole) bool {
	return role == serviceRoleAdminReporting || role == serviceRoleAdminReportingProjector
}

func roleNeedsMerchantService(role serviceRole) bool {
	return role == serviceRoleEBSAdapter
}

func roleNeedsWalletService(role serviceRole) bool {
	return role == serviceRoleWalletLedger ||
		role == serviceRoleWalletWorker
}

func roleNeedsPSPWebhookStore(role serviceRole) bool {
	return role == serviceRolePSPWebhook
}

func roleNeedsWalletPSPDeps(role serviceRole) bool {
	return role == serviceRolePSPWebhook ||
		role == serviceRoleWalletWorker
}

func pspStoreForRole(role serviceRole) (*walletstore.Store, error) {
	switch role {
	case serviceRolePSPWebhook:
		if pspWebhookStore == nil {
			return nil, errMissingPSPStore
		}
		return pspWebhookStore, nil
	case serviceRoleWalletWorker:
		if walletService == nil || walletService.Store == nil {
			return nil, errMissingPSPStore
		}
		return walletService.Store, nil
	default:
		return nil, fmt.Errorf("%w: %s", errMissingPSPStore, role)
	}
}

func registerAdminReportingRoutes(route *fiber.App, tenantIdentity fiber.Handler, adminIdentity fiber.Handler) {
	route.Use("/dashboard/assets", filesystem.New(filesystem.Config{
		Root: dashboard.AssetFileSystem(),
	}))
	dashboardGet := func(path string, handler interface{}) {
		route.Get(path, adminIdentity, tenantIdentity, wrapHandler(handler))
	}
	dashboardGet("/dashboard", dashService.BrowserDashboard)
	dashboardGet("/dashboard/", dashService.BrowserDashboard)
	dashboardGet("/dashboard/get_tid", dashService.TransactionByTid)
	dashboardGet("/dashboard/get", dashService.TransactionByTid)
	dashboardGet("/dashboard/all", dashService.GetAll)
	dashboardGet("/dashboard/all/:id", dashService.GetID)
	dashboardGet("/dashboard/count", dashService.TransactionsCount)
	dashboardGet("/dashboard/settlement", dashService.DailySettlement)
	dashboardGet("/dashboard/merchant", dashService.MerchantTransactionsEndpoint)
	dashboardGet("/dashboard/merchant/:id", dashService.MerchantViews)
	dashboardGet("/dashboard/status", dashService.QRStatus)
	dashboardGet("/dashboard/test_browser", dashService.IndexPage)
	dashboardGet("/dashboard/stream", dashService.Stream)
}

func registerNotificationChatRoutes(route *fiber.App, tenantIdentity fiber.Handler, userIdentity fiber.Handler, _ fiber.Handler, consumerHandler *consumerhandler.Handler) {
	route.Get("/ws", userIdentity, chatWebSocketHandler(hub))

	consumerhandler.RegisterNotificationAdminInternalRoutes(route.Group("/internal/notification-chat", tenantIdentity), consumerHandler)

	cons := route.Group("/consumer", userIdentity)
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

func chatWebSocketHandler(chatHub *chat.Hub) fiber.Handler {
	return func(c *fiber.Ctx) error {
		identity, err := chatGatewayIdentityFromFiber(c)
		if err != nil {
			return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"code": "missing_gateway_identity", "message": "missing gateway identity"})
		}
		return adaptor.HTTPHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = r.WithContext(context.WithValue(context.Background(), chatGatewayIdentityContextKey{}, identity))
			chat.ServeWs(chatHub, w, r)
		})(c)
	}
}

func registerConsumerBeneficiaryRoutes(route *fiber.App, userIdentity fiber.Handler, consumerHandler *consumerhandler.Handler) {
	consumerhandler.RegisterBeneficiaryRoutes(route.Group("/consumer", userIdentity), consumerHandler)
}

func chatClientIDFromGatewayIdentity(r *http.Request) (string, error) {
	if r == nil {
		return "", chat.ErrUnauthorized
	}
	identity, ok := r.Context().Value(chatGatewayIdentityContextKey{}).(chatGatewayIdentity)
	if !ok {
		return "", chat.ErrUnauthorized
	}
	if identity.Mobile == "" {
		return "", chat.ErrUnauthorized
	}
	return identity.Mobile, nil
}

func chatGatewayIdentityFromFiber(c *fiber.Ctx) (chatGatewayIdentity, error) {
	if c == nil {
		return chatGatewayIdentity{}, chat.ErrUnauthorized
	}
	tenantID, ok := c.Locals("tenant_id").(string)
	if !ok {
		return chatGatewayIdentity{}, chat.ErrUnauthorized
	}
	userID, ok := c.Locals("user_id").(int64)
	if !ok {
		return chatGatewayIdentity{}, chat.ErrUnauthorized
	}
	sessionEpoch, ok := c.Locals("session_epoch").(int64)
	if !ok || sessionEpoch <= 0 {
		return chatGatewayIdentity{}, chat.ErrUnauthorized
	}
	token, ok := c.Locals("session_token").(string)
	if !ok || strings.TrimSpace(token) == "" {
		return chatGatewayIdentity{}, chat.ErrUnauthorized
	}
	mobile, ok := c.Locals("mobile").(string)
	if !ok || strings.TrimSpace(mobile) == "" {
		return chatGatewayIdentity{}, chat.ErrUnauthorized
	}
	identity, err := gateway.ParseInternalUserIdentity(tenantID, strconv.FormatInt(userID, 10), mobile)
	if err != nil {
		return chatGatewayIdentity{}, chat.ErrUnauthorized
	}
	identity.SessionEpoch = sessionEpoch
	return chatGatewayIdentity{UserIdentity: identity, Token: token}, nil
}

func registerIdentityAuthRoutes(route *fiber.App, tenantIdentity fiber.Handler, userIdentity fiber.Handler, adminIdentity fiber.Handler, consumerHandler *consumerhandler.Handler) {
	route.Post("/generate_api_key", adminIdentity, consumerHandler.GenerateAPIKey)
	consumerhandler.RegisterIdentityInternalRoutes(route.Group("/internal/identity-auth", tenantIdentity), consumerHandler)

	cons := route.Group("/consumer")
	consumerhandler.RegisterIdentityPublicRoutes(cons.Group("", tenantIdentity), consumerHandler)
	consumerhandler.RegisterIdentityAuthedRoutes(cons.Group("", userIdentity), consumerHandler)
}

func registerCardVaultRoutes(route *fiber.App, tenantIdentity fiber.Handler, userIdentity fiber.Handler, _ fiber.Handler, consumerHandler *consumerhandler.Handler) {
	cons := route.Group("/consumer")
	consumerhandler.RegisterCardVaultAuthedRoutes(cons.Group("", userIdentity), consumerHandler)
	consumerhandler.RegisterCardVaultInternalRoutes(route.Group("/internal/card-vault", userIdentity), consumerHandler)
	consumerhandler.RegisterCardVaultAdminInternalRoutes(route.Group("/internal/card-vault", tenantIdentity), consumerHandler)
}

func registerEBSAdapterRoutes(route *fiber.App, tenantIdentity fiber.Handler, userIdentity fiber.Handler, consumerHandler *consumerhandler.Handler) {
	cons := route.Group("/consumer")
	consumerhandler.RegisterEBSAdapterPublicRoutes(cons.Group("", tenantIdentity), consumerHandler)
	consumerhandler.RegisterEBSAdapterAuthedRoutes(cons.Group("", userIdentity), consumerHandler)
}

func registerWalletAPIRoutes(route *fiber.App, tenantIdentity fiber.Handler, userIdentity fiber.Handler, adminIdentity fiber.Handler) {
	if walletPublicClient == nil {
		logrusLogger.Fatal("wallet-api role requires an initialized wallet-ledger grpc client")
	}
	if walletAdminClient == nil {
		logrusLogger.Fatal("wallet-api role requires an initialized wallet-ledger admin grpc client")
	}
	walletUserHandler := wallethandler.NewGRPCUserHandler(walletPublicClient, noebsConfig)
	walletAdminHandler := wallethandler.NewGRPCAdminHandler(walletAdminClient)
	wallethandler.RegisterGRPCUserRoutes(route.Group("/wallet", userIdentity), walletUserHandler)
	wallethandler.RegisterGRPCAdminRoutes(route.Group("/admin/wallet", adminIdentity, tenantIdentity), walletAdminHandler)
}

// GetMainEngine function responsible for getting all of our routes to be delivered for fiber
func GetMainEngine() *fiber.App {
	ensureInit()
	role, err := currentServiceRole()
	if err != nil {
		logrusLogger.Fatalf("error in runtime service role: %v", err)
	}
	route := fiber.New(fiber.Config{})
	route.Use(gateway.RequestID())
	tenantIdentity := gateway.InternalTenantIdentityMiddleware()
	userIdentity := gateway.InternalUserIdentityMiddleware()
	adminIdentity := gateway.InternalAdminIdentityMiddleware()
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
	route.Get("/test", func(c *fiber.Ctx) error {
		return c.Status(http.StatusOK).JSON(fiber.Map{"message": true})
	})
	if role == serviceRoleAPIGateway {
		route.Get("/metrics", adminGuard, adaptor.HTTPHandler(promhttp.Handler()))
	}
	if roleReceivesSignedHTTP(role) {
		route.Use(signedWorkloadBoundary(role, workloadVerifier))
	}

	buildConsumerHandler := func() *consumerhandler.Handler {
		h, err := consumerhandler.New(&consumerService)
		if err != nil {
			logrusLogger.Fatalf("consumer handler startup: %v", err)
		}
		return h
	}
	if role == serviceRolePSPWebhook {
		walletWebhookHandler := wallethandler.NewPSPWebhookHandler(pspWebhookStore, walletPSPLoader, walletPSPRegistry, walletWorkflowClient)
		wallethandler.RegisterWebhookRoutes(route.Group("", tenantIdentity), walletWebhookHandler)
		return route
	}
	if role == serviceRoleIdentityAuth {
		consumerHandler := buildConsumerHandler()
		registerIdentityAuthRoutes(route, tenantIdentity, userIdentity, adminIdentity, consumerHandler)
		return route
	}
	if role == serviceRoleCardVault {
		consumerHandler := buildConsumerHandler()
		registerCardVaultRoutes(route, tenantIdentity, userIdentity, adminIdentity, consumerHandler)
		return route
	}
	if role == serviceRoleEBSAdapter {
		consumerHandler := buildConsumerHandler()
		registerEBSAdapterRoutes(route, tenantIdentity, userIdentity, consumerHandler)
		return route
	}
	if role == serviceRoleAdminReporting {
		registerAdminReportingRoutes(route, tenantIdentity, adminIdentity)
		return route
	}
	if role == serviceRoleNotification {
		consumerHandler := buildConsumerHandler()
		registerNotificationChatRoutes(route, tenantIdentity, userIdentity, adminIdentity, consumerHandler)
		return route
	}
	if role == serviceRoleBeneficiary {
		consumerHandler := buildConsumerHandler()
		registerConsumerBeneficiaryRoutes(route, userIdentity, consumerHandler)
		return route
	}
	if role == serviceRoleWalletAPI {
		registerWalletAPIRoutes(route, tenantIdentity, userIdentity, adminIdentity)
		return route
	}
	if role != serviceRoleAPIGateway {
		logrusLogger.Fatalf("service role %s does not own HTTP routes", role)
	}
	if err := registerAPIGatewayProxyRoutes(route, noebsConfig, auth, adminGuard); err != nil {
		logrusLogger.Fatalf("error in api gateway service discovery: %v", err)
	}

	route.Get("/app/config", appConfigHandler)

	return route
}

var initOnce sync.Once

func ensureInit() {
	initOnce.Do(initConfig)
}

func init() {
	if isConfigUtilityCommand() {
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

	role, err := currentServiceRole()
	if err != nil {
		logrusLogger.Fatalf("error in runtime service role: %v", err)
	}
	if err := validateServiceDiscoveryCatalog(string(role), noebsConfig); err != nil {
		logrusLogger.Fatalf("error in runtime service discovery config: %v", err)
	}
	tenantID, err := configuredTenantID(noebsConfig)
	if err != nil {
		logrusLogger.Fatalf("error in runtime config: %v", err)
	}
	if err := validateRoleDatabaseConfig(role, noebsConfig.DatabaseURL, noebsConfig.DatabaseDriver); err != nil {
		logrusLogger.Fatalf("error in runtime database config: %v", err)
	}
	if err := validateRoleRuntimeConfig(role, noebsConfig); err != nil {
		logrusLogger.Fatalf("error in runtime service config: %v", err)
	}
	if err := validateWorkloadAuthRuntimeConfig(role, noebsConfig); err != nil {
		logrusLogger.Fatalf("error in workload authentication config: %v", err)
	}
	ebs_fields.ConfigureEBSHTTPClient(noebsConfig)
	configureLogger(noebsConfig)
	if err := initOTel(context.Background(), role, noebsConfig, logrusLogger); err != nil {
		logrusLogger.Fatalf("error initializing otel: %v", err)
	}
	if role.opensDatabase() {
		database, err = store.OpenFromConfig(noebsConfig.DatabaseURL, noebsConfig.DatabaseDriver)
		if err != nil {
			logrusLogger.Fatalf("error in connecting to db: %v", err)
		}
		storeSvc = store.New(database, store.WithDataKey(noebsConfig.DataKey))
		migrateCtx, cancelMigrate := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelMigrate()
		if migrationScope, ok := role.migrationScope(); ok {
			if err := store.MigrateScope(migrateCtx, database, tenantID, migrationScope); err != nil {
				logrusLogger.Fatalf("error in migrations: %v", err)
			}
			if err := storeSvc.EnsureTenant(migrateCtx, tenantID); err != nil {
				logrusLogger.Fatalf("error ensuring tenant: %v", err)
			}
			if err := ensureNoReservedTenant(migrateCtx, storeSvc); err != nil {
				logrusLogger.Fatalf("error validating tenants: %v", err)
			}
		} else {
			logrusLogger.Printf("Migrations are owned by service-specific migration roles; current role is %s", role)
		}
	} else {
		logrusLogger.Printf("%s role does not open a service database", role)
	}

	logrusLogger.Printf(
		"Runtime config loaded: role=%s driver=%s tenant=%s port=%s database=%t temporal=%t grpc=%t wallet=%t",
		role,
		noebsConfig.DatabaseDriver,
		noebsConfig.DefaultTenantID,
		noebsConfig.Port,
		role.opensDatabase(),
		noebsConfig.TemporalEnabled,
		noebsConfig.GRPCEnabled,
		noebsConfig.WalletEnabled,
	)
	if role.runsMigrations() {
		dataConfigs.DB = database
		return
	}
	if err := initWorkloadAuth(role, noebsConfig); err != nil {
		logrusLogger.Fatalf("error initializing workload authentication: %v", err)
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
	if role == serviceRoleAPIGateway {
		sessionValidator, err := newIdentitySessionValidator(noebsConfig, workloadSigners)
		if err != nil {
			logrusLogger.Fatalf("configure identity session validation: %v", err)
		}
		auth.Sessions = sessionValidator
	}
	if role.startsChat() && database != nil && database.DB != nil {
		sessionValidator, err := newChatSessionValidator(noebsConfig, workloadSigners)
		if err != nil {
			logrusLogger.Fatalf("configure chat session validation: %v", err)
		}
		chatCfg := chat.DefaultHubConfig()
		chatCfg.MaxUnreadMessages = 1000
		chatCfg.UnreadBatchSize = 200
		chatCfg.PersistBatchSize = 128
		chatCfg.PersistFlushInterval = 10 * time.Millisecond
		chatCfg.ClientIDFromRequest = chatClientIDFromGatewayIdentity
		chatCfg.ValidateClientSession = chatSessionValidation(sessionValidator)
		chatCfg.SessionValidationInterval = chatSessionValidationInterval
		hub = chat.NewHubWithConfig(database.DB, chatCfg)
	}
	if role.startsChat() && (database == nil || database.DB == nil) {
		logrusLogger.Fatalf("%s role requires an initialized database", role)
	}
	if err := initRoleServices(role); err != nil {
		logrusLogger.Fatalf("error initializing role services: %v", err)
	}
	if role.startsEBSEventPublisher() {
		writer, err := eventing.NewKafkaWriter(noebsConfig.KafkaBrokers, noebsConfig.KafkaTransactionTopic)
		if err != nil {
			logrusLogger.Fatalf("error creating ebs transaction event writer: %v", err)
		}
		ebsEventPublisher = &eventing.OutboxPublisher{
			Store:        storeSvc,
			Writer:       writer,
			Topic:        noebsConfig.KafkaTransactionTopic,
			BatchSize:    noebsConfig.EBSTransactionEventPublisherBatchSize,
			PollInterval: time.Duration(noebsConfig.EBSTransactionEventPublisherPollIntervalMs) * time.Millisecond,
		}
	}
	if role.startsAdminReportingProjector() {
		reader, err := eventing.NewKafkaReader(noebsConfig.KafkaBrokers, noebsConfig.KafkaTransactionTopic, noebsConfig.AdminReportingKafkaConsumerGroup)
		if err != nil {
			logrusLogger.Fatalf("error creating admin-reporting transaction event reader: %v", err)
		}
		adminReportingProjector = &eventing.AdminReportingProjector{
			Reader:  reader,
			Service: &adminReportingService,
			Topic:   noebsConfig.KafkaTransactionTopic,
		}
	}
	if role == serviceRolePSPWebhook {
		client, err := walletworker.NewClient(walletworker.Options{
			Host:      noebsConfig.TemporalHost,
			Port:      noebsConfig.TemporalPort,
			Namespace: noebsConfig.TemporalNamespace,
			TaskQueue: walletworker.TaskQueueMain,
		})
		if err != nil {
			logrusLogger.Fatalf("error creating wallet workflow client: %v", err)
		}
		walletWorkflowClient = client
		walletWorkflowCloser = client
	}
	if role == serviceRoleWalletAPI {
		if err := initWalletLedgerPublicClient(noebsConfig); err != nil {
			logrusLogger.Fatalf("error creating wallet-ledger grpc client: %v", err)
		}
	}
	if role.startsWalletWorker() {
		if walletPSPRegistry == nil || walletPSPLoader == nil {
			logrusLogger.Fatalf("error in wallet-worker PSP dependencies: %v", errMissingWalletPSPDeps)
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
		tenants, err := storeSvc.ListTenants(context.Background())
		if err != nil {
			logrusLogger.Fatalf("error listing tenants for wallet schedules: %v", err)
		}
		if err := startWalletCronWorkflows(context.Background(), runner.Client, tenants, noebsConfig, workerOpts.TaskQueue); err != nil {
			logrusLogger.Fatalf("error starting wallet cron workflows: %v", err)
		}
	}
	if role.startsGRPC() {
		if err := initGRPCServers(); err != nil {
			logrusLogger.Fatalf("error initializing grpc servers: %v", err)
		}
	}
	dataConfigs.DB = database

}
