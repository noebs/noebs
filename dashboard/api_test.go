package dashboard

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
)

func TestService_calculateOffset(t *testing.T) {
	type fields struct {
		Store *store.Store
	}
	type args struct {
		page     int
		pageSize int
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   uint
	}{
		{"calculateOffset", fields{}, args{page: 0, pageSize: 10}, 0},
		{"calculateOffset", fields{}, args{page: 1, pageSize: 10}, 0},
		{"calculateOffset", fields{}, args{page: 2, pageSize: 10}, 10},
		{"calculateOffset", fields{}, args{page: 3, pageSize: 10}, 20},
		{"calculateOffset", fields{}, args{page: 4, pageSize: 10}, 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Service{
				Store: tt.fields.Store,
			}
			if got := s.calculateOffset(tt.args.page, tt.args.pageSize); got != tt.want {
				t.Errorf("Service.calculateOffset() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveTenantIDDoesNotDefaultFromServiceConfig(t *testing.T) {
	s := Service{
		NoebsConfig: ebs_fields.NoebsConfig{DefaultTenantID: "test-tenant"},
	}

	_, err := s.resolveTenantID(nil)
	if !errors.Is(err, store.ErrMissingTenantID) {
		t.Fatalf("resolveTenantID(nil) error = %v, want %v", err, store.ErrMissingTenantID)
	}
}

func TestResolveTenantIDUsesValidatedTenantIdentity(t *testing.T) {
	app := fiber.New()
	service := Service{}
	app.Get("/", gateway.InternalTenantIdentityMiddleware(), func(c *fiber.Ctx) error {
		tenantID, err := service.resolveTenantID(c)
		if err != nil {
			t.Fatalf("resolveTenantID() error = %v", err)
		}
		if tenantID != "tenant_1" {
			t.Fatalf("tenantID = %q, want tenant_1", tenantID)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(gateway.GatewayTenantIDHeader, " tenant_1 ")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	_ = resp.Body.Close()
}

func TestResolveTenantIDDoesNotReadGatewayTenantHeaderDirectly(t *testing.T) {
	app := fiber.New()
	service := Service{}
	app.Get("/", func(c *fiber.Ctx) error {
		if _, err := service.resolveTenantID(c); !errors.Is(err, store.ErrMissingTenantID) {
			t.Fatalf("resolveTenantID() error = %v, want %v", err, store.ErrMissingTenantID)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(gateway.GatewayTenantIDHeader, "tenant_1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	_ = resp.Body.Close()
}

func TestResolveTenantIDIgnoresPublicTenantHeader(t *testing.T) {
	app := fiber.New()
	service := Service{}
	app.Get("/", func(c *fiber.Ctx) error {
		if _, err := service.resolveTenantID(c); !errors.Is(err, store.ErrMissingTenantID) {
			t.Fatalf("resolveTenantID() error = %v, want %v", err, store.ErrMissingTenantID)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "tenant_1")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	_ = resp.Body.Close()
}
