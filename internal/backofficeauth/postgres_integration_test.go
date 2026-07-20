package backofficeauth_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/backofficeauth"
	"github.com/adonese/noebs/internal/testdb"
	"github.com/adonese/noebs/store"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresStoreConsumesFlowsAndSerializesRefreshAcrossInstances(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	postgres, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgres.Terminate(context.Background()) })
	const databaseName = "gateway_auth"
	migrationURL, err := postgres.CreateDatabaseForRole(ctx, databaseName, "gateway_auth_migrate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgres.DropDatabase(context.Background(), databaseName) })
	migrationDB, err := store.OpenFromConfig(migrationURL, store.DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateScope(ctx, migrationDB, store.MigrationScopeGatewayAuth); err != nil {
		_ = migrationDB.Close()
		t.Fatal(err)
	}
	assertBackofficeCleanupAuthority(t, ctx, migrationDB)
	if err := migrationDB.Close(); err != nil {
		t.Fatal(err)
	}
	databaseURL, err := postgres.DatabaseURLForRole(databaseName, "gateway_auth_runtime")
	if err != nil {
		t.Fatal(err)
	}
	firstDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = firstDB.Close() })
	secondDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondDB.Close() })
	first, err := backofficeauth.NewPostgresStore(firstDB)
	if err != nil {
		t.Fatal(err)
	}
	second, err := backofficeauth.NewPostgresStore(secondDB)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	clock := staticClock{now: now}
	flow := backofficeauth.FlowRecord{
		StateHash:    digest(1),
		BrowserHash:  digest(2),
		PKCEVerifier: envelope("flow-key", 3),
		NonceHash:    digest(4),
		ReturnPath:   "/backoffice/t/acme/wallet",
		CreatedAt:    now,
		ExpiresAt:    now.Add(5 * time.Minute),
	}
	if err := first.CreateFlow(ctx, flow); err != nil {
		t.Fatal(err)
	}
	const attempts = 32
	start := make(chan struct{})
	var flowSuccesses atomic.Int32
	var flowFailures atomic.Int32
	var wg sync.WaitGroup
	for attempt := 0; attempt < attempts; attempt++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			store := first
			if index%2 == 1 {
				store = second
			}
			_, err := store.ConsumeFlow(ctx, flow.StateHash, flow.BrowserHash, now)
			if err == nil {
				flowSuccesses.Add(1)
				return
			}
			if !errors.Is(err, backofficeauth.ErrInvalidFlow) {
				flowFailures.Add(1)
			}
		}(attempt)
	}
	close(start)
	wg.Wait()
	if flowSuccesses.Load() != 1 || flowFailures.Load() != 0 {
		t.Fatalf("flow successes=%d unexpected failures=%d", flowSuccesses.Load(), flowFailures.Load())
	}

	sessionCreatedAt := now.Add(-10 * time.Minute)
	session := backofficeauth.SessionRecord{
		SessionHash:       digest(5),
		Issuer:            "https://api.noebs.sd/auth/realms/noebs",
		Subject:           "operator-1",
		Tokens:            envelope("session-key", 6),
		AccessExpiresAt:   now.Add(-time.Minute),
		RefreshExpiresAt:  now.Add(30 * time.Minute),
		IdleExpiresAt:     now.Add(30 * time.Minute),
		AbsoluteExpiresAt: now.Add(8 * time.Hour),
		LastSeenAt:        sessionCreatedAt,
		CreatedAt:         sessionCreatedAt,
		UpdatedAt:         sessionCreatedAt,
	}
	if err := first.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	start = make(chan struct{})
	var refreshCalls atomic.Int32
	var refreshFailures atomic.Int32
	for attempt := 0; attempt < attempts; attempt++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			store := first
			if index%2 == 1 {
				store = second
			}
			refreshed, err := store.RefreshSession(
				ctx,
				session.SessionHash,
				clock,
				time.Minute,
				func(context.Context, backofficeauth.SessionRecord) (backofficeauth.SessionRefresh, error) {
					refreshCalls.Add(1)
					return backofficeauth.SessionRefresh{
						Tokens:           envelope("session-key", 7),
						AccessExpiresAt:  now.Add(10 * time.Minute),
						RefreshExpiresAt: now.Add(30 * time.Minute),
					}, nil
				},
			)
			if err != nil || !refreshed.AccessExpiresAt.Equal(now.Add(10*time.Minute)) {
				refreshFailures.Add(1)
			}
		}(attempt)
	}
	close(start)
	wg.Wait()
	if refreshCalls.Load() != 1 || refreshFailures.Load() != 0 {
		t.Fatalf("refresh calls=%d failures=%d", refreshCalls.Load(), refreshFailures.Load())
	}

	touchSession := session
	touchSession.SessionHash = digest(12)
	touchSession.AccessExpiresAt = now.Add(time.Hour)
	touchSession.IdleExpiresAt = now.Add(10 * time.Second)
	if err := first.CreateSession(ctx, touchSession); err != nil {
		t.Fatal(err)
	}
	start = make(chan struct{})
	var touchFailures atomic.Int32
	for attempt := 0; attempt < attempts; attempt++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			store := first
			if index%2 == 1 {
				store = second
			}
			current, err := store.TouchSession(
				ctx,
				touchSession.SessionHash,
				now,
				now.Add(30*time.Minute),
				now.Add(-time.Minute),
			)
			if err != nil || !current.LastSeenAt.Equal(now) || !current.IdleExpiresAt.Equal(now.Add(30*time.Minute)) {
				touchFailures.Add(1)
			}
		}(attempt)
	}
	close(start)
	wg.Wait()
	if touchFailures.Load() != 0 {
		t.Fatalf("concurrent touch failures=%d", touchFailures.Load())
	}
	absoluteExpired := session
	absoluteExpired.SessionHash = digest(13)
	absoluteExpired.IdleExpiresAt = now.Add(-time.Minute)
	absoluteExpired.AbsoluteExpiresAt = now.Add(-time.Minute)
	if err := first.CreateSession(ctx, absoluteExpired); err != nil {
		t.Fatal(err)
	}
	_, err = second.TouchSession(
		ctx,
		absoluteExpired.SessionHash,
		now,
		absoluteExpired.AbsoluteExpiresAt,
		now.Add(-time.Minute),
	)
	if !errors.Is(err, backofficeauth.ErrSessionExpired) {
		t.Fatalf("absolute-expired touch error = %v", err)
	}
	if _, err := first.LoadSession(ctx, absoluteExpired.SessionHash); !errors.Is(err, backofficeauth.ErrSessionNotFound) {
		t.Fatalf("absolute-expired session load error = %v", err)
	}

	deadlineSession := session
	deadlineSession.SessionHash = digest(10)
	deadlineSession.RefreshExpiresAt = now.Add(time.Minute)
	if err := first.CreateSession(ctx, deadlineSession); err != nil {
		t.Fatal(err)
	}
	deadlineClock := &mutableClock{now: now}
	_, err = second.RefreshSession(ctx, deadlineSession.SessionHash, deadlineClock, time.Minute,
		func(context.Context, backofficeauth.SessionRecord) (backofficeauth.SessionRefresh, error) {
			deadlineClock.Set(now.Add(2 * time.Minute))
			return backofficeauth.SessionRefresh{
				Tokens:           envelope("session-key", 11),
				AccessExpiresAt:  now.Add(10 * time.Minute),
				RefreshExpiresAt: now.Add(10 * time.Minute),
			}, nil
		})
	if !errors.Is(err, backofficeauth.ErrSessionExpired) {
		t.Fatalf("refresh crossing stored deadline error = %v", err)
	}
	if _, err := first.LoadSession(ctx, deadlineSession.SessionHash); !errors.Is(err, backofficeauth.ErrSessionNotFound) {
		t.Fatalf("deadline-crossed session load error = %v", err)
	}

	revoked := session
	revoked.SessionHash = digest(8)
	if err := first.CreateSession(ctx, revoked); err != nil {
		t.Fatal(err)
	}
	_, err = second.RefreshSession(ctx, revoked.SessionHash, clock, time.Minute,
		func(context.Context, backofficeauth.SessionRecord) (backofficeauth.SessionRefresh, error) {
			return backofficeauth.SessionRefresh{}, backofficeauth.ErrSessionRevoked
		})
	if !errors.Is(err, backofficeauth.ErrSessionRevoked) {
		t.Fatalf("revoked refresh error = %v", err)
	}
	if _, err := first.LoadSession(ctx, revoked.SessionHash); !errors.Is(err, backofficeauth.ErrSessionNotFound) {
		t.Fatalf("revoked session load error = %v", err)
	}

	refreshExpired := session
	refreshExpired.SessionHash = digest(9)
	refreshExpired.RefreshExpiresAt = now.Add(-time.Minute)
	if err := first.CreateSession(ctx, refreshExpired); err != nil {
		t.Fatal(err)
	}
	cleanup, err := first.DeleteExpired(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup.Flows != 0 || cleanup.Sessions != 1 {
		t.Fatalf("cleanup result = %+v, want 0 flows and 1 session", cleanup)
	}
	if _, err := second.LoadSession(ctx, refreshExpired.SessionHash); !errors.Is(err, backofficeauth.ErrSessionNotFound) {
		t.Fatalf("refresh-expired session load error = %v", err)
	}
}

func TestGatewayAuthMigrationKeepsRefreshExpiryAsCleanupBoundary(t *testing.T) {
	migration, err := os.ReadFile("../../store/migrations/postgres/gateway_auth/001_gateway_auth.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(migration)
	for _, required := range []string{
		"CREATE INDEX backoffice_sessions_refresh_expiry_idx",
		"browser_hash BYTEA NOT NULL CHECK",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration missing %q", required)
		}
	}
	for _, removed := range []string{"keycloak_session", "browser_hash BYTEA NOT NULL UNIQUE", "GRANT "} {
		if strings.Contains(sql, removed) {
			t.Fatalf("migration retained %q", removed)
		}
	}
}

type staticClock struct {
	now time.Time
}

func (c staticClock) Now() time.Time { return c.now }

type mutableClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *mutableClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

func assertBackofficeCleanupAuthority(t testing.TB, ctx context.Context, db *store.DB) {
	t.Helper()
	var canDeleteFlow, canReadFlowExpiry, canReadFlowState bool
	var canDeleteSession, canReadSessionExpiry, canReadSessionToken bool
	if err := db.QueryRowContext(ctx, `
		SELECT
			has_table_privilege('gateway_auth_cleanup', 'public.backoffice_auth_flows', 'DELETE'),
			has_column_privilege('gateway_auth_cleanup', 'public.backoffice_auth_flows', 'expires_at', 'SELECT'),
			has_column_privilege('gateway_auth_cleanup', 'public.backoffice_auth_flows', 'state_hash', 'SELECT'),
			has_table_privilege('gateway_auth_cleanup', 'public.backoffice_sessions', 'DELETE'),
			has_column_privilege('gateway_auth_cleanup', 'public.backoffice_sessions', 'refresh_expires_at', 'SELECT'),
			has_column_privilege('gateway_auth_cleanup', 'public.backoffice_sessions', 'tokens_ciphertext', 'SELECT')
	`).Scan(
		&canDeleteFlow,
		&canReadFlowExpiry,
		&canReadFlowState,
		&canDeleteSession,
		&canReadSessionExpiry,
		&canReadSessionToken,
	); err != nil {
		t.Fatal(err)
	}
	if !canDeleteFlow || !canReadFlowExpiry || canReadFlowState || !canDeleteSession || !canReadSessionExpiry || canReadSessionToken {
		t.Fatalf(
			"gateway cleanup authority = flow(delete:%v expiry:%v state:%v) session(delete:%v expiry:%v token:%v)",
			canDeleteFlow,
			canReadFlowExpiry,
			canReadFlowState,
			canDeleteSession,
			canReadSessionExpiry,
			canReadSessionToken,
		)
	}
}

func digest(fill byte) backofficeauth.Digest {
	var value backofficeauth.Digest
	for index := range value {
		value[index] = fill
	}
	return value
}

func envelope(keyID string, fill byte) backofficeauth.Envelope {
	nonce := make([]byte, 12)
	ciphertext := make([]byte, 32)
	for index := range nonce {
		nonce[index] = fill
	}
	for index := range ciphertext {
		ciphertext[index] = fill
	}
	return backofficeauth.Envelope{KeyID: keyID, Nonce: nonce, Ciphertext: ciphertext}
}
