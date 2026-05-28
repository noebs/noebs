package main

import (
	"errors"
	"fmt"
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
	errMissingGatewayAuth  = errors.New("missing api-gateway auth runtime config")
	errMissingIdentityAuth = errors.New("missing identity-auth runtime config")
	errInvalidWalletConfig = errors.New("invalid wallet runtime config")
	errMissingEBSConfig    = errors.New("missing ebs-adapter runtime config")
	errLegacyEBSConfig     = errors.New("legacy ebs-adapter runtime selector not allowed")
	errMissingKafkaConfig  = errors.New("missing kafka runtime config")
)

const (
	serviceRoleAPIGateway     serviceRole = "api-gateway"
	serviceRoleIdentityAuth   serviceRole = "identity-auth"
	serviceRoleCardVault      serviceRole = "card-vault"
	serviceRoleEBSAdapter     serviceRole = "ebs-adapter"
	serviceRolePSPWebhook     serviceRole = "psp-webhook"
	serviceRoleAdminReporting serviceRole = "admin-reporting"
	serviceRoleNotification   serviceRole = "notification-chat"
	serviceRoleBeneficiary    serviceRole = "consumer-beneficiary"
	serviceRoleWalletAPI      serviceRole = "wallet-api"
	serviceRoleWalletLedger   serviceRole = "wallet-ledger"
	serviceRoleWalletWorker   serviceRole = "wallet-worker"

	serviceRoleIdentityAuthMigrate   serviceRole = "identity-auth-migrate"
	serviceRoleCardVaultMigrate      serviceRole = "card-vault-migrate"
	serviceRoleEBSAdapterMigrate     serviceRole = "ebs-adapter-migrate"
	serviceRolePSPWebhookMigrate     serviceRole = "psp-webhook-migrate"
	serviceRoleAdminReportingMigrate serviceRole = "admin-reporting-migrate"
	serviceRoleNotificationMigrate   serviceRole = "notification-chat-migrate"
	serviceRoleBeneficiaryMigrate    serviceRole = "consumer-beneficiary-migrate"
	serviceRoleWalletLedgerMigrate   serviceRole = "wallet-ledger-migrate"
)

func currentServiceRole() (serviceRole, error) {
	return parseServiceRole(noebsConfig.ServiceRole)
}

func parseServiceRole(value string) (serviceRole, error) {
	role := serviceRole(strings.TrimSpace(value))
	switch role {
	case "":
		return "", errMissingServiceRole
	case serviceRoleAPIGateway,
		serviceRoleIdentityAuth,
		serviceRoleCardVault,
		serviceRoleEBSAdapter,
		serviceRolePSPWebhook,
		serviceRoleAdminReporting,
		serviceRoleNotification,
		serviceRoleBeneficiary,
		serviceRoleWalletAPI,
		serviceRoleWalletLedger,
		serviceRoleWalletWorker,
		serviceRoleIdentityAuthMigrate,
		serviceRoleCardVaultMigrate,
		serviceRoleEBSAdapterMigrate,
		serviceRolePSPWebhookMigrate,
		serviceRoleAdminReportingMigrate,
		serviceRoleNotificationMigrate,
		serviceRoleBeneficiaryMigrate,
		serviceRoleWalletLedgerMigrate:
		return role, nil
	default:
		return "", fmt.Errorf("%w: %q", errInvalidServiceRole, value)
	}
}

func (r serviceRole) startsHTTP() bool {
	return r == serviceRoleAPIGateway || r == serviceRoleIdentityAuth || r == serviceRoleCardVault || r == serviceRoleEBSAdapter || r == serviceRolePSPWebhook || r == serviceRoleAdminReporting || r == serviceRoleNotification || r == serviceRoleBeneficiary || r == serviceRoleWalletAPI
}

func (r serviceRole) startsGRPC() bool {
	return r == serviceRoleWalletLedger
}

func (r serviceRole) startsWalletWorker() bool {
	return r == serviceRoleWalletWorker
}

func (r serviceRole) startsChat() bool {
	return r == serviceRoleNotification
}

func (r serviceRole) opensDatabase() bool {
	_, ok := r.databaseOwnerRole()
	return ok
}

func (r serviceRole) requiresTemporal() bool {
	return r == serviceRolePSPWebhook || r == serviceRoleWalletLedger || r == serviceRoleWalletWorker
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
		return nil
	default:
		return fmt.Errorf("%w: %s noebs.db_driver %q", store.ErrUnsupportedDatabaseDriver, role, driver)
	}
}

func validateRoleRuntimeConfig(role serviceRole, cfg ebs_fields.NoebsConfig) error {
	if err := validateOTelRuntimeConfig(role, cfg); err != nil {
		return err
	}
	if roleRequiresDataKey(role) && strings.TrimSpace(cfg.DataKey) == "" {
		return fmt.Errorf("%w: %s", store.ErrMissingDataKey, role)
	}
	if err := validateGatewayAuthRuntimeConfig(role, cfg); err != nil {
		return err
	}
	if err := validateIdentityAuthRuntimeConfig(role, cfg); err != nil {
		return err
	}
	if err := validateEBSRuntimeConfig(role, cfg); err != nil {
		return err
	}
	if err := validateKafkaRuntimeConfig(role, cfg); err != nil {
		return err
	}
	if role == serviceRoleIdentityAuth {
		if _, err := serviceDiscoveryEndpoint(cfg, serviceRoleCardVault); err != nil {
			return err
		}
	}
	if role == serviceRoleEBSAdapter {
		if _, err := serviceDiscoveryEndpoint(cfg, serviceRoleCardVault); err != nil {
			return err
		}
		if _, err := serviceDiscoveryEndpoint(cfg, serviceRoleIdentityAuth); err != nil {
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
		if err := (walletworker.Options{
			Host:      strings.TrimSpace(cfg.TemporalHost),
			Port:      strings.TrimSpace(cfg.TemporalPort),
			Namespace: strings.TrimSpace(cfg.TemporalNamespace),
			TaskQueue: walletworker.TaskQueueMain,
		}).Validate(); err != nil {
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

func roleUsesWalletFeature(role serviceRole) bool {
	return role == serviceRolePSPWebhook ||
		role == serviceRoleWalletAPI ||
		role == serviceRoleWalletLedger ||
		role == serviceRoleWalletWorker
}

func roleRequiresDataKey(role serviceRole) bool {
	return role == serviceRoleCardVault || role == serviceRoleCardVaultMigrate
}

func validateGatewayAuthRuntimeConfig(role serviceRole, cfg ebs_fields.NoebsConfig) error {
	if role != serviceRoleAPIGateway {
		return nil
	}
	required := []struct {
		key   string
		value string
	}{
		{key: "jwt_secret", value: cfg.JWTKey},
		{key: "admin_key", value: cfg.AdminKey},
		{key: "admin_user", value: cfg.AdminUser},
		{key: "admin_password", value: cfg.AdminPassword},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%w: noebs.%s", errMissingGatewayAuth, field.key)
		}
	}
	return nil
}

func validateIdentityAuthRuntimeConfig(role serviceRole, cfg ebs_fields.NoebsConfig) error {
	if role != serviceRoleIdentityAuth {
		return nil
	}
	required := []struct {
		key   string
		value string
	}{
		{key: "jwt_secret", value: cfg.JWTKey},
		{key: "sms_key", value: cfg.SMSAPIKey},
		{key: "sms_sender", value: cfg.SMSSender},
		{key: "sms_gateway", value: cfg.SMSGateway},
		{key: "sms_message", value: cfg.SMSMessage},
		{key: "google_client_id", value: cfg.GoogleClientID},
		{key: "google_client_secret", value: cfg.GoogleClientSecret},
		{key: "google_redirect_url", value: cfg.GoogleRedirectURL},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%w: noebs.%s", errMissingIdentityAuth, field.key)
		}
	}
	return nil
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
	if cfg.EBSDynamicFees.CardTransferfees <= 0 {
		return fmt.Errorf("%w: noebs.ebs_dynamic_fees.p2p_fees", errMissingEBSConfig)
	}
	if cfg.EBSDynamicFees.SpecialPaymentFees <= 0 {
		return fmt.Errorf("%w: noebs.ebs_dynamic_fees.special_payment_fees", errMissingEBSConfig)
	}
	if cfg.EBSDynamicFees.CustomFees <= 0 {
		return fmt.Errorf("%w: noebs.ebs_dynamic_fees.custom_fees", errMissingEBSConfig)
	}
	return nil
}

func validateKafkaRuntimeConfig(role serviceRole, cfg ebs_fields.NoebsConfig) error {
	if role != serviceRoleEBSAdapter && role != serviceRoleAdminReporting {
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
	if role == serviceRoleAdminReporting && strings.TrimSpace(cfg.AdminReportingKafkaConsumerGroup) == "" {
		return fmt.Errorf("%w: noebs.admin_reporting_kafka_consumer_group", errMissingKafkaConfig)
	}
	return nil
}

func validateWalletRuntimeSettings(cfg ebs_fields.NoebsConfig) error {
	if strings.TrimSpace(cfg.WalletDefaultCurrency) == "" {
		return fmt.Errorf("%w: wallet_default_currency", errInvalidWalletConfig)
	}
	if cfg.WalletHoldExpirySeconds <= 0 {
		return fmt.Errorf("%w: wallet_hold_expiry_seconds", errInvalidWalletConfig)
	}
	if cfg.WalletApprovalTimeoutSeconds <= 0 {
		return fmt.Errorf("%w: wallet_approval_timeout_seconds", errInvalidWalletConfig)
	}
	if cfg.WalletVerificationTimeoutSeconds <= 0 {
		return fmt.Errorf("%w: wallet_verification_timeout_seconds", errInvalidWalletConfig)
	}
	if cfg.WalletManualTransferApprovalTimeoutSeconds <= 0 {
		return fmt.Errorf("%w: wallet_manual_approval_timeout_seconds", errInvalidWalletConfig)
	}
	return nil
}

func validateWalletWorkerSchedules(cfg ebs_fields.NoebsConfig) error {
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
	case serviceRoleEBSAdapter, serviceRoleEBSAdapterMigrate:
		return serviceRoleEBSAdapter, true
	case serviceRolePSPWebhook, serviceRolePSPWebhookMigrate:
		return serviceRolePSPWebhook, true
	case serviceRoleAdminReporting, serviceRoleAdminReportingMigrate:
		return serviceRoleAdminReporting, true
	case serviceRoleNotification, serviceRoleNotificationMigrate:
		return serviceRoleNotification, true
	case serviceRoleBeneficiary, serviceRoleBeneficiaryMigrate:
		return serviceRoleBeneficiary, true
	case serviceRoleWalletLedger, serviceRoleWalletLedgerMigrate, serviceRoleWalletWorker:
		return serviceRoleWalletLedger, true
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
	case serviceRolePSPWebhookMigrate:
		return store.MigrationScopePSPWebhook, true
	case serviceRoleAdminReportingMigrate:
		return store.MigrationScopeAdminReporting, true
	case serviceRoleNotificationMigrate:
		return store.MigrationScopeNotificationChat, true
	case serviceRoleBeneficiaryMigrate:
		return store.MigrationScopeConsumerBeneficiary, true
	case serviceRoleWalletLedgerMigrate:
		return store.MigrationScopeWalletLedger, true
	default:
		return "", false
	}
}
