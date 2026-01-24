package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/dashboard"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/merchant"
	"github.com/adonese/noebs/store"
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
var dashService dashboard.Service
var merchantServices = merchant.Service{}
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

	if otelShutdown != nil {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), otelShutdownTimeout)
			defer cancel()
			if err := otelShutdown(ctx); err != nil {
				logrusLogger.WithError(err).Warn("otel shutdown failed")
			}
		}()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if hub != nil {
		go hub.Run()
	} else {
		logrusLogger.Warn("chat hub disabled (db unavailable)")
	}
	go consumerService.BillerHooks(ctx)
	go consumerService.Pusher(ctx)
	if noebsConfig.Port == "" {
		noebsConfig.Port = ":8080"
	}
	logrusLogger.Fatal(GetMainEngine().Listen(noebsConfig.Port))
}
