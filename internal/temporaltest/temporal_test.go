package temporaltest

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.temporal.io/sdk/client"
)

func TestTemporalContainer(t *testing.T) {
	if os.Getenv("DOCKER_HOST") == "" && os.Getenv("XDG_RUNTIME_DIR") == "" {
		t.Skip("docker host not configured")
	}
	defer func() {
		if r := recover(); r != nil {
			t.Skipf("docker unavailable: %v", r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	nw, err := network.New(ctx, network.WithAttachable())
	if err != nil {
		t.Skipf("docker network unavailable: %v", err)
	}
	defer func() {
		_ = nw.Remove(ctx)
	}()

	pg, err := postgres.Run(ctx,
		"postgres:16",
		postgres.WithDatabase("temporal"),
		postgres.WithUsername("temporal"),
		postgres.WithPassword("temporal"),
		network.WithNetwork([]string{"temporal-postgres"}, nw),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Skipf("postgres container unavailable: %v", err)
	}
	defer func() {
		_ = pg.Terminate(ctx)
	}()

	req := testcontainers.ContainerRequest{
		Image:        "temporalio/auto-setup:1.23.1",
		ExposedPorts: []string{"7233/tcp"},
		Env: map[string]string{
			"DB":             "postgresql",
			"DB_PORT":        "5432",
			"POSTGRES_USER":  "temporal",
			"POSTGRES_PWD":   "temporal",
			"POSTGRES_SEEDS": "temporal-postgres",
		},
		Networks: []string{nw.Name},
		NetworkAliases: map[string][]string{
			nw.Name: {"temporal"},
		},
		WaitingFor: wait.ForListeningPort("7233/tcp").WithStartupTimeout(2 * time.Minute),
	}
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			t.Skipf("temporal container startup timed out: %v", err)
		}
		t.Skipf("temporal container unavailable: %v", err)
	}
	defer func() {
		_ = ctr.Terminate(ctx)
	}()

	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("temporal host: %v", err)
	}
	port, err := ctr.MappedPort(ctx, "7233/tcp")
	if err != nil {
		t.Fatalf("temporal port: %v", err)
	}

	addr := net.JoinHostPort(host, port.Port())
	c, err := client.Dial(client.Options{HostPort: addr, Namespace: "default"})
	if err != nil {
		t.Fatalf("temporal dial failed: %v", err)
	}
	c.Close()
}
