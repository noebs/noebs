package adminreporting

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/testdb"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
)

func TestStoreTransactionProjectionUsesAdminReportingScope(t *testing.T) {
	startCtx, startCancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	if err := service.StoreTransactionProjection(context.Background(), "tenant-a", TransactionProjectionCommand{}); !errors.Is(err, ErrMissingTransactionProjection) {
		t.Fatalf("missing projection error = %v, want %v", err, ErrMissingTransactionProjection)
	}
}

func TestResolveTenantIDUsesGatewayTenantHeader(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		tenantID, err := resolveTenantID(c)
		if err != nil {
			t.Fatalf("resolveTenantID() error = %v", err)
		}
		if tenantID != "tenant-a" {
			t.Fatalf("tenantID = %q, want tenant-a", tenantID)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(gateway.GatewayTenantIDHeader, " tenant-a ")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	_ = resp.Body.Close()
}

func TestResolveTenantIDIgnoresPublicTenantHeader(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		if _, err := resolveTenantID(c); !errors.Is(err, store.ErrMissingTenantID) {
			t.Fatalf("resolveTenantID() error = %v, want %v", err, store.ErrMissingTenantID)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "tenant-a")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	_ = resp.Body.Close()
}
