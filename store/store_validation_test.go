package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/testdb"
)

var (
	validationPostgresOnce sync.Once
	validationPostgres     *testdb.PostgresContainer
	validationPostgresErr  error
	validationDatabaseSeq  atomic.Uint64
)

func TestMain(m *testing.M) {
	code := m.Run()
	if validationPostgres != nil {
		_ = validationPostgres.Terminate(context.Background())
	}
	os.Exit(code)
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db := newValidationDB(t)
	return New(db, WithDataKey("test-data-key"))
}

func newTestStoreWithoutDataKey(t *testing.T) *Store {
	t.Helper()
	db := newValidationDB(t)
	return New(db)
}

func newValidationDB(t *testing.T) *DB {
	t.Helper()
	ctx := context.Background()
	validationPostgresOnce.Do(func() {
		validationPostgres, validationPostgresErr = testdb.StartPostgresContainer(ctx)
	})
	if validationPostgresErr != nil {
		t.Fatalf("start postgres: %v", validationPostgresErr)
	}
	databaseName := fmt.Sprintf("store_validation_%d", validationDatabaseSeq.Add(1))
	dbURL, err := validationPostgres.CreateDatabase(ctx, databaseName)
	if err != nil {
		t.Fatalf("create postgres database: %v", err)
	}
	db, err := OpenFromConfig(dbURL, DriverPostgres)
	if err != nil {
		t.Fatalf("open postgres database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_ = validationPostgres.DropDatabase(context.Background(), databaseName)
	})
	return db
}

func TestStore_EnsureTenant_MissingTenantID(t *testing.T) {
	s := newTestStore(t)
	err := s.EnsureTenant(context.Background(), "")
	if !errors.Is(err, ErrMissingTenantID) {
		t.Fatalf("expected ErrMissingTenantID, got %v", err)
	}
}

func TestStore_EnsureTenant_RejectsReservedTenantID(t *testing.T) {
	s := newTestStore(t)
	err := s.EnsureTenant(context.Background(), "default")
	if !errors.Is(err, ErrInvalidTenantID) {
		t.Fatalf("expected ErrInvalidTenantID, got %v", err)
	}
}

func TestValidateTenantID(t *testing.T) {
	got, err := ValidateTenantID(" tenant_1 ")
	if err != nil {
		t.Fatalf("ValidateTenantID() error = %v", err)
	}
	if got != "tenant_1" {
		t.Fatalf("tenantID = %q, want tenant_1", got)
	}
	for _, tenantID := range []string{"", "   "} {
		if _, err := ValidateTenantID(tenantID); !errors.Is(err, ErrMissingTenantID) {
			t.Fatalf("ValidateTenantID(%q) = %v, want ErrMissingTenantID", tenantID, err)
		}
	}
	for _, tenantID := range []string{"default", "Default", " default "} {
		if _, err := ValidateTenantID(tenantID); !errors.Is(err, ErrInvalidTenantID) {
			t.Fatalf("ValidateTenantID(%q) = %v, want ErrInvalidTenantID", tenantID, err)
		}
	}
}

func TestStore_CreateUser_MissingTenantID(t *testing.T) {
	s := newTestStore(t)
	err := s.CreateUser(context.Background(), "", &ebs_fields.User{Mobile: "0990000000"})
	if !errors.Is(err, ErrMissingTenantID) {
		t.Fatalf("expected ErrMissingTenantID, got %v", err)
	}
}

func TestStore_CreateUser_MissingUser(t *testing.T) {
	s := newTestStore(t)
	err := s.CreateUser(context.Background(), "t1", nil)
	if !errors.Is(err, ErrMissingUser) {
		t.Fatalf("expected ErrMissingUser, got %v", err)
	}
}

func TestStore_CreateToken_MissingTenantID(t *testing.T) {
	s := newTestStore(t)
	err := s.CreateToken(context.Background(), "", &ebs_fields.Token{UUID: "u1"})
	if !errors.Is(err, ErrMissingTenantID) {
		t.Fatalf("expected ErrMissingTenantID, got %v", err)
	}
}

func TestStore_CreateToken_MissingUUID(t *testing.T) {
	s := newTestStore(t)
	err := s.CreateToken(context.Background(), "t1", &ebs_fields.Token{})
	if !errors.Is(err, ErrMissingUUID) {
		t.Fatalf("expected ErrMissingUUID, got %v", err)
	}
}

func TestStore_AddCards_RequiresDataKey(t *testing.T) {
	s := newTestStoreWithoutDataKey(t)
	err := s.AddCards(context.Background(), "t1", 1, []ebs_fields.Card{{Pan: "9222081700000000", IPIN: "1234", Mobile: "0912141660"}})
	if !errors.Is(err, ErrMissingDataKey) {
		t.Fatalf("expected ErrMissingDataKey, got %v", err)
	}
}

func TestStore_AddCards_RequiresMobile(t *testing.T) {
	s := newTestStore(t)
	err := s.AddCards(context.Background(), "t1", 1, []ebs_fields.Card{{Pan: "9222081700000000"}})
	if !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("expected ErrMissingMobile, got %v", err)
	}
}

func TestStore_UpsertCacheCard_RequiresDataKey(t *testing.T) {
	s := newTestStoreWithoutDataKey(t)
	err := s.UpsertCacheCard(context.Background(), "t1", ebs_fields.CacheCards{Pan: "9222081700000000"})
	if !errors.Is(err, ErrMissingDataKey) {
		t.Fatalf("expected ErrMissingDataKey, got %v", err)
	}
}

func TestStore_CreateToken_RequiresDataKeyForDestinationPAN(t *testing.T) {
	s := newTestStoreWithoutDataKey(t)
	err := s.CreateToken(context.Background(), "t1", &ebs_fields.Token{UUID: "u1", ToCard: "9222081700000000"})
	if !errors.Is(err, ErrMissingDataKey) {
		t.Fatalf("expected ErrMissingDataKey, got %v", err)
	}
}

func TestStore_GetNotifications_MissingMobile(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetNotifications(context.Background(), "t1", "")
	if !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("expected ErrMissingMobile, got %v", err)
	}
}

func TestStore_MarkNotificationsRead_MissingMobile(t *testing.T) {
	s := newTestStore(t)
	err := s.MarkNotificationsRead(context.Background(), "t1", "")
	if !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("expected ErrMissingMobile, got %v", err)
	}
}
