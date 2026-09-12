package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/eventing"
	"github.com/adonese/noebs/internal/verification"
	"github.com/adonese/noebs/store"
	walletworker "github.com/adonese/noebs/wallet/worker"
	"github.com/gofiber/fiber/v2"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"golang.org/x/sync/errgroup"
)

const temporalIdentityClientID = "noebs-temporal-identity-auth"
const temporalIdentityWorkerClientID = "noebs-temporal-identity-worker"

var identityTemporalClient client.Client

func isIdentityWorkerCommand() bool { return len(os.Args) > 1 && os.Args[1] == "identity-worker" }

func initIdentityWorkflow(ctx context.Context, cfg ebs_fields.NoebsConfig) error {
	opts, err := buildTemporalOptions(context.Background(), cfg, walletworker.TaskQueue(verification.TaskQueue), temporalIdentityClientID)
	if err != nil {
		return err
	}
	connection, err := walletworker.NewClient(ctx, opts)
	if err != nil {
		return err
	}
	identityTemporalClient = connection
	consumerService.IdentityWorkflow = &verification.Client{Temporal: connection, Sessions: consumerService.Store}
	return nil
}

func runIdentityWorker() error {
	if len(os.Args) != 2 {
		return errors.New("identity-worker takes its configuration from config, service and secrets YAML")
	}
	payload, err := loadConfig()
	if err != nil {
		return err
	}
	var cfg ebs_fields.NoebsConfig
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return err
	}
	if err := validateIdentityWorkerDependencies(cfg); err != nil {
		return err
	}
	if cfg.ServiceRole != string(serviceRoleIdentityAuth) {
		return errors.New("identity-worker requires identity-auth service database configuration")
	}
	if cfg.Port == "" {
		return errors.New("identity-worker health port is required")
	}
	spec, _ := postgresRoleSpecForService(serviceRoleIdentityAuth)
	if err := validatePostgresDatabaseIdentity(cfg.DatabaseURL, spec); err != nil {
		return err
	}
	if err := store.ValidateDatabaseTLSConfig(cfg.DatabaseURL, cfg.DatabaseCACertificate); err != nil {
		return err
	}
	db, err := openPostgresDatabaseWithAuthority(cfg.DatabaseURL, cfg.DatabaseDriver, cfg.DatabaseCACertificate, spec)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	opts, err := buildTemporalOptions(ctx, cfg, walletworker.TaskQueue(verification.TaskQueue), temporalIdentityWorkerClientID)
	if err != nil {
		return err
	}
	s := store.New(db)
	runner, err := walletworker.NewRunner(ctx, opts, func(w worker.Worker) error { verification.Register(w, s); return nil })
	if err != nil {
		return err
	}
	defer runner.Stop()
	if err := runner.Start(); err != nil {
		return err
	}
	writer, err := eventing.NewKafkaWriter(cfg.KafkaBrokers, cfg.KafkaStatusTopic)
	if err != nil {
		return err
	}
	publisher := &eventing.OutboxPublisher{Store: &store.IdentityStatusOutbox{Store: s, Topic: cfg.KafkaStatusTopic}, Writer: writer, Topic: cfg.KafkaStatusTopic, BatchSize: cfg.StatusEventBatchSize, PollInterval: time.Duration(cfg.StatusEventPollIntervalMs) * time.Millisecond}
	if err := publisher.Validate(); err != nil {
		writer.Close()
		return err
	}
	listener, err := net.Listen("tcp", cfg.Port)
	if err != nil {
		writer.Close()
		return err
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/test", func(c *fiber.Ctx) error {
		checkCtx, cancel := context.WithTimeout(c.UserContext(), time.Second)
		defer cancel()
		if err := db.PingContext(checkCtx); err != nil {
			return c.SendStatus(503)
		}
		if _, err := runner.Client.CheckHealth(checkCtx, &client.CheckHealthRequest{}); err != nil {
			return c.SendStatus(503)
		}
		return c.JSON(fiber.Map{"message": true})
	})
	group, runCtx := errgroup.WithContext(ctx)
	group.Go(func() error { return publisher.Run(runCtx) })
	group.Go(func() error { return runHTTPServer(runCtx, app, listener, applicationShutdownTimeout) })
	return group.Wait()
}

func newStatusNotificationConsumer(cfg ebs_fields.NoebsConfig, s *store.Store) (*eventing.StatusNotificationConsumer, error) {
	if s == nil {
		return nil, fmt.Errorf("status notification store is required")
	}
	reader, err := eventing.NewKafkaReader(cfg.KafkaBrokers, cfg.KafkaStatusTopic, cfg.StatusNotificationConsumerGroup)
	if err != nil {
		return nil, err
	}
	return &eventing.StatusNotificationConsumer{Reader: reader, Store: s, Topic: cfg.KafkaStatusTopic}, nil
}
