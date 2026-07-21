package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	walletworker "github.com/adonese/noebs/wallet/worker"
)

type serviceRole string

var (
	errMissingServiceRole  = errors.New("missing noebs.service_role")
	errInvalidServiceRole  = errors.New("invalid noebs.service_role")
	errMissingDatabaseURL  = errors.New("missing noebs.db_url")
	errDatabaseNotAllowed  = errors.New("database config not allowed for service role")
	errDatabaseOwnerKey    = errors.New("service database entry must use owner role")
	errWalletNotEnabled    = errors.New("wallet_enabled required for service role")
	errTemporalNotEnabled  = errors.New("temporal_enabled required for service role")
	errGRPCNotEnabled      = errors.New("grpc_enabled required for service role")
	errMissingGRPCPort     = errors.New("missing noebs.grpc_port")
	errMissingGRPCGateway  = errors.New("missing noebs.grpc_gateway_port")
	errInvalidWalletConfig = errors.New("invalid wallet runtime config")
	errMissingEBSConfig    = errors.New("missing ebs-adapter runtime config")
	errLegacyEBSConfig     = errors.New("legacy ebs-adapter runtime selector not allowed")
	errMissingKafkaConfig  = errors.New("missing kafka runtime config")
	errMissingHealthPort   = errors.New("missing background health port")
	errHealthNotAllowed    = errors.New("background health not allowed for service role")
)

const (
	serviceRoleAPIGateway              serviceRole = "api-gateway"
	serviceRoleIdentityAuth            serviceRole = "identity-auth"
	serviceRoleCardVault               serviceRole = "card-vault"
	serviceRoleEBSAdapter              serviceRole = "ebs-adapter"
	serviceRoleEBSAdapterEvents        serviceRole = "ebs-adapter-events"
	serviceRolePSPWebhook              serviceRole = "psp-webhook"
	serviceRoleAdminReporting          serviceRole = "admin-reporting"
	serviceRoleAdminReportingProjector serviceRole = "admin-reporting-projector"
	serviceRoleNotification            serviceRole = "notification-chat"
	serviceRoleWalletAPI               serviceRole = "wallet-api"
	serviceRoleWalletLedger            serviceRole = "wallet-ledger"
	serviceRoleWalletWorker            serviceRole = "wallet-worker"
	serviceRoleWorkloadAuthMigrate     serviceRole = "workload-auth-migrate"
	serviceRoleWorkloadAuthCleanup     serviceRole = "workload-auth-cleanup"
	serviceRoleGatewayAuthMigrate      serviceRole = "gateway-auth-migrate"
	serviceRoleGatewayAuthCleanup      serviceRole = "gateway-auth-cleanup"

	serviceRoleIdentityAuthMigrate   serviceRole = "identity-auth-migrate"
	serviceRoleCardVaultMigrate      serviceRole = "card-vault-migrate"
	serviceRoleEBSAdapterMigrate     serviceRole = "ebs-adapter-migrate"
	serviceRoleAdminReportingMigrate serviceRole = "admin-reporting-migrate"
	serviceRoleNotificationMigrate   serviceRole = "notification-chat-migrate"
	serviceRoleWalletLedgerMigrate   serviceRole = "wallet-ledger-migrate"
)

var serviceRoleCatalog = [...]serviceRole{
	serviceRoleAPIGateway,
	serviceRoleIdentityAuth,
	serviceRoleCardVault,
	serviceRoleEBSAdapter,
	serviceRoleEBSAdapterEvents,
	serviceRolePSPWebhook,
	serviceRoleAdminReporting,
	serviceRoleAdminReportingProjector,
	serviceRoleNotification,
	serviceRoleWalletAPI,
	serviceRoleWalletLedger,
	serviceRoleWalletWorker,
	serviceRoleWorkloadAuthMigrate,
	serviceRoleWorkloadAuthCleanup,
	serviceRoleGatewayAuthMigrate,
	serviceRoleGatewayAuthCleanup,
	serviceRoleIdentityAuthMigrate,
	serviceRoleCardVaultMigrate,
	serviceRoleEBSAdapterMigrate,
	serviceRoleAdminReportingMigrate,
	serviceRoleNotificationMigrate,
	serviceRoleWalletLedgerMigrate,
}

func currentServiceRole() (serviceRole, error) {
	return parseServiceRole(noebsConfig.ServiceRole)
}

func parseServiceRole(value string) (serviceRole, error) {
	role := serviceRole(strings.TrimSpace(value))
	if role == "" {
		return "", errMissingServiceRole
	}
	for _, known := range serviceRoleCatalog {
		if role == known {
			return role, nil
		}
	}
	return "", fmt.Errorf("%w: %q", errInvalidServiceRole, value)
}

func (r serviceRole) startsHTTP() bool {
	return r == serviceRoleAPIGateway || r == serviceRoleIdentityAuth || r == serviceRoleCardVault || r == serviceRoleEBSAdapter || r == serviceRolePSPWebhook || r == serviceRoleAdminReporting || r == serviceRoleNotification || r == serviceRoleWalletAPI
}

func (r serviceRole) startsGRPC() bool {
	return r == serviceRoleWalletLedger
}

func (r serviceRole) startsWalletWorker() bool {
	return r == serviceRoleWalletWorker
}

func (r serviceRole) startsEBSEventPublisher() bool {
	return r == serviceRoleEBSAdapterEvents
}

func (r serviceRole) startsAdminReportingProjector() bool {
	return r == serviceRoleAdminReportingProjector
}

func (r serviceRole) startsBackgroundHealth() bool {
	return r == serviceRoleWalletWorker || r == serviceRoleEBSAdapterEvents || r == serviceRoleAdminReportingProjector
}

func (r serviceRole) startsChat() bool {
	return r == serviceRoleNotification
}

func (r serviceRole) cleansWorkloadAuthNonces() bool {
	return r == serviceRoleWorkloadAuthCleanup
}

func (r serviceRole) cleansGatewayAuthSessions() bool {
	return r == serviceRoleGatewayAuthCleanup
}

func (r serviceRole) opensDatabase() bool {
	_, ok := r.databaseOwnerRole()
	return ok
}

func (r serviceRole) requiresTemporal() bool {
	return r == serviceRoleWalletLedger || r == serviceRoleWalletWorker
}

func validateRoleDatabaseConfig(role serviceRole, dbURL, driver string) error {
	if !role.opensDatabase() {
		if strings.TrimSpace(dbURL) != "" {
			return fmt.Errorf("%w: %s must not set noebs.db_url", errDatabaseNotAllowed, role)
		}
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "":
		return fmt.Errorf("%w: %s requires noebs.db_driver", store.ErrMissingDatabaseDriver, role)
	case "postgres", store.DriverPostgres:
		if strings.TrimSpace(dbURL) == "" {
			return fmt.Errorf("%w: %s requires noebs.db_url", errMissingDatabaseURL, role)
		}
		spec, present := postgresRoleSpecForService(role)
		if !present {
			return fmt.Errorf("%w: no Postgres role is assigned to %s", errPostgresDatabaseIdentity, role)
		}
		return validatePostgresDatabaseIdentity(dbURL, spec)
	default:
		return fmt.Errorf("%w: %s noebs.db_driver %q", store.ErrUnsupportedDatabaseDriver, role, driver)
	}
}

func validateRoleRuntimeConfig(role serviceRole, cfg ebs_fields.NoebsConfig) error {
	if err := validateDatabaseTransportRuntimeConfig(role, cfg); err != nil {
		return err
	}
	if err := validateInternalTransportRuntimeConfig(role, cfg); err != nil {
		return err
	}
	if err := validateOTelRuntimeConfig(role, cfg); err != nil {
		return err
	}
	if role == serviceRoleAPIGateway {
		if err := validateBackofficeRuntimeConfig(cfg); err != nil {
			return fmt.Errorf("back-office OIDC runtime: %w", err)
		}
		if err := validateWalletAuthorizationRuntimeConfig(cfg); err != nil {
			return fmt.Errorf("wallet transaction authorization runtime: %w", err)
		}
		if err := validatePSPWebhookRoutes(cfg.PSPWebhookRoutes); err != nil {
			return err
		}
	} else if len(cfg.PSPWebhookRoutes) != 0 {
		return errors.New("noebs.psp_webhook_routes is allowed only for api-gateway")
	}
	if roleRequiresDataKey(role) && strings.TrimSpace(cfg.DataKey) == "" {
		return fmt.Errorf("%w: %s", store.ErrMissingDataKey, role)
	}
	if err := validateEBSRuntimeConfig(role, cfg); err != nil {
		return err
	}
	if err := validateKafkaRuntimeConfig(role, cfg); err != nil {
		return err
	}
	if role.startsBackgroundHealth() && strings.TrimSpace(cfg.Port) == "" {
		return fmt.Errorf("%w: %s requires noebs.port", errMissingHealthPort, role)
	}
	if role == serviceRoleEBSAdapter {
		if _, err := serviceDiscoveryEndpoint(cfg, serviceRoleCardVault); err != nil {
			return err
		}
		if _, err := serviceDiscoveryEndpoint(cfg, serviceRoleNotification); err != nil {
			return err
		}
	}
	if roleUsesWalletFeature(role) && !cfg.WalletEnabled {
		return fmt.Errorf("%w: %s", errWalletNotEnabled, role)
	}
	if roleUsesWalletFeature(role) {
		if err := validateWalletRuntimeSettings(cfg); err != nil {
			return err
		}
	}
	if role.requiresTemporal() {
		if !cfg.TemporalEnabled {
			return fmt.Errorf("%w: %s", errTemporalNotEnabled, role)
		}
		expectedClientID := temporalLedgerClientID
		if role == serviceRoleWalletWorker {
			expectedClientID = temporalWorkerClientID
		}
		if _, err := buildTemporalOptions(context.Background(), cfg, walletworker.TaskQueueMain, expectedClientID); err != nil {
			return fmt.Errorf("%s temporal config: %w", role, err)
		}
	}
	if role == serviceRoleWalletLedger {
		if !cfg.GRPCEnabled {
			return fmt.Errorf("%w: %s", errGRPCNotEnabled, role)
		}
		if strings.TrimSpace(cfg.GRPCPort) == "" {
			return fmt.Errorf("%w: %s", errMissingGRPCPort, role)
		}
	}
	if cfg.GRPCGatewayEnabled && strings.TrimSpace(cfg.GRPCGatewayPort) == "" {
		return fmt.Errorf("%w: %s", errMissingGRPCGateway, role)
	}
	if role == serviceRoleWalletWorker {
		return validateWalletWorkerSchedules(cfg)
	}
	return nil
}

func validateDatabaseTransportRuntimeConfig(role serviceRole, cfg ebs_fields.NoebsConfig) error {
	urls := make([]string, 0, 2)
	if role.opensDatabase() {
		urls = append(urls, cfg.DatabaseURL)
	}
	if roleReceivesSignedHTTP(role) {
		urls = append(urls, cfg.WorkloadAuth.NonceDatabaseURL)
	}
	if len(urls) == 0 {
		if strings.TrimSpace(cfg.DatabaseCACertificate) != "" {
			return errors.New("noebs.database_ca_certificate is not allowed for this role")
		}
		return nil
	}
	ca := strings.TrimSpace(cfg.DatabaseCACertificate)
	if cfg.InternalTransport.Present() && ca == "" {
		return errors.New("noebs.database_ca_certificate is required for the release transport boundary")
	}
	if ca == "" {
		return nil
	}
	for _, databaseURL := range urls {
		if err := store.ValidateDatabaseTLSConfig(databaseURL, ca); err != nil {
			return err
		}
	}
	return nil
}

func roleUsesWalletFeature(role serviceRole) bool {
	return role == serviceRoleAPIGateway ||
		role == serviceRolePSPWebhook ||
		role == serviceRoleWalletAPI ||
		role == serviceRoleWalletLedger ||
		role == serviceRoleWalletWorker
}

func roleRequiresDataKey(role serviceRole) bool {
	return role == serviceRoleCardVault || role == serviceRoleCardVaultMigrate
}

func validateEBSRuntimeConfig(role serviceRole, cfg ebs_fields.NoebsConfig) error {
	if role != serviceRoleEBSAdapter {
		return nil
	}
	legacySelectors := map[string]bool{
		"is_consumer_prod": cfg.IsConsumerProd,
		"is_merchant_prod": cfg.IsMerchantProd,
	}
	for key, value := range legacySelectors {
		if value {
			return fmt.Errorf("%w: noebs.%s", errLegacyEBSConfig, key)
		}
	}
	legacyValues := map[string]string{
		"consumer_qa":      cfg.ConsumerQAIP,
		"consumer_prod":    cfg.ConsumerProd,
		"merchant_qa":      cfg.MerchantQAIP,
		"merchant_prod":    cfg.MerchantProd,
		"ipin_qa":          cfg.IPINQA,
		"ipin_prod":        cfg.IPIN,
		"consumer_qa_id":   cfg.ConsumerQAID,
		"consumer_prod_id": cfg.ConsumerProdID,
		"merchant_qa_id":   cfg.MerchantQAID,
		"merchant_prod_id": cfg.MerchantProdID,
	}
	for key, value := range legacyValues {
		if strings.TrimSpace(value) != "" {
			return fmt.Errorf("%w: noebs.%s", errLegacyEBSConfig, key)
		}
	}
	required := map[string]string{
		"consumer_endpoint": cfg.ConsumerIP,
		"merchant_endpoint": cfg.MerchantIP,
		"ipin_endpoint":     cfg.IPINIp,
		"consumer_app_id":   cfg.ConsumerID,
		"merchant_app_id":   cfg.MerchantID,
		"ipin_username":     cfg.EBSIPINUsername,
		"ipin_password":     cfg.EBSIPINPassword,
		"pub_key":           cfg.EBSConsumerKey,
		"ipin_key":          cfg.EBSIpinKey,
		"pan":               cfg.BillInquiryPAN,
		"pin":               cfg.BillInquiryPIN,
		"ipin":              cfg.BillInquiryIPIN,
		"exp_date":          cfg.BillInquiryExpDate,
	}
	for key, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: noebs.%s", errMissingEBSConfig, key)
		}
	}
	return nil
}

func validateKafkaRuntimeConfig(role serviceRole, cfg ebs_fields.NoebsConfig) error {
	if role != serviceRoleEBSAdapter && role != serviceRoleEBSAdapterEvents && role != serviceRoleAdminReportingProjector {
		return nil
	}
	if len(cfg.KafkaBrokers) == 0 {
		return fmt.Errorf("%w: noebs.kafka_brokers", errMissingKafkaConfig)
	}
	for i, broker := range cfg.KafkaBrokers {
		if strings.TrimSpace(broker) == "" {
			return fmt.Errorf("%w: noebs.kafka_brokers[%d]", errMissingKafkaConfig, i)
		}
		if err := validateHostPortServiceDiscoveryEndpoint(fmt.Sprintf("noebs.kafka_brokers[%d]", i), broker); err != nil {
			return err
		}
	}
	if strings.TrimSpace(cfg.KafkaTransactionTopic) == "" {
		return fmt.Errorf("%w: noebs.kafka_transaction_topic", errMissingKafkaConfig)
	}
	if role == serviceRoleAdminReportingProjector && strings.TrimSpace(cfg.AdminReportingKafkaConsumerGroup) == "" {
		return fmt.Errorf("%w: noebs.admin_reporting_kafka_consumer_group", errMissingKafkaConfig)
	}
	if role == serviceRoleEBSAdapterEvents {
		if cfg.EBSTransactionEventPublisherBatchSize <= 0 {
			return fmt.Errorf("%w: noebs.ebs_transaction_event_publisher_batch_size", errMissingKafkaConfig)
		}
		if cfg.EBSTransactionEventPublisherPollIntervalMs <= 0 {
			return fmt.Errorf("%w: noebs.ebs_transaction_event_publisher_poll_interval_ms", errMissingKafkaConfig)
		}
	}
	return nil
}

func validateWalletRuntimeSettings(cfg ebs_fields.NoebsConfig) error {
	if strings.TrimSpace(cfg.WalletDefaultCurrency) == "" {
		return fmt.Errorf("%w: wallet_default_currency", errInvalidWalletConfig)
	}
	if cfg.WalletHoldExpirySeconds <= 0 || cfg.WalletHoldExpirySeconds > math.MaxInt32 {
		return fmt.Errorf("%w: wallet_hold_expiry_seconds", errInvalidWalletConfig)
	}
	if cfg.WalletApprovalTimeoutSeconds <= 0 || cfg.WalletApprovalTimeoutSeconds > math.MaxInt32 {
		return fmt.Errorf("%w: wallet_approval_timeout_seconds", errInvalidWalletConfig)
	}
	if cfg.WalletManualTransferApprovalTimeoutSeconds <= 0 {
		return fmt.Errorf("%w: wallet_manual_approval_timeout_seconds", errInvalidWalletConfig)
	}
	if cfg.WalletApprovalThreshold < 0 {
		return fmt.Errorf("%w: wallet_approval_threshold", errInvalidWalletConfig)
	}
	if cfg.WalletFXQuoteMaxPerUserObservation <= 0 {
		return fmt.Errorf("%w: wallet_fx_quote_max_per_user_observation", errInvalidWalletConfig)
	}
	return nil
}

func validateWalletWorkerSchedules(cfg ebs_fields.NoebsConfig) error {
	if strings.TrimSpace(cfg.WalletFXRefreshCron) == "" {
		return fmt.Errorf("%w: wallet_fx_refresh_cron", errMissingWalletWorkflowCron)
	}
	if strings.TrimSpace(cfg.WalletPSPPollerCron) == "" {
		return fmt.Errorf("%w: wallet_psp_poller_cron", errMissingWalletWorkflowCron)
	}
	if cfg.WalletPSPPollerBatchSize <= 0 {
		return fmt.Errorf("%w: wallet_psp_poller_batch_size", errInvalidWalletConfig)
	}
	if cfg.WalletPSPPollerIntervalSeconds <= 0 {
		return fmt.Errorf("%w: wallet_psp_poller_interval_seconds", errInvalidWalletConfig)
	}
	if strings.TrimSpace(cfg.WalletReconciliationCron) == "" {
		return fmt.Errorf("%w: wallet_reconciliation_cron", errMissingWalletWorkflowCron)
	}
	if cfg.WalletReconciliationBatchSize <= 0 {
		return fmt.Errorf("%w: wallet_reconciliation_batch_size", errInvalidWalletConfig)
	}
	if cfg.WalletReconciliationLookbackHours <= 0 {
		return fmt.Errorf("%w: wallet_reconciliation_lookback_hours", errInvalidWalletConfig)
	}
	return nil
}

func (r serviceRole) runsMigrations() bool {
	_, ok := r.migrationScope()
	return ok
}

func (r serviceRole) databaseOwnerRole() (serviceRole, bool) {
	switch r {
	case serviceRoleIdentityAuth, serviceRoleIdentityAuthMigrate:
		return serviceRoleIdentityAuth, true
	case serviceRoleCardVault, serviceRoleCardVaultMigrate:
		return serviceRoleCardVault, true
	case serviceRoleEBSAdapter, serviceRoleEBSAdapterEvents, serviceRoleEBSAdapterMigrate:
		return serviceRoleEBSAdapter, true
	case serviceRolePSPWebhook:
		return serviceRoleWalletLedger, true
	case serviceRoleAdminReporting, serviceRoleAdminReportingProjector, serviceRoleAdminReportingMigrate:
		return serviceRoleAdminReporting, true
	case serviceRoleNotification, serviceRoleNotificationMigrate:
		return serviceRoleNotification, true
	case serviceRoleWalletLedger, serviceRoleWalletLedgerMigrate, serviceRoleWalletWorker:
		return serviceRoleWalletLedger, true
	case serviceRoleWorkloadAuthMigrate, serviceRoleWorkloadAuthCleanup:
		return serviceRoleWorkloadAuthMigrate, true
	case serviceRoleAPIGateway, serviceRoleGatewayAuthMigrate, serviceRoleGatewayAuthCleanup:
		return serviceRoleAPIGateway, true
	default:
		return "", false
	}
}

func (r serviceRole) migrationScope() (string, bool) {
	switch r {
	case serviceRoleIdentityAuthMigrate:
		return store.MigrationScopeIdentityAuth, true
	case serviceRoleCardVaultMigrate:
		return store.MigrationScopeCardVault, true
	case serviceRoleEBSAdapterMigrate:
		return store.MigrationScopeEBSAdapter, true
	case serviceRoleAdminReportingMigrate:
		return store.MigrationScopeAdminReporting, true
	case serviceRoleNotificationMigrate:
		return store.MigrationScopeNotificationChat, true
	case serviceRoleWalletLedgerMigrate:
		return store.MigrationScopeWalletLedger, true
	case serviceRoleWorkloadAuthMigrate:
		return store.MigrationScopeWorkloadAuth, true
	case serviceRoleGatewayAuthMigrate:
		return store.MigrationScopeGatewayAuth, true
	default:
		return "", false
	}
}
