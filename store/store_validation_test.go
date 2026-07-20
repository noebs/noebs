package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/tenantcatalog"
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
		if testdb.IsContainerRuntimeUnavailable(validationPostgresErr) {
			t.Skipf("container runtime unavailable: %v", validationPostgresErr)
		}
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

func TestStoreProvisionTenantCatalogIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := newMigrationAuthorityDB(t, MigrationScopeIdentityAuth)
	if err := MigrateScope(ctx, db, MigrationScopeIdentityAuth); err != nil {
		t.Fatal(err)
	}
	catalog, err := tenantcatalog.New([]tenantcatalog.Tenant{
		{ID: "tenant-cutover", Name: "Tenant Cutover"},
		{ID: "tenant-sandbox", Name: "Tenant Sandbox"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := New(db)
	if err := store.ProvisionTenantCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	first := tenantCatalogSnapshot(t, ctx, db)
	if err := store.ProvisionTenantCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	if second := tenantCatalogSnapshot(t, ctx, db); !reflect.DeepEqual(second, first) {
		t.Fatalf("second provision changed rows: first=%#v second=%#v", first, second)
	}
	if _, err := db.ExecContext(ctx, "UPDATE tenants SET name = 'drifted' WHERE id = 'tenant-sandbox'"); err != nil {
		t.Fatal(err)
	}
	if err := store.ProvisionTenantCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	converged := tenantCatalogSnapshot(t, ctx, db)
	if err := store.ProvisionTenantCatalog(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	if steady := tenantCatalogSnapshot(t, ctx, db); !reflect.DeepEqual(steady, converged) {
		t.Fatalf("steady provision changed rows: converged=%#v steady=%#v", converged, steady)
	}
	want := []tenantCatalogRow{
		{ID: "tenant-cutover", Name: "Tenant Cutover", Version: converged[0].Version},
		{ID: "tenant-sandbox", Name: "Tenant Sandbox", Version: converged[1].Version},
	}
	if !reflect.DeepEqual(converged, want) {
		t.Fatalf("tenants = %#v, want %#v", converged, want)
	}
}

func TestStoreProvisionTenantCatalogRejectsExtraDatabaseTenantAtomically(t *testing.T) {
	ctx := context.Background()
	db := newMigrationAuthorityDB(t, MigrationScopeIdentityAuth)
	if err := MigrateScope(ctx, db, MigrationScopeIdentityAuth); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO tenants(id, name, created_at) VALUES ('tenant-extra', 'Extra Tenant', NOW())"); err != nil {
		t.Fatal(err)
	}
	catalog, err := tenantcatalog.New([]tenantcatalog.Tenant{
		{ID: "tenant-cutover", Name: "Tenant Cutover"},
		{ID: "tenant-sandbox", Name: "Tenant Sandbox"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := New(db).ProvisionTenantCatalog(ctx, catalog); !errors.Is(err, ErrTenantCatalogMismatch) {
		t.Fatalf("ProvisionTenantCatalog() error = %v, want ErrTenantCatalogMismatch", err)
	}
	rows := tenantCatalogSnapshot(t, ctx, db)
	if len(rows) != 1 || rows[0].ID != "tenant-extra" || rows[0].Name != "Extra Tenant" {
		t.Fatalf("failed provision was not atomic: %#v", rows)
	}
}

type tenantCatalogRow struct {
	ID      string `db:"id"`
	Name    string `db:"name"`
	Version string `db:"version"`
}

func tenantCatalogSnapshot(t testing.TB, ctx context.Context, db *DB) []tenantCatalogRow {
	t.Helper()
	var rows []tenantCatalogRow
	if err := db.SelectContext(ctx, &rows, "SELECT id, name, xmin::text AS version FROM tenants ORDER BY id"); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestValidateTenantID(t *testing.T) {
	got, err := ValidateTenantID("tenant-cutover")
	if err != nil {
		t.Fatalf("ValidateTenantID() error = %v", err)
	}
	if got != "tenant-cutover" {
		t.Fatalf("tenantID = %q, want tenant-cutover", got)
	}
	if _, err := ValidateTenantID(""); !errors.Is(err, ErrMissingTenantID) {
		t.Fatalf("ValidateTenantID(\"\") = %v, want ErrMissingTenantID", err)
	}
	for _, tenantID := range []string{"   ", " tenant-cutover ", "tenant_1", "Tenant-Cutover", "default", "Default", " default "} {
		if _, err := ValidateTenantID(tenantID); !errors.Is(err, ErrInvalidTenantID) {
			t.Fatalf("ValidateTenantID(%q) = %v, want ErrInvalidTenantID", tenantID, err)
		}
	}
}

func TestStore_CreateTransaction_MissingUUID(t *testing.T) {
	s := newTestStore(t)
	err := s.CreateTransaction(context.Background(), "t1", ebs_fields.EBSResponse{})
	if !errors.Is(err, ErrMissingUUID) {
		t.Fatalf("expected ErrMissingUUID, got %v", err)
	}
}

func TestStore_CreatePushDataRequiresExplicitFields(t *testing.T) {
	s := &Store{}
	ctx := context.Background()
	if err := s.CreatePushData(ctx, "t1", nil); !errors.Is(err, ErrMissingPushData) {
		t.Fatalf("CreatePushData(nil) error = %v, want %v", err, ErrMissingPushData)
	}
	if err := s.CreatePushData(ctx, "t1", &ebs_fields.PushDataRecord{UUID: " "}); !errors.Is(err, ErrMissingUUID) {
		t.Fatalf("CreatePushData(missing uuid) error = %v, want %v", err, ErrMissingUUID)
	}
	if err := s.CreatePushData(ctx, "t1", &ebs_fields.PushDataRecord{UUID: "push-uuid"}); !errors.Is(err, ErrMissingPushTarget) {
		t.Fatalf("CreatePushData(missing target) error = %v, want %v", err, ErrMissingPushTarget)
	}
}

type rowsAffectedResult int64

func (r rowsAffectedResult) LastInsertId() (int64, error) {
	return 0, nil
}

func (r rowsAffectedResult) RowsAffected() (int64, error) {
	return int64(r), nil
}

type execFunc func(context.Context, string, ...any) (sql.Result, error)

func (fn execFunc) ExecContext(ctx context.Context, stmt string, args ...any) (sql.Result, error) {
	return fn(ctx, stmt, args...)
}

func TestExecContextRequireRowsAffected(t *testing.T) {
	err := execContextRequireRowsAffected(context.Background(), execFunc(func(context.Context, string, ...any) (sql.Result, error) {
		return rowsAffectedResult(1), nil
	}), "UPDATE users SET language = 'en'")
	if err != nil {
		t.Fatalf("execContextRequireRowsAffected() error = %v", err)
	}

	err = execContextRequireRowsAffected(context.Background(), execFunc(func(context.Context, string, ...any) (sql.Result, error) {
		return rowsAffectedResult(0), nil
	}), "UPDATE users SET language = 'en'")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("zero rows error = %v, want %v", err, sql.ErrNoRows)
	}
}

func TestErrNotFoundOnlyMatchesNoRows(t *testing.T) {
	if !ErrNotFound(sql.ErrNoRows) {
		t.Fatalf("ErrNotFound(sql.ErrNoRows) = false, want true")
	}
	if ErrNotFound(nil) {
		t.Fatalf("ErrNotFound(nil) = true, want false")
	}
	if ErrNotFound(errors.New("database file not found")) {
		t.Fatalf("ErrNotFound(non-store error) = true, want false")
	}
}

func TestStore_TransactionParticipantQueriesRequireExplicitInputs(t *testing.T) {
	s := &Store{}
	ctx := context.Background()
	if _, err := s.GetTransactionsByParticipantUserID(ctx, "t1", 0); !errors.Is(err, ErrInvalidUserID) {
		t.Fatalf("missing user id error = %v, want %v", err, ErrInvalidUserID)
	}
	if _, err := s.GetTransactionByUUIDForParticipantUserID(ctx, "t1", 0, "transaction-uuid"); !errors.Is(err, ErrInvalidUserID) {
		t.Fatalf("missing user id error = %v, want %v", err, ErrInvalidUserID)
	}
	if _, err := s.GetTransactionByUUIDForParticipantUserID(ctx, "t1", 1, " "); !errors.Is(err, ErrMissingUUID) {
		t.Fatalf("missing uuid error = %v, want %v", err, ErrMissingUUID)
	}
}

func TestStore_CacheBillerRequiresExplicitFields(t *testing.T) {
	s := &Store{}
	if err := s.UpsertCacheBiller(context.Background(), "t1", " ", "0010010001"); !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("UpsertCacheBiller(missing mobile) error = %v, want %v", err, ErrMissingMobile)
	}
	if err := s.UpsertCacheBiller(context.Background(), "t1", "0912141660", " "); !errors.Is(err, ErrMissingBillerID) {
		t.Fatalf("UpsertCacheBiller(missing biller id) error = %v, want %v", err, ErrMissingBillerID)
	}
	if _, err := s.GetCacheBiller(context.Background(), "t1", " "); !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("GetCacheBiller(missing mobile) error = %v, want %v", err, ErrMissingMobile)
	}
}

func newIdentityAuthTestStore(t *testing.T, ctx context.Context) *Store {
	t.Helper()
	db := newMigrationAuthorityDB(t, MigrationScopeIdentityAuth)
	if err := MigrateScope(ctx, db, MigrationScopeIdentityAuth); err != nil {
		t.Fatalf("migrate identity-auth scope: %v", err)
	}
	return New(db)
}

func TestStore_CoreTenantValidationFailsBeforeDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{}
	cases := []struct {
		name string
		run  func(string) error
	}{
		{"UpsertCacheBiller", func(tenantID string) error {
			return s.UpsertCacheBiller(ctx, tenantID, "0990000000", "biller")
		}},
		{"GetCacheBiller", func(tenantID string) error {
			_, err := s.GetCacheBiller(ctx, tenantID, "0990000000")
			return err
		}},
		{"CreateTransaction", func(tenantID string) error {
			return s.CreateTransaction(ctx, tenantID, ebs_fields.EBSResponse{UUID: "transaction-uuid"})
		}},
		{"GetTransactionsByParticipantUserID", func(tenantID string) error {
			_, err := s.GetTransactionsByParticipantUserID(ctx, tenantID, 1)
			return err
		}},
		{"GetTransactionByUUID", func(tenantID string) error {
			_, err := s.GetTransactionByUUID(ctx, tenantID, "transaction-uuid")
			return err
		}},
		{"GetTransactionByUUIDForParticipantUserID", func(tenantID string) error {
			_, err := s.GetTransactionByUUIDForParticipantUserID(ctx, tenantID, 1, "transaction-uuid")
			return err
		}},
		{"CreatePushData", func(tenantID string) error {
			return s.CreatePushData(ctx, tenantID, &ebs_fields.PushDataRecord{UUID: "push-uuid"})
		}},
		{"GetMeterName", func(tenantID string) error {
			_, err := s.GetMeterName(ctx, tenantID, "nec")
			return err
		}},
		{"UpdatePaymentRequest", func(tenantID string) error {
			return s.UpdatePaymentRequest(ctx, tenantID, "push-uuid", ebs_fields.QrData{})
		}},
	}
	tenantCases := []struct {
		tenantID string
		wantErr  error
	}{
		{"", ErrMissingTenantID},
		{"default", ErrInvalidTenantID},
	}
	for _, tc := range cases {
		for _, tenantCase := range tenantCases {
			t.Run(tc.name+"/"+tenantCase.tenantID, func(t *testing.T) {
				err := tc.run(tenantCase.tenantID)
				if !errors.Is(err, tenantCase.wantErr) {
					t.Fatalf("expected %v, got %v", tenantCase.wantErr, err)
				}
			})
		}
	}
}

func TestStoreTargetedUpdatesReportMissingRows(t *testing.T) {
	ctx := context.Background()
	tenantID := "tenant-targeted-updates"
	identityDB := newMigrationAuthorityDB(t, MigrationScopeIdentityAuth)
	if err := MigrateScope(ctx, identityDB, MigrationScopeIdentityAuth); err != nil {
		t.Fatalf("migrate %s: %v", MigrationScopeIdentityAuth, err)
	}
	notificationDB := newMigrationAuthorityDB(t, MigrationScopeNotificationChat)
	if err := MigrateScope(ctx, notificationDB, MigrationScopeNotificationChat); err != nil {
		t.Fatalf("migrate %s: %v", MigrationScopeNotificationChat, err)
	}
	identityStore := New(identityDB, WithDataKey("test-data-key"))
	notificationStore := New(notificationDB, WithDataKey("test-data-key"))
	provisionTestTenant(t, ctx, identityStore, tenantID, "Missing User Tenant")
	provisionTestTenant(t, ctx, notificationStore, tenantID, "Missing User Tenant")
	fullname := "Missing User"
	tests := []struct {
		name string
		run  func() error
	}{
		{"UpdateProfileProjection", func() error {
			return identityStore.UpdateProfileProjection(ctx, tenantID, 999, ProfileProjectionUpdate{Fullname: &fullname})
		}},
		{"SetProfileDeviceToken", func() error {
			return identityStore.SetProfileDeviceToken(ctx, tenantID, 999, "device-token")
		}},
		{"UpdatePaymentRequest", func() error {
			return notificationStore.UpdatePaymentRequest(ctx, tenantID, "missing-push", ebs_fields.QrData{UUID: "payment-1"})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("%s error = %v, want %v", tt.name, err, sql.ErrNoRows)
			}
		})
	}
}
