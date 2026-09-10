package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/adonese/noebs/adminreporting"
	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/dashboard"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/eventing"
	"github.com/adonese/noebs/store"
	"github.com/adonese/noebs/wallet"
	walletinterop "github.com/adonese/noebs/wallet/interop"
	walletpsp "github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletworker "github.com/adonese/noebs/wallet/worker"
	"github.com/sirupsen/logrus"
	chat "github.com/tutipay/ws"
)

var noebsConfig ebs_fields.NoebsConfig
var logrusLogger = logrus.New()
var database *store.DB
var storeSvc *store.Store
var consumerService consumer.Service
var dataConfigs ebs_fields.Configs
var service consumer.Service
var adminReportingService adminreporting.Service
var dashService dashboard.Service
var walletService *wallet.Service
var pspWebhookStore *walletstore.Store
var walletWorker *walletworker.Runner
var interopWorker *walletinterop.Worker
var ebsEventPublisher *eventing.OutboxPublisher
var adminReportingProjector *eventing.AdminReportingProjector
var walletPSPRegistry *walletpsp.Registry
var walletPSPLoader *walletpsp.Loader
var rawSecrets map[string]interface{}
var hub *chat.Hub
var logSampling gateway.LogSamplingConfig
var otelShutdown func(context.Context) error
var otelEnabled bool

func main() {
	if err := runMain(); err != nil {
		logrusLogger.WithError(err).Error("service stopped")
		os.Exit(1)
	}
}

func runMain() error {
	if isRenderConfigCommand() {
		if err := renderConfigFiles(); err != nil {
			return fmt.Errorf("render config: %w", err)
		}
		return nil
	}
	if isValidateDeploymentCommand() {
		if err := validateDeploymentCommand(); err != nil {
			return fmt.Errorf("validate deployment: %w", err)
		}
		return nil
	}
	if isValidateKubernetesDeploymentCommand() {
		if err := validateKubernetesDeploymentCommand(); err != nil {
			return fmt.Errorf("validate kubernetes deployment: %w", err)
		}
		return nil
	}
	if isRenderKubernetesSecretsCommand() {
		if err := renderKubernetesSecretsCommand(); err != nil {
			return fmt.Errorf("render kubernetes secrets: %w", err)
		}
		return nil
	}
	if isRenderEdgeInternalTransportCommand() {
		if err := renderEdgeInternalTransportCommand(); err != nil {
			return fmt.Errorf("render edge internal transport: %w", err)
		}
		return nil
	}
	if isRenderKeycloakBootstrapSecretsCommand() {
		if err := renderKeycloakBootstrapSecretsCommand(); err != nil {
			return fmt.Errorf("render Keycloak bootstrap secrets: %w", err)
		}
		return nil
	}
	if isPrepareKubernetesReleaseCommand() {
		if err := prepareKubernetesReleaseCommand(); err != nil {
			return fmt.Errorf("prepare kubernetes release: %w", err)
		}
		return nil
	}
	if isReconcileKeycloakCommand() {
		if err := reconcileKeycloakCommand(); err != nil {
			return fmt.Errorf("reconcile Keycloak: %w", err)
		}
		return nil
	}
	if isAssignKeycloakMembershipsCommand() {
		if err := assignKeycloakMembershipsCommand(); err != nil {
			return fmt.Errorf("assign Keycloak memberships: %w", err)
		}
		return nil
	}
	if isLookupKeycloakSubjectCommand() {
		if err := lookupKeycloakSubjectCommand(); err != nil {
			return fmt.Errorf("lookup Keycloak subject: %w", err)
		}
		return nil
	}
	if isDeleteKeycloakBootstrapCommand() {
		if err := deleteKeycloakBootstrapCommand(); err != nil {
			return fmt.Errorf("delete Keycloak bootstrap client: %w", err)
		}
		return nil
	}
	if isEnsureTemporalNamespaceCommand() {
		if err := ensureTemporalNamespaceCommand(); err != nil {
			return fmt.Errorf("ensure Temporal namespace: %w", err)
		}
		return nil
	}
	if isInternalHealthcheckCommand() {
		if err := checkInternalHealth(); err != nil {
			return fmt.Errorf("internal healthcheck: %w", err)
		}
		return nil
	}
	role, err := currentServiceRole()
	if err != nil {
		return fmt.Errorf("runtime service role: %w", err)
	}
	if workloadAuthDatabase != nil {
		defer func() { _ = workloadAuthDatabase.Close() }()
	}

	if otelShutdown != nil {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), otelShutdownTimeout)
			defer cancel()
			if err := otelShutdown(ctx); err != nil {
				logrusLogger.WithError(err).Warn("otel shutdown failed")
			}
		}()
	}
	if role.runsMigrations() {
		logrusLogger.Print("migration service role completed")
		return nil
	}
	if role.cleansWorkloadAuthNonces() {
		if err := cleanupExpiredWorkloadNonces(context.Background()); err != nil {
			return fmt.Errorf("cleanup workload nonces: %w", err)
		}
		return nil
	}
	if role.cleansGatewayAuthSessions() {
		if err := cleanupExpiredGatewayAuth(context.Background()); err != nil {
			return fmt.Errorf("cleanup gateway authentication: %w", err)
		}
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return runService(ctx, role)
}

func runService(ctx context.Context, role serviceRole) error {
	if role == serviceRoleWalletWorker && noebsConfig.InteropTenant != "" {
		var err error
		interopWorker, err = walletinterop.NewWorker(ctx, walletService.Store, noebsConfig.InteropTenant, noebsConfig.InteropFSPID)
		if err != nil {
			return fmt.Errorf("configure interop worker: %w", err)
		}
		if _, err = interopWorker.Start(ctx); err != nil {
			return fmt.Errorf("start interop worker: %w", err)
		}
	}
	if role.startsBackgroundHealth() {
		if _, err := startBackgroundHealthServer(ctx, role, noebsConfig.Port); err != nil {
			return fmt.Errorf("start background health server: %w", err)
		}
	}

	if role == serviceRoleWalletLedger {
		if grpcServer == nil || grpcListener == nil {
			return fmt.Errorf("wallet-ledger role requires an initialized grpc server")
		}
		logrusLogger.Printf("grpc server listening on %s", grpcListener.Addr())
		return runGRPCServer(ctx, grpcServer, grpcListener, applicationShutdownTimeout)
	}
	if role == serviceRoleWalletWorker {
		if walletWorker == nil {
			return fmt.Errorf("wallet-worker role requires an initialized temporal worker")
		}
		<-ctx.Done()
		walletWorker.Stop()
		return nil
	}
	if role.startsEBSEventPublisher() {
		if ebsEventPublisher == nil {
			return fmt.Errorf("ebs-adapter-events role requires an initialized event publisher")
		}
		if err := ebsEventPublisher.Run(ctx); err != nil {
			return fmt.Errorf("run ebs-adapter-events: %w", err)
		}
		return nil
	}
	if role.startsAdminReportingProjector() {
		if adminReportingProjector == nil {
			return fmt.Errorf("admin-reporting-projector role requires an initialized projector")
		}
		if err := adminReportingProjector.Run(ctx); err != nil {
			return fmt.Errorf("run admin-reporting-projector: %w", err)
		}
		return nil
	}
	if !role.startsHTTP() {
		return fmt.Errorf("service role %s has no runnable process", role)
	}
	defer closeWalletLedgerPublicClient()

	if role.startsChat() && noebsConfig.ChatEnabled {
		if hub == nil {
			return fmt.Errorf("notification-chat role requires an initialized chat hub")
		}
		go hub.Run()
	}
	if noebsConfig.Port == "" {
		return fmt.Errorf("%s role requires port", role)
	}
	listener, err := net.Listen("tcp", noebsConfig.Port)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", noebsConfig.Port, err)
	}
	if internalTransportServerTLS != nil {
		listener = tls.NewListener(listener, internalTransportServerTLS.Clone())
	}
	return runHTTPServer(ctx, GetMainEngine(), listener, applicationShutdownTimeout)
}
