package consumer

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/adonese/noebs/internal/testdb"
	"github.com/adonese/noebs/store"
)

const testKafkaTransactionTopic = "test-ebs-transactions"

var (
	postgresOnce      sync.Once
	postgresContainer *testdb.PostgresContainer
	postgresErr       error
)

func ensurePostgresContainer(t *testing.T) *testdb.PostgresContainer {
	t.Helper()
	postgresOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		postgresContainer, postgresErr = testdb.StartPostgresContainer(ctx)
	})
	if postgresErr != nil {
		if testdb.IsContainerRuntimeUnavailable(postgresErr) {
			t.Skipf("container runtime unavailable: %v", postgresErr)
		}
		t.Fatalf("start postgres container: %v", postgresErr)
	}
	return postgresContainer
}

func newTestDBWithScopes(t *testing.T, scopes []string) (*store.DB, *store.Store, string) {
	t.Helper()
	container := ensurePostgresContainer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	dbName := fmt.Sprintf("noebs_consumer_%d", time.Now().UnixNano())
	dbURL, err := container.CreateDatabase(ctx, dbName)
	if err != nil {
		t.Fatalf("create test db: %v", err)
	}
	db, err := store.OpenFromConfig(dbURL, "postgres")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dropCancel()
		_ = container.DropDatabase(dropCtx, dbName)
	})
	tenantID := "test-tenant"
	if err := migrateConsumerTestScopes(ctx, db, tenantID, scopes); err != nil {
		t.Fatalf("migrate service scopes: %v", err)
	}
	storeSvc := store.New(db, store.WithDataKey("test-data-key"))
	catalog, err := tenantcatalog.New([]tenantcatalog.Tenant{{ID: tenantcatalog.ID(tenantID), Name: "Consumer Test Tenant"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := storeSvc.ProvisionTenantCatalog(ctx, catalog); err != nil {
		t.Fatalf("provision tenant: %v", err)
	}
	return db, storeSvc, tenantID
}

func migrateConsumerTestScopes(ctx context.Context, db *store.DB, tenantID string, scopes []string) error {
	for _, scope := range scopes {
		if err := store.MigrateScope(ctx, db, scope); err != nil {
			return fmt.Errorf("%s: %w", scope, err)
		}
	}
	return nil
}

func transactionActorContext(t *testing.T, userID int64) context.Context {
	t.Helper()
	ctx, err := WithTransactionActor(context.Background(), userID)
	if err != nil {
		t.Fatalf("bind transaction actor %d: %v", userID, err)
	}
	return ctx
}

func noConsumerTransactionContext() context.Context {
	return WithNoConsumerTransactionParticipants(context.Background())
}

func testHTTPClient() *http.Client {
	return &http.Client{Timeout: 2 * time.Second}
}

func seedProfile(t *testing.T, storeSvc *store.Store, tenantID, identitySeed string) store.ProfileProjection {
	t.Helper()
	profile, err := storeSvc.CreateProfileProjection(context.Background(), store.CreateProfileProjectionParams{
		PrincipalIdentity: store.PrincipalIdentity{
			TenantID: tenantID,
			Issuer:   "https://identity.example/realms/noebs",
			Subject:  "test:" + identitySeed,
		},
		Fullname: "Test User",
		Username: identitySeed,
		Email:    identitySeed + "@example.com",
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	return profile
}
