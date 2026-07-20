package testdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/adonese/noebs/internal/postgresauthority"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var ErrContainerRuntimeUnavailable = errors.New("container runtime unavailable")

// PostgresContainer wraps a testcontainers postgres instance.
type PostgresContainer struct {
	container            *postgres.PostgresContainer
	adminURL             string
	clusterLock          *sql.Conn
	serviceDatabaseMu    sync.Mutex
	serviceDatabaseLocks map[string]*sql.Conn
}

// StartPostgresContainer boots a postgres container for tests.
func StartPostgresContainer(ctx context.Context) (pc *PostgresContainer, err error) {
	if adminURL := strings.TrimSpace(os.Getenv("NOEBS_TEST_POSTGRES_URL")); adminURL != "" {
		if _, err := url.ParseRequestURI(adminURL); err != nil {
			return nil, fmt.Errorf("invalid NOEBS_TEST_POSTGRES_URL: %w", err)
		}
		pc := &PostgresContainer{adminURL: adminURL}
		if err := pc.acquireSharedClusterLock(ctx); err != nil {
			return nil, err
		}
		if err := pc.ensureApplicationRoles(ctx); err != nil {
			pc.releaseSharedClusterLock(context.Background())
			return nil, err
		}
		return pc, nil
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
	pc = &PostgresContainer{container: container, adminURL: adminURL}
	if err := pc.ensureApplicationRoles(ctx); err != nil {
		_ = container.Terminate(ctx)
		return nil, err
	}
	return pc, nil
}

func (p *PostgresContainer) ensureApplicationRoles(ctx context.Context) error {
	admin, err := sql.Open("pgx", p.adminURL)
	if err != nil {
		return err
	}
	defer func() { _ = admin.Close() }()
	tx, err := admin.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(684398214)`); err != nil {
		return err
	}
	roles := postgresauthority.Roles()
	if err := resetManagedRoleMemberships(ctx, tx, roles); err != nil {
		return err
	}
	for _, role := range roles {
		if err := convergeTestRole(ctx, tx, role.Name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type roleMembership struct {
	granted string
	member  string
}

func resetManagedRoleMemberships(ctx context.Context, tx *sql.Tx, roles []postgresauthority.Role) error {
	managedRoles := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		managedRoles[role.Name] = struct{}{}
	}
	membershipRows, err := tx.QueryContext(ctx, `
		SELECT granted.rolname, member.rolname
		FROM pg_auth_members membership
		JOIN pg_roles granted ON granted.oid = membership.roleid
		JOIN pg_roles member ON member.oid = membership.member
	`)
	if err != nil {
		return err
	}
	var memberships []roleMembership
	for membershipRows.Next() {
		var edge roleMembership
		if err := membershipRows.Scan(&edge.granted, &edge.member); err != nil {
			_ = membershipRows.Close()
			return err
		}
		if _, managed := managedRoles[edge.granted]; managed {
			memberships = append(memberships, edge)
			continue
		}
		if _, managed := managedRoles[edge.member]; managed {
			memberships = append(memberships, edge)
		}
	}
	if err := membershipRows.Close(); err != nil {
		return err
	}
	for _, edge := range memberships {
		statement := "REVOKE " + quoteIdent(edge.granted) + " FROM " + quoteIdent(edge.member)
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("reset Postgres test role membership %s -> %s: %w", edge.granted, edge.member, err)
		}
	}
	return nil
}

func convergeTestRole(ctx context.Context, tx *sql.Tx, role string) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists); err != nil {
		return err
	}
	verb := "CREATE ROLE "
	if exists {
		verb = "ALTER ROLE "
	}
	attributes := " LOGIN PASSWORD " + quoteLiteral(testRolePassword(role)) +
		" NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS" +
		" CONNECTION LIMIT -1 VALID UNTIL 'infinity'"
	if _, err := tx.ExecContext(ctx, verb+quoteIdent(role)+attributes); err != nil {
		return fmt.Errorf("converge Postgres test role %s: %w", role, err)
	}
	if _, err := tx.ExecContext(ctx, "ALTER ROLE "+quoteIdent(role)+" RESET ALL"); err != nil {
		return fmt.Errorf("reset Postgres test role %s settings: %w", role, err)
	}
	return resetTestRoleDatabaseSettings(ctx, tx, role)
}

func resetTestRoleDatabaseSettings(ctx context.Context, tx *sql.Tx, role string) error {
	settingRows, err := tx.QueryContext(ctx, `
		SELECT database.datname
		FROM pg_db_role_setting setting
		JOIN pg_database database ON database.oid = setting.setdatabase
		JOIN pg_roles role ON role.oid = setting.setrole
		WHERE role.rolname = $1
	`, role)
	if err != nil {
		return err
	}
	var databases []string
	for settingRows.Next() {
		var database string
		if err := settingRows.Scan(&database); err != nil {
			_ = settingRows.Close()
			return err
		}
		databases = append(databases, database)
	}
	if err := settingRows.Close(); err != nil {
		return err
	}
	for _, database := range databases {
		statement := "ALTER ROLE " + quoteIdent(role) + " IN DATABASE " + quoteIdent(database) + " RESET ALL"
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("reset Postgres test role %s settings in %s: %w", role, database, err)
		}
	}
	return nil
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
	if p == nil {
		return nil
	}
	p.releaseServiceDatabaseLocks(ctx)
	p.releaseSharedClusterLock(ctx)
	if p.container == nil {
		return nil
	}
	return p.container.Terminate(ctx)
}

func (p *PostgresContainer) acquireSharedClusterLock(ctx context.Context) error {
	admin, err := sql.Open("pgx", p.adminURL)
	if err != nil {
		return err
	}
	conn, err := admin.Conn(ctx)
	_ = admin.Close()
	if err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtext($1))`, "noebs-shared-test-postgres"); err != nil {
		_ = conn.Close()
		return fmt.Errorf("acquire shared PostgreSQL test environment: %w", err)
	}
	p.clusterLock = conn
	return nil
}

func (p *PostgresContainer) releaseSharedClusterLock(ctx context.Context) {
	if p.clusterLock == nil {
		return
	}
	_, _ = p.clusterLock.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, "noebs-shared-test-postgres")
	_ = p.clusterLock.Close()
	p.clusterLock = nil
}

// CreateDatabase creates a new database and returns its connection URL.
func (p *PostgresContainer) CreateDatabase(ctx context.Context, name string) (string, error) {
	return p.createDatabase(ctx, name, "")
}

// CreateDatabaseForRole creates a canonical service database owned by its
// migration role and returns an actual role-authenticated connection URL.
func (p *PostgresContainer) CreateDatabaseForRole(ctx context.Context, name, role string) (string, error) {
	if p == nil {
		return "", fmt.Errorf("postgres container is nil")
	}
	if role == "" {
		return "", fmt.Errorf("database owner role is required")
	}
	migrationRole, ok := postgresauthority.MigrationRole(name)
	if !ok {
		return "", fmt.Errorf("%q is not a service database", name)
	}
	if migrationRole.Name != role {
		return "", fmt.Errorf("database %q requires migration role %q, got %q", name, migrationRole.Name, role)
	}
	lock, err := p.acquireServiceDatabaseLock(ctx, name)
	if err != nil {
		return "", err
	}
	release := true
	defer func() {
		if release {
			p.releaseServiceDatabaseLock(context.Background(), name, lock)
		}
	}()
	if err := p.dropDatabase(ctx, name); err != nil {
		return "", fmt.Errorf("reset service database %s: %w", name, err)
	}
	databaseURL, err := p.createDatabase(ctx, name, role)
	if err != nil {
		return "", err
	}
	p.serviceDatabaseMu.Lock()
	if p.serviceDatabaseLocks == nil {
		p.serviceDatabaseLocks = make(map[string]*sql.Conn)
	}
	p.serviceDatabaseLocks[name] = lock
	p.serviceDatabaseMu.Unlock()
	release = false
	return databaseURL, nil
}

// DatabaseURLForRole returns an authenticated URL for an execution role in an
// already acquired canonical service database.
func (p *PostgresContainer) DatabaseURLForRole(name, role string) (string, error) {
	if p == nil {
		return "", fmt.Errorf("postgres container is nil")
	}
	roleMatchesDatabase := false
	for _, candidate := range postgresauthority.Roles() {
		if candidate.Name == role && candidate.Database == name {
			roleMatchesDatabase = true
			break
		}
	}
	if !roleMatchesDatabase {
		return "", fmt.Errorf("role %q is not an execution role for database %q", role, name)
	}
	databaseURL, err := p.databaseURL(name)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(databaseURL)
	if err != nil {
		return "", err
	}
	u.User = url.UserPassword(role, testRolePassword(role))
	return u.String(), nil
}

func (p *PostgresContainer) createDatabase(ctx context.Context, name, role string) (string, error) {
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
	defer func() { _ = admin.Close() }()

	stmt := fmt.Sprintf("CREATE DATABASE %s", quoteIdent(name))
	if role != "" {
		stmt += " OWNER " + quoteIdent(role)
	}
	if _, err := admin.ExecContext(ctx, stmt); err != nil {
		return "", err
	}
	databaseURL, err := p.databaseURL(name)
	if err != nil || role == "" {
		return databaseURL, err
	}
	databaseAdmin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return "", err
	}
	defer func() { _ = databaseAdmin.Close() }()
	if _, err := databaseAdmin.ExecContext(ctx, "ALTER SCHEMA public OWNER TO "+quoteIdent(role)); err != nil {
		return "", fmt.Errorf("set %s public schema owner: %w", name, err)
	}
	u, err := url.Parse(databaseURL)
	if err != nil {
		return "", err
	}
	u.User = url.UserPassword(role, testRolePassword(role))
	return u.String(), nil
}

// DropDatabase drops a database if it exists.
func (p *PostgresContainer) DropDatabase(ctx context.Context, name string) error {
	if p == nil || name == "" {
		return nil
	}
	p.serviceDatabaseMu.Lock()
	lock := p.serviceDatabaseLocks[name]
	delete(p.serviceDatabaseLocks, name)
	p.serviceDatabaseMu.Unlock()
	if lock != nil {
		defer p.releaseServiceDatabaseLock(context.Background(), name, lock)
	}
	return p.dropDatabase(ctx, name)
}

func (p *PostgresContainer) dropDatabase(ctx context.Context, name string) error {
	admin, err := sql.Open("pgx", p.adminURL)
	if err != nil {
		return err
	}
	defer func() { _ = admin.Close() }()

	stmt := fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", quoteIdent(name))
	_, err = admin.ExecContext(ctx, stmt)
	return err
}

func (p *PostgresContainer) acquireServiceDatabaseLock(ctx context.Context, name string) (*sql.Conn, error) {
	admin, err := sql.Open("pgx", p.adminURL)
	if err != nil {
		return nil, err
	}
	conn, err := admin.Conn(ctx)
	_ = admin.Close()
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtext($1))`, "noebs-test-service-database:"+name); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("acquire %s test database: %w", name, err)
	}
	return conn, nil
}

func (p *PostgresContainer) releaseServiceDatabaseLock(ctx context.Context, name string, conn *sql.Conn) {
	if conn == nil {
		return
	}
	_, _ = conn.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, "noebs-test-service-database:"+name)
	_ = conn.Close()
}

func (p *PostgresContainer) releaseServiceDatabaseLocks(ctx context.Context) {
	p.serviceDatabaseMu.Lock()
	locks := p.serviceDatabaseLocks
	p.serviceDatabaseLocks = nil
	p.serviceDatabaseMu.Unlock()
	for name, lock := range locks {
		p.releaseServiceDatabaseLock(ctx, name, lock)
	}
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

func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func testRolePassword(role string) string {
	digest := sha256.Sum256([]byte("noebs-test-postgres-role:" + role))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func postgresAdminURL(host string, port string) string {
	port, _, _ = strings.Cut(port, "/")
	return fmt.Sprintf("postgres://noebs:noebs@%s/postgres?sslmode=disable", net.JoinHostPort(host, port))
}
