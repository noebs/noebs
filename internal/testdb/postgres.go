package testdb

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// PostgresContainer wraps a testcontainers postgres instance.
type PostgresContainer struct {
	container *postgres.PostgresContainer
	adminURL  string
}

// StartPostgresContainer boots a postgres container for tests.
func StartPostgresContainer(ctx context.Context) (pc *PostgresContainer, err error) {
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
		testcontainers.WithWaitStrategy(
			wait.ForSQL("5432/tcp", "pgx", postgresAdminURL).WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		return nil, err
	}
	adminURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}
	return &PostgresContainer{container: container, adminURL: adminURL}, nil
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
