package handler

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
)

type registeredRoute struct {
	method string
	path   string
}

func assertRouteCatalogExact(t *testing.T, app *fiber.App, expected []registeredRoute) {
	t.Helper()
	want := make(map[string]bool, len(expected))
	for _, route := range expected {
		want[route.method+" "+route.path] = true
	}
	for _, route := range app.GetRoutes(true) {
		if route.Method == fiber.MethodHead {
			continue
		}
		key := route.Method + " " + route.Path
		if !want[key] {
			t.Errorf("unexpected registered route %s", key)
			continue
		}
		delete(want, key)
	}
	for route := range want {
		t.Errorf("missing registered route %s", route)
	}
}

func TestEBSAdapterRouteCatalogIsExact(t *testing.T) {
	app := fiber.New()
	RegisterEBSAdapterAuthedRoutes(app.Group("/consumer"), &Handler{})

	assertRouteCatalogExact(t, app, []registeredRoute{
		{method: http.MethodPost, path: "/consumer/cards/enrollment-intents"},
		{method: http.MethodPost, path: "/consumer/cards/enrollment-intents/:enrollment_id/confirm"},
		{method: http.MethodPost, path: "/consumer/balance"},
		{method: http.MethodPost, path: "/consumer/status"},
		{method: http.MethodPost, path: "/consumer/is_alive"},
		{method: http.MethodGet, path: "/consumer/biller"},
		{method: http.MethodPost, path: "/consumer/n/status"},
		{method: http.MethodGet, path: "/consumer/nec2name"},
		{method: http.MethodPost, path: "/consumer/generate_qr"},
		{method: http.MethodPost, path: "/consumer/qr_status"},
		{method: http.MethodPost, path: "/consumer/qr_refund"},
		{method: http.MethodPost, path: "/consumer/qr_complete"},
		{method: http.MethodGet, path: "/consumer/transaction"},
		{method: http.MethodGet, path: "/consumer/transactions"},
	})
}

func TestCardVaultRouteCatalogIsExact(t *testing.T) {
	app := fiber.New()
	handler := &Handler{}
	RegisterCardVaultAuthedRoutes(app.Group("/consumer"), handler)
	RegisterCardVaultInternalRoutes(app.Group("/internal/card-vault"), handler)
	RegisterCardVaultAdminInternalRoutes(app.Group("/internal/card-vault"), handler)

	assertRouteCatalogExact(t, app, []registeredRoute{
		{method: http.MethodGet, path: "/consumer/cards"},
		{method: http.MethodPatch, path: "/consumer/cards/:card_id"},
		{method: http.MethodDelete, path: "/consumer/cards/:card_id"},
		{method: http.MethodPut, path: "/consumer/cards/:card_id/main"},
		{method: http.MethodPost, path: "/internal/card-vault/enrollment-intents"},
		{method: http.MethodPost, path: "/internal/card-vault/enrollment-intents/begin"},
		{method: http.MethodPost, path: "/internal/card-vault/enrollment-intents/claim-rail"},
		{method: http.MethodPost, path: "/internal/card-vault/enrollment-intents/complete"},
		{method: http.MethodPost, path: "/internal/card-vault/enrollment-intents/fail"},
		{method: http.MethodPost, path: "/internal/card-vault/funded-operations/claim"},
	})
}
