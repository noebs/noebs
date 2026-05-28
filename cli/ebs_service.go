package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/adonese/noebs/adminreporting"
	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/dashboard"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/merchant"
	"github.com/adonese/noebs/store"
	"github.com/adonese/noebs/wallet"
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
var auth gateway.JWTAuth
var adminReportingService adminreporting.Service
var dashService dashboard.Service
var merchantServices = merchant.Service{}
var walletService *wallet.Service
var pspWebhookStore *walletstore.Store
var walletWorker *walletworker.Runner
var walletPSPRegistry *walletpsp.Registry
var walletPSPLoader *walletpsp.Loader
var rawSecrets map[string]interface{}
var hub *chat.Hub
var logSampling gateway.LogSamplingConfig
var otelShutdown func(context.Context) error
var otelEnabled bool

func main() {
	if isRenderConfigCommand() {
		if err := renderConfigFiles(); err != nil {
			logrusLogger.Fatalf("render config failed: %v", err)
		}
		return
	}
	if isRenderDatabasePasswordCommand() {
		if err := renderDatabasePasswordFile(); err != nil {
			logrusLogger.Fatalf("render database password failed: %v", err)
		}
		return
	}
	if isValidateDeploymentCommand() {
		if err := validateDeploymentCommand(); err != nil {
			logrusLogger.Fatalf("validate deployment failed: %v", err)
		}
		return
	}
	if isValidateKubernetesDeploymentCommand() {
		if err := validateKubernetesDeploymentCommand(); err != nil {
			logrusLogger.Fatalf("validate kubernetes deployment failed: %v", err)
		}
		return
	}
	if isRenderKubernetesSecretsCommand() {
		if err := renderKubernetesSecretsCommand(); err != nil {
			logrusLogger.Fatalf("render kubernetes secrets failed: %v", err)
		}
		return
	}
	role, err := currentServiceRole()
	if err != nil {
		logrusLogger.Fatalf("error in runtime service role: %v", err)
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
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if role.startsGRPC() && grpcServer != nil && grpcListener != nil {
		go func() {
			logrusLogger.Printf("grpc server listening on %s", grpcListener.Addr())
			if err := grpcServer.Serve(grpcListener); err != nil {
				logrusLogger.WithError(err).Error("grpc server stopped")
			}
		}()
	}
	go func() {
		<-ctx.Done()
		if grpcServer != nil {
			grpcServer.GracefulStop()
		}
	}()
	if role == serviceRoleWalletLedger {
		if grpcServer == nil || grpcListener == nil {
			logrusLogger.Fatal("wallet-ledger role requires an initialized grpc server")
		}
		<-ctx.Done()
		return
	}
	if role == serviceRoleWalletWorker {
		if walletWorker == nil {
			logrusLogger.Fatal("wallet-worker role requires an initialized temporal worker")
		}
		<-ctx.Done()
		walletWorker.Stop()
		return
	}
	if !role.startsHTTP() {
		logrusLogger.Fatalf("service role %s has no runnable process", role)
	}
	go func() {
		<-ctx.Done()
		closeWalletWorkflowClient()
		closeWalletLedgerPublicClient()
	}()

	if role.startsChat() {
		if hub == nil {
			logrusLogger.Fatal("notification-chat role requires an initialized chat hub")
		}
		go hub.Run()
	}
	if noebsConfig.Port == "" {
		logrusLogger.Fatalf("%s role requires port", role)
	}
	logrusLogger.Fatal(GetMainEngine().Listen(noebsConfig.Port))
}
