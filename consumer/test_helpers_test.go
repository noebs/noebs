package consumer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/testdb"
	"github.com/adonese/noebs/store"
	"github.com/sirupsen/logrus"
)

type testEnv struct {
	Service *Service
	Auth    *gateway.JWTAuth
	Store   *store.Store
	DB      *store.DB
	Tenant  string
}

const testKafkaTransactionTopic = "test-ebs-transactions"

var (
	postgresOnce      sync.Once
	postgresContainer *testdb.PostgresContainer
	postgresErr       error
)

func ensurePostgresContainer(t *testing.T) *testdb.PostgresContainer {
	t.Helper()
	postgresOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		postgresContainer, postgresErr = testdb.StartPostgresContainer(ctx)
	})
	if postgresErr != nil {
		t.Fatalf("start postgres container: %v", postgresErr)
	}
	return postgresContainer
}

func newTestDB(t *testing.T) (*store.DB, *store.Store, string) {
	t.Helper()
	return newTestDBWithScopes(t, []string{
		store.MigrationScopeIdentityAuth,
		store.MigrationScopeCardVault,
		store.MigrationScopeEBSAdapter,
		store.MigrationScopeNotificationChat,
		store.MigrationScopeConsumerBeneficiary,
	})
}

func newTestDBWithScopes(t *testing.T, scopes []string) (*store.DB, *store.Store, string) {
	t.Helper()
	container := ensurePostgresContainer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer dropCancel()
		_ = container.DropDatabase(dropCtx, dbName)
	})
	tenantID := "test-tenant"
	if err := migrateConsumerTestScopes(ctx, db, tenantID, scopes); err != nil {
		t.Fatalf("migrate service scopes: %v", err)
	}
	storeSvc := store.New(db, store.WithDataKey("test-data-key"))
	if err := storeSvc.EnsureTenant(ctx, tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}
	return db, storeSvc, tenantID
}

func migrateConsumerTestScopes(ctx context.Context, db *store.DB, tenantID string, scopes []string) error {
	for _, scope := range scopes {
		if err := store.MigrateScope(ctx, db, tenantID, scope); err != nil {
			return fmt.Errorf("%s: %w", scope, err)
		}
	}
	return nil
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	db, storeSvc, tenantID := newTestDB(t)

	smsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(smsServer.Close)

	cfg := ebs_fields.NoebsConfig{
		JWTKey:                "test-secret",
		BillInquiryIPIN:       "0000",
		EBSConsumerKey:        "test-key",
		SMSGateway:            smsServer.URL + "?",
		SMSAPIKey:             "test-key",
		SMSSender:             "noebs",
		SMSMessage:            "test",
		DefaultTenantID:       tenantID,
		KafkaTransactionTopic: testKafkaTransactionTopic,
	}

	auth := &gateway.JWTAuth{NoebsConfig: cfg}
	auth.Init()

	logger := logrus.New()
	service := &Service{
		Store:       storeSvc,
		NoebsConfig: cfg,
		Logger:      logger,
		Auth:        auth,
	}

	return &testEnv{Service: service, Auth: auth, Store: storeSvc, DB: db, Tenant: tenantID}
}

func seedUser(t *testing.T, storeSvc *store.Store, tenantID, mobile, password string) ebs_fields.User {
	t.Helper()
	user := ebs_fields.User{
		Mobile:   mobile,
		Username: mobile,
		Password: password,
		Email:    mobile + "@example.com",
	}
	if err := user.HashPassword(); err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := storeSvc.CreateUser(context.Background(), tenantID, &user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}
