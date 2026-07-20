package adminreporting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/adonese/noebs/internal/testdb"
	"github.com/adonese/noebs/store"
)

func TestStoreTransactionProjectionUsesAdminReportingScope(t *testing.T) {
	startCtx, startCancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer startCancel()
	container, err := testdb.StartPostgresContainer(startCtx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	ctx := context.Background()
	const dbName = "admin_reporting"
	dbURL, err := container.CreateDatabaseForRole(ctx, dbName, "admin_reporting_migrate")
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		_ = container.DropDatabase(context.Background(), dbName)
	})

	db, err := store.OpenFromConfig(dbURL, store.DriverPostgres)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tenantID := "tenant-a"
	if err := store.MigrateScope(ctx, db, store.MigrationScopeAdminReporting); err != nil {
		t.Fatalf("migrate admin-reporting scope: %v", err)
	}
	storeSvc := store.New(db)
	catalog, err := tenantcatalog.New([]tenantcatalog.Tenant{{ID: tenantcatalog.ID(tenantID), Name: "Admin Reporting Tenant"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := storeSvc.ProvisionTenantCatalog(ctx, catalog); err != nil {
		t.Fatalf("provision tenant: %v", err)
	}

	service := &Service{Store: storeSvc}
	err = service.StoreTransactionProjection(ctx, tenantID, TransactionProjectionCommand{
		Transaction: &ebs_fields.EBSResponse{
			UUID:            "projection-uuid",
			ResponseCode:    0,
			ResponseMessage: "Approved",
			TerminalID:      "terminal-a",
		},
	})
	if err != nil {
		t.Fatalf("store transaction projection: %v", err)
	}
	if err := service.StoreTransactionProjection(ctx, tenantID, TransactionProjectionCommand{
		Transaction: &ebs_fields.EBSResponse{
			UUID:            "projection-uuid",
			ResponseCode:    0,
			ResponseMessage: "Approved",
			TerminalID:      "terminal-a",
		},
	}); err != nil {
		t.Fatalf("exact projection replay: %v", err)
	}
	err = service.StoreTransactionProjection(ctx, tenantID, TransactionProjectionCommand{
		Transaction: &ebs_fields.EBSResponse{
			UUID:            "projection-uuid",
			ResponseCode:    0,
			ResponseMessage: "Approved",
			TerminalID:      "terminal-b",
		},
	})
	if !errors.Is(err, store.ErrDuplicateTransaction) {
		t.Fatalf("mismatched projection replay error = %v, want %v", err, store.ErrDuplicateTransaction)
	}

	got, err := storeSvc.GetTransactionByUUID(ctx, tenantID, "projection-uuid")
	if err != nil {
		t.Fatalf("get projection: %v", err)
	}
	if got.TerminalID != "terminal-a" {
		t.Fatalf("projection terminal = %q, want terminal-a", got.TerminalID)
	}
}

func TestStoreTransactionProjectionRejectsMissingInputs(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	if err := service.StoreTransactionProjection(context.Background(), "", TransactionProjectionCommand{Transaction: &ebs_fields.EBSResponse{}}); !errors.Is(err, store.ErrMissingTenantID) {
		t.Fatalf("missing tenant error = %v, want %v", err, store.ErrMissingTenantID)
	}
	if err := service.StoreTransactionProjection(context.Background(), "default", TransactionProjectionCommand{Transaction: &ebs_fields.EBSResponse{}}); !errors.Is(err, store.ErrInvalidTenantID) {
		t.Fatalf("reserved tenant error = %v, want %v", err, store.ErrInvalidTenantID)
	}
	if err := service.StoreTransactionProjection(context.Background(), "tenant-a", TransactionProjectionCommand{}); !errors.Is(err, ErrMissingTransactionProjection) {
		t.Fatalf("missing projection error = %v, want %v", err, ErrMissingTransactionProjection)
	}
	if err := service.StoreTransactionProjection(context.Background(), "tenant-a", TransactionProjectionCommand{Transaction: &ebs_fields.EBSResponse{}}); !errors.Is(err, store.ErrMissingUUID) {
		t.Fatalf("missing projection uuid error = %v, want %v", err, store.ErrMissingUUID)
	}
}
