package transactionauth_test

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

	"github.com/adonese/noebs/internal/testdb"
	"github.com/adonese/noebs/internal/transactionauth"
	"github.com/adonese/noebs/store"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresIntentClaimIsAtomicAcrossGatewayInstances(t *testing.T) {
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
	if _, err := migrationDB.ExecContext(ctx, `INSERT INTO tenants(id, name) VALUES ('alpha', 'Alpha')`); err != nil {
		_ = migrationDB.Close()
		t.Fatal(err)
	}
	assertTransactionCleanupAuthority(t, ctx, migrationDB)
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
	first, err := transactionauth.NewPostgresStore(firstDB)
	if err != nil {
		t.Fatal(err)
	}
	second, err := transactionauth.NewPostgresStore(secondDB)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, time.July, 20, 4, 0, 0, 0, time.UTC)
	binding := binding(7)
	intent := transactionauth.IntentRecord{
		IntentHash:       digest(1),
		BrowserStartHash: digest(2),
		Binding:          binding,
		CreatedAt:        now,
		ExpiresAt:        now.Add(10 * time.Minute),
	}
	if err := first.CreateIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	flow := transactionauth.NewFlowRecord{
		StateHash:    digest(3),
		BrowserHash:  digest(4),
		PKCEVerifier: envelope("flow-key", 5),
		NonceHash:    digest(6),
		CreatedAt:    now,
		ExpiresAt:    now.Add(5 * time.Minute),
	}
	started, err := first.StartFlow(ctx, intent.BrowserStartHash, flow, now)
	if err != nil {
		t.Fatal(err)
	}
	if started.IntentHash != intent.IntentHash || started.Binding != binding {
		t.Fatalf("started flow = %+v", started)
	}
	if _, err := second.StartFlow(ctx, intent.BrowserStartHash, flow, now); !errors.Is(err, transactionauth.ErrInvalidBrowserStart) {
		t.Fatalf("replayed browser start error = %v", err)
	}

	const attempts = 32
	start := make(chan struct{})
	var callbackWinners atomic.Int32
	var unexpected atomic.Int32
	var wait sync.WaitGroup
	for attempt := range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			store := first
			if attempt%2 == 1 {
				store = second
			}
			consumed, err := store.ConsumeFlow(ctx, flow.StateHash, flow.BrowserHash, now)
			if err == nil {
				if consumed.Binding != binding {
					unexpected.Add(1)
				}
				callbackWinners.Add(1)
				return
			}
			if !errors.Is(err, transactionauth.ErrInvalidFlow) {
				unexpected.Add(1)
			}
		}()
	}
	close(start)
	wait.Wait()
	if callbackWinners.Load() != 1 || unexpected.Load() != 0 {
		t.Fatalf("callback winners=%d unexpected=%d", callbackWinners.Load(), unexpected.Load())
	}
	if err := first.AuthorizeIntent(ctx, intent.IntentHash, now, now, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	wrong := binding
	wrong.RequestDigest = digest(99)
	if err := second.ClaimIntent(ctx, intent.IntentHash, wrong, now); !errors.Is(err, transactionauth.ErrIntentNotFound) {
		t.Fatalf("wrong binding claim error = %v", err)
	}

	start = make(chan struct{})
	var claimWinners atomic.Int32
	unexpected.Store(0)
	for attempt := range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			store := first
			if attempt%2 == 1 {
				store = second
			}
			err := store.ClaimIntent(ctx, intent.IntentHash, binding, now)
			if err == nil {
				claimWinners.Add(1)
				return
			}
			if !errors.Is(err, transactionauth.ErrIntentNotFound) {
				unexpected.Add(1)
			}
		}()
	}
	close(start)
	wait.Wait()
	if claimWinners.Load() != 1 || unexpected.Load() != 0 {
		t.Fatalf("claim winners=%d unexpected=%d", claimWinners.Load(), unexpected.Load())
	}

	t.Run("wrong browser binding preserves flow", func(t *testing.T) {
		intent := pendingIntent(20, now)
		if err := first.CreateIntent(ctx, intent); err != nil {
			t.Fatal(err)
		}
		flow := pendingFlow(23, now)
		if _, err := first.StartFlow(ctx, intent.BrowserStartHash, flow, now); err != nil {
			t.Fatal(err)
		}
		if _, err := second.ConsumeFlow(ctx, flow.StateHash, digest(99), now); !errors.Is(err, transactionauth.ErrInvalidFlow) {
			t.Fatalf("wrong browser binding error = %v", err)
		}
		if _, err := second.ConsumeFlow(ctx, flow.StateHash, flow.BrowserHash, now); err != nil {
			t.Fatalf("legitimate callback after mismatch: %v", err)
		}
	})

	t.Run("flow uniqueness rollback preserves browser start", func(t *testing.T) {
		firstIntent := pendingIntent(30, now)
		secondIntent := pendingIntent(40, now)
		if err := first.CreateIntent(ctx, firstIntent); err != nil {
			t.Fatal(err)
		}
		if err := first.CreateIntent(ctx, secondIntent); err != nil {
			t.Fatal(err)
		}
		firstFlow := pendingFlow(33, now)
		if _, err := first.StartFlow(ctx, firstIntent.BrowserStartHash, firstFlow, now); err != nil {
			t.Fatal(err)
		}
		conflict := pendingFlow(43, now)
		conflict.StateHash = firstFlow.StateHash
		if _, err := second.StartFlow(ctx, secondIntent.BrowserStartHash, conflict, now); !errors.Is(err, transactionauth.ErrInvalidBrowserStart) {
			t.Fatalf("state conflict error = %v", err)
		}
		if _, err := second.StartFlow(ctx, secondIntent.BrowserStartHash, pendingFlow(53, now), now); err != nil {
			t.Fatalf("browser start was consumed by rolled-back conflict: %v", err)
		}
	})

	t.Run("expiry boundaries fail closed", func(t *testing.T) {
		expiredStart := pendingIntent(60, now)
		expiredStart.ExpiresAt = now.Add(time.Second)
		if err := first.CreateIntent(ctx, expiredStart); err != nil {
			t.Fatal(err)
		}
		if _, err := first.StartFlow(ctx, expiredStart.BrowserStartHash, pendingFlow(63, now), expiredStart.ExpiresAt); !errors.Is(err, transactionauth.ErrInvalidBrowserStart) {
			t.Fatalf("expired start error = %v", err)
		}

		expiredCallback := pendingIntent(70, now)
		if err := first.CreateIntent(ctx, expiredCallback); err != nil {
			t.Fatal(err)
		}
		callbackFlow := pendingFlow(73, now)
		callbackFlow.ExpiresAt = now.Add(time.Second)
		if _, err := first.StartFlow(ctx, expiredCallback.BrowserStartHash, callbackFlow, now); err != nil {
			t.Fatal(err)
		}
		if _, err := first.ConsumeFlow(ctx, callbackFlow.StateHash, callbackFlow.BrowserHash, callbackFlow.ExpiresAt); !errors.Is(err, transactionauth.ErrInvalidFlow) {
			t.Fatalf("expired callback error = %v", err)
		}

		expiredClaim := pendingIntent(80, now)
		if err := first.CreateIntent(ctx, expiredClaim); err != nil {
			t.Fatal(err)
		}
		claimFlow := pendingFlow(83, now)
		if _, err := first.StartFlow(ctx, expiredClaim.BrowserStartHash, claimFlow, now); err != nil {
			t.Fatal(err)
		}
		if _, err := first.ConsumeFlow(ctx, claimFlow.StateHash, claimFlow.BrowserHash, now); err != nil {
			t.Fatal(err)
		}
		expiresAt := now.Add(time.Second)
		if err := first.AuthorizeIntent(ctx, expiredClaim.IntentHash, now, now, expiresAt); err != nil {
			t.Fatal(err)
		}
		if err := first.ClaimIntent(ctx, expiredClaim.IntentHash, expiredClaim.Binding, expiresAt); !errors.Is(err, transactionauth.ErrIntentNotFound) {
			t.Fatalf("expired claim error = %v", err)
		}
	})

	t.Run("cleanup role has least privilege and cascades flows", func(t *testing.T) {
		expired := pendingIntent(90, now)
		expired.ExpiresAt = now.Add(time.Second)
		if err := first.CreateIntent(ctx, expired); err != nil {
			t.Fatal(err)
		}
		flow := pendingFlow(93, now)
		if _, err := first.StartFlow(ctx, expired.BrowserStartHash, flow, now); err != nil {
			t.Fatal(err)
		}
		cleanupURL, err := postgres.DatabaseURLForRole(databaseName, "gateway_auth_cleanup")
		if err != nil {
			t.Fatal(err)
		}
		cleanupDB, err := sql.Open("pgx", cleanupURL)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cleanupDB.Close() })
		cleanup, err := transactionauth.NewPostgresStore(cleanupDB)
		if err != nil {
			t.Fatal(err)
		}
		if err := cleanup.CreateIntent(ctx, pendingIntent(100, now)); !errors.Is(err, transactionauth.ErrStoreUnavailable) {
			t.Fatalf("cleanup insert error = %v", err)
		}
		deleted, err := cleanup.DeleteExpired(ctx, expired.ExpiresAt)
		if err != nil || deleted < 1 {
			t.Fatalf("cleanup = %d, %v", deleted, err)
		}
		if _, err := first.ConsumeFlow(ctx, flow.StateHash, flow.BrowserHash, now); !errors.Is(err, transactionauth.ErrInvalidFlow) {
			t.Fatalf("cascaded flow error = %v", err)
		}
	})
}

func TestPostgresIntentExpiryAndMigrationAuthority(t *testing.T) {
	migration, err := os.ReadFile("../../store/migrations/postgres/gateway_auth/001_gateway_auth.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(migration)
	for _, required := range []string{
		"CREATE TABLE wallet_transaction_authorization_intents",
		"CREATE TABLE wallet_transaction_authorization_flows",
		"operation IN ('wallet.p2p', 'wallet.withdrawal')",
		"request_digest BYTEA NOT NULL",
		"authentication_time TIMESTAMPTZ",
		"ON DELETE CASCADE",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("gateway migration missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"wallet_pin",
		"wallet_user_2fa",
		"oauth_token",
		"access_token",
		"refresh_token",
		"wallet_transaction_authorization_flows_expiry_idx",
		"wallet_transaction_authorization_intents,\n    wallet_transaction_authorization_flows\nTO gateway_auth_cleanup",
		"GRANT ",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("gateway migration persists forbidden material %q", forbidden)
		}
	}
}

func assertTransactionCleanupAuthority(t testing.TB, ctx context.Context, db *store.DB) {
	t.Helper()
	var canDeleteIntent, canReadIntentExpiry, canReadIntentDigest bool
	if err := db.QueryRowContext(ctx, `
		SELECT
			has_table_privilege('gateway_auth_cleanup', 'public.wallet_transaction_authorization_intents', 'DELETE'),
			has_column_privilege('gateway_auth_cleanup', 'public.wallet_transaction_authorization_intents', 'expires_at', 'SELECT'),
			has_column_privilege('gateway_auth_cleanup', 'public.wallet_transaction_authorization_intents', 'request_digest', 'SELECT')
	`).Scan(&canDeleteIntent, &canReadIntentExpiry, &canReadIntentDigest); err != nil {
		t.Fatal(err)
	}
	if !canDeleteIntent || !canReadIntentExpiry || canReadIntentDigest {
		t.Fatalf("gateway cleanup intent authority = delete:%v expiry:%v digest:%v", canDeleteIntent, canReadIntentExpiry, canReadIntentDigest)
	}
}

func pendingIntent(fill byte, now time.Time) transactionauth.IntentRecord {
	return transactionauth.IntentRecord{
		IntentHash:       digest(fill),
		BrowserStartHash: digest(fill + 1),
		Binding:          binding(fill + 2),
		CreatedAt:        now,
		ExpiresAt:        now.Add(10 * time.Minute),
	}
}

func pendingFlow(fill byte, now time.Time) transactionauth.NewFlowRecord {
	return transactionauth.NewFlowRecord{
		StateHash:    digest(fill),
		BrowserHash:  digest(fill + 1),
		PKCEVerifier: envelope("flow-key", fill+2),
		NonceHash:    digest(fill + 3),
		CreatedAt:    now,
		ExpiresAt:    now.Add(5 * time.Minute),
	}
}

func binding(fill byte) transactionauth.Binding {
	return transactionauth.Binding{
		TenantID:       "alpha",
		Issuer:         "https://api.noebs.sd/auth/realms/noebs",
		Subject:        "subject-1",
		Operation:      transactionauth.OperationWalletP2P,
		RequestDigest:  digest(fill),
		IdempotencyKey: "transfer-1",
	}
}

func digest(fill byte) transactionauth.Digest {
	var value transactionauth.Digest
	for index := range value {
		value[index] = fill
	}
	return value
}

func envelope(keyID string, fill byte) transactionauth.Envelope {
	nonce := make([]byte, 12)
	ciphertext := make([]byte, 32)
	for index := range nonce {
		nonce[index] = fill
	}
	for index := range ciphertext {
		ciphertext[index] = fill
	}
	return transactionauth.Envelope{KeyID: keyID, Nonce: nonce, Ciphertext: ciphertext}
}
