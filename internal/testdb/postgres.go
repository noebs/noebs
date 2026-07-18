package testdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var ErrContainerRuntimeUnavailable = errors.New("container runtime unavailable")

// PostgresContainer wraps a testcontainers postgres instance.
type PostgresContainer struct {
	container *postgres.PostgresContainer
	adminURL  string
}

// StartPostgresContainer boots a postgres container for tests.
func StartPostgresContainer(ctx context.Context) (pc *PostgresContainer, err error) {
	if adminURL := strings.TrimSpace(os.Getenv("NOEBS_TEST_POSTGRES_URL")); adminURL != "" {
		if _, err := url.ParseRequestURI(adminURL); err != nil {
			return nil, fmt.Errorf("invalid NOEBS_TEST_POSTGRES_URL: %w", err)
		}
		return &PostgresContainer{adminURL: adminURL}, nil
	}
	defer func() {
		if r := recover(); r != nil {
			pc = nil
			err = fmt.Errorf("testcontainers panic: %v", r)
		}
	}()
	container, err := postgres.Run(
		ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("postgres"),
		postgres.WithUsername("noebs"),
		postgres.WithPassword("noebs"),
		testcontainers.WithWaitStrategyAndDeadline(4*time.Minute,
			wait.ForSQL("5432/tcp", "pgx", postgresAdminURL).WithStartupTimeout(4*time.Minute),
		),
	)
	if err != nil {
		return nil, WrapContainerRuntimeError(err)
	}
	adminURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}
	return &PostgresContainer{container: container, adminURL: adminURL}, nil
}

func WrapContainerRuntimeError(err error) error {
	if err == nil || errors.Is(err, ErrContainerRuntimeUnavailable) {
		return err
	}
	if IsContainerRuntimeUnavailable(err) {
		return fmt.Errorf("%w: %v", ErrContainerRuntimeUnavailable, err)
	}
	return err
}

func IsContainerRuntimeUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrContainerRuntimeUnavailable) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "get provider") ||
		strings.Contains(message, "docker provider") ||
		strings.Contains(message, "docker daemon") ||
		strings.Contains(message, "cannot connect to docker") ||
		strings.Contains(message, "podman socket")
}

// Terminate stops and removes the container.
func (p *PostgresContainer) Terminate(ctx context.Context) error {
	if p == nil || p.container == nil {
		return nil
	}
	return p.container.Terminate(ctx)
}

// CreateDatabase creates a new database and returns its connection URL.
func (p *PostgresContainer) CreateDatabase(ctx context.Context, name string) (string, error) {
	if p == nil {
		return "", fmt.Errorf("postgres container is nil")
	}
	if name == "" {
		return "", fmt.Errorf("database name is required")
	}
	admin, err := sql.Open("pgx", p.adminURL)
	if err != nil {
		return "", err
	}
	defer admin.Close()

	stmt := fmt.Sprintf("CREATE DATABASE %s", quoteIdent(name))
	if _, err := admin.ExecContext(ctx, stmt); err != nil {
		return "", err
	}
	return p.databaseURL(name)
}

// DropDatabase drops a database if it exists.
func (p *PostgresContainer) DropDatabase(ctx context.Context, name string) error {
	if p == nil || name == "" {
		return nil
	}
	admin, err := sql.Open("pgx", p.adminURL)
	if err != nil {
		return err
	}
	defer admin.Close()

	stmt := fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", quoteIdent(name))
	_, err = admin.ExecContext(ctx, stmt)
	return err
}

func (p *PostgresContainer) databaseURL(name string) (string, error) {
	u, err := url.Parse(p.adminURL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + name
	return u.String(), nil
}

func quoteIdent(value string) string {
	return "\"" + strings.ReplaceAll(value, "\"", "\"\"") + "\""
}

func postgresAdminURL(host string, port string) string {
	port, _, _ = strings.Cut(port, "/")
	return fmt.Sprintf("postgres://noebs:noebs@%s/postgres?sslmode=disable", net.JoinHostPort(host, port))
}
