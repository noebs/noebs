package adminreporting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/testdb"
	"github.com/adonese/noebs/store"
)

func TestStoreTransactionProjectionUsesAdminReportingScope(t *testing.T) {
	startCtx, startCancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer startCancel()
	container, err := testdb.StartPostgresContainer(startCtx)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	ctx := context.Background()
	dbName := "admin_reporting_projection_" + time.Now().Format("20060102150405")
	dbURL, err := container.CreateDatabase(ctx, dbName)
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
	if err := store.MigrateScope(ctx, db, tenantID, store.MigrationScopeAdminReporting); err != nil {
		t.Fatalf("migrate admin-reporting scope: %v", err)
	}
	storeSvc := store.New(db)
	if err := storeSvc.EnsureTenant(ctx, tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
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
	err = service.StoreTransactionProjection(ctx, tenantID, TransactionProjectionCommand{
		Transaction: &ebs_fields.EBSResponse{
			UUID:            "projection-uuid",
			ResponseCode:    0,
			ResponseMessage: "Approved",
			TerminalID:      "terminal-b",
		},
	})
	if err != nil {
		t.Fatalf("update transaction projection: %v", err)
	}

	got, err := storeSvc.GetTransactionByUUID(ctx, tenantID, "projection-uuid")
	if err != nil {
		t.Fatalf("get projection: %v", err)
	}
	if got.TerminalID != "terminal-b" {
		t.Fatalf("projection terminal = %q, want terminal-b", got.TerminalID)
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
