package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"gopkg.in/yaml.v3"
)

func TestIdentityRuntimeRequiresItsOwnTemporalClient(t *testing.T) {
	cfg := identityAuthRuntimeConfig()
	for _, clientID := range []string{"", temporalWorkerClientID, temporalLedgerClientID, temporalIdentityWorkerClientID} {
		cfg.TemporalClientID = clientID
		if err := validateRoleRuntimeConfig(serviceRoleIdentityAuth, cfg); !errors.Is(err, errInvalidTemporalRuntime) {
			t.Fatalf("identity client %q error=%v", clientID, err)
		}
	}
	cfg = identityAuthRuntimeConfig()
	cfg.TemporalEnabled = false
	if err := validateRoleRuntimeConfig(serviceRoleIdentityAuth, cfg); !errors.Is(err, errTemporalNotEnabled) {
		t.Fatalf("disabled Temporal=%v", err)
	}
}

func TestStatusKafkaRuntimeRequiresExplicitSourceAndConsumerSettings(t *testing.T) {
	base := ebs_fields.NoebsConfig{KafkaBrokers: []string{"kafka:9092"}, KafkaStatusTopic: "noebs.status.changed.v1", StatusEventBatchSize: 100, StatusEventPollIntervalMs: 1000, StatusNotificationConsumerGroup: "notifications"}
	for _, role := range []serviceRole{serviceRoleWalletWorker, serviceRoleNotification} {
		if err := validateKafkaRuntimeConfig(role, base); err != nil {
			t.Fatalf("%s valid status config=%v", role, err)
		}
		for _, change := range []func(*ebs_fields.NoebsConfig){func(c *ebs_fields.NoebsConfig) { c.KafkaBrokers = nil }, func(c *ebs_fields.NoebsConfig) { c.KafkaStatusTopic = "" }} {
			cfg := base
			change(&cfg)
			before := cfg
			if err := validateKafkaRuntimeConfig(role, cfg); !errors.Is(err, errMissingKafkaConfig) {
				t.Fatalf("%s missing status config=%v", role, err)
			}
			if !reflect.DeepEqual(before, cfg) {
				t.Fatal("validation mutated input")
			}
		}
	}
	for _, change := range []func(*ebs_fields.NoebsConfig){func(c *ebs_fields.NoebsConfig) { c.StatusEventBatchSize = 0 }, func(c *ebs_fields.NoebsConfig) { c.StatusEventPollIntervalMs = 0 }} {
		cfg := base
		change(&cfg)
		if err := validateKafkaRuntimeConfig(serviceRoleWalletWorker, cfg); !errors.Is(err, errMissingKafkaConfig) {
			t.Fatalf("missing publisher setting=%v", err)
		}
	}
	cfg := base
	cfg.StatusNotificationConsumerGroup = ""
	if err := validateKafkaRuntimeConfig(serviceRoleNotification, cfg); !errors.Is(err, errMissingKafkaConfig) {
		t.Fatalf("missing consumer group=%v", err)
	}
}

func TestIdentityWorkerDependenciesUseWorkerClientAndKafka(t *testing.T) {
	cfg := identityAuthRuntimeConfig()
	cfg.ServiceRole = string(serviceRoleIdentityAuth)
	cfg.Port = ":8080"
	cfg.TemporalClientID = temporalIdentityWorkerClientID
	cfg.KafkaBrokers = []string{"kafka:9092"}
	cfg.KafkaStatusTopic = "status"
	cfg.StatusEventBatchSize = 10
	cfg.StatusEventPollIntervalMs = 1000
	if err := validateIdentityWorkerDependencies(cfg); err != nil {
		t.Fatal(err)
	}
	cfg.TemporalClientID = temporalIdentityClientID
	if err := validateIdentityWorkerDependencies(cfg); !errors.Is(err, errInvalidTemporalRuntime) {
		t.Fatalf("worker accepted API client=%v", err)
	}
	cfg.TemporalClientID = temporalIdentityWorkerClientID
	cfg.KafkaBrokers = nil
	if err := validateIdentityWorkerDependencies(cfg); !errors.Is(err, errMissingKafkaConfig) {
		t.Fatalf("worker accepted missing Kafka=%v", err)
	}
}

func TestDockerIdentityWorkerAndStatusConsumersHaveReadyDependencies(t *testing.T) {
	compose := decodeComposeDocument(t, filepath.Join("..", "docker-compose.yml"))
	for _, service := range []string{"identity-worker", "wallet-worker", "notification-chat"} {
		requireComposeDependency(t, service, compose.Services[service], "kafka-topics", "service_completed_successfully")
	}
	for _, service := range []string{"identity-worker", "identity-auth"} {
		requireComposeDependency(t, service, compose.Services[service], "temporal-namespace-bootstrap", "service_completed_successfully")
		requireComposeNetworks(t, service, compose.Services[service].Networks, "backend", "temporal-control")
	}
	worker := compose.Services["identity-worker"]
	requireComposeSecret(t, "identity-worker", worker.Secrets, "identity-worker-secrets", "/app/secrets.yaml")
	requireComposeVolume(t, "identity-worker", worker.Volumes, "./deploy/docker/services/identity-worker.yaml", "/app/service.yaml")
	var commands struct {
		Services map[string]struct {
			Command []string `yaml:"command"`
		} `yaml:"services"`
	}
	raw, err := os.ReadFile(filepath.Join("..", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err = yaml.Unmarshal(raw, &commands); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(commands.Services["identity-worker"].Command, []string{"identity-worker"}) {
		t.Fatal("worker command missing")
	}
	entrypoint, err := os.ReadFile(filepath.Join("..", "scripts", "entrypoint.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entrypoint), `exec noebs "$@"`) {
		t.Fatal("entrypoint discards worker command")
	}
}
