package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type cardVaultRoute struct {
	name   string
	method string
	path   string
}

func cardVaultSteadyRoutes() []cardVaultRoute {
	return []cardVaultRoute{
		{name: "card list", method: http.MethodGet, path: "/consumer/get_cards"},
		{name: "add card", method: http.MethodPost, path: "/consumer/add_card"},
		{name: "edit card", method: http.MethodPut, path: "/consumer/edit_card"},
		{name: "delete card", method: http.MethodDelete, path: "/consumer/delete_card"},
		{name: "main card", method: http.MethodPost, path: "/consumer/cards/set_main"},
		{name: "get payment token", method: http.MethodGet, path: "/consumer/payment_token"},
		{name: "create payment token", method: http.MethodPost, path: "/consumer/payment_token"},
		{name: "payment request", method: http.MethodPost, path: "/consumer/payment_request"},
	}
}

func cardVaultTransitionalRoutes() []cardVaultRoute {
	return nil
}

func TestCardVaultRoutesAreProxiedByAPIGateway(t *testing.T) {
	ensureInit()
	configureGatewayProxyForTest(t)
	authorization := testAuthorizationHeader(t)
	route := GetMainEngine()

	tests := append(cardVaultSteadyRoutes(), cardVaultTransitionalRoutes()...)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Authorization", authorization)
			resp, err := route.Test(req)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			assertGatewayProxied(t, resp)
		})
	}
}

func TestCardVaultSteadyRoutesAreOwnedByCardVault(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleCardVault)
	route := GetMainEngine()

	for _, tt := range cardVaultSteadyRoutes() {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			setTestGatewayUserIdentityHeaders(req)
			resp, err := route.Test(req)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("read response body: %v", err)
				}
				if strings.Contains(string(body), "Cannot "+tt.method+" "+tt.path) {
					t.Fatalf("card-vault did not register %s", tt.path)
				}
			}
		})
	}
}

func TestCardVaultTransitionalRoutesStayVisibleUntilSplit(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleCardVault)
	route := GetMainEngine()

	for _, tt := range cardVaultTransitionalRoutes() {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			setTestGatewayUserIdentityHeaders(req)
			resp, err := route.Test(req)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			assertFiberRouteRegistered(t, resp, tt.method, tt.path)
		})
	}
}

func TestCardVaultSteadyRoutesExcludeTransitionalRoutes(t *testing.T) {
	steady := map[string]bool{}
	for _, route := range cardVaultSteadyRoutes() {
		steady[route.method+" "+route.path] = true
	}
	for _, route := range cardVaultTransitionalRoutes() {
		key := route.method + " " + route.path
		if steady[key] {
			t.Fatalf("%s must stay transitional until service-to-service commands replace mixed ownership", key)
		}
	}
}

func TestCardVaultDoesNotExposePublicMobilePANLookup(t *testing.T) {
	for _, spec := range gatewayProxyRouteSpecs() {
		if spec.path == "/consumer/users/cards" || spec.path == "/consumer/mobile2pan" {
			t.Fatalf("%s must not be proxied as a public route; use internal card-vault commands", spec.path)
		}
	}

	ensureInit()
	setServiceRoleForTest(t, serviceRoleCardVault)
	route := GetMainEngine()

	for _, tt := range []cardVaultRoute{
		{name: "cards by mobile", method: http.MethodGet, path: "/consumer/users/cards"},
		{name: "mobile to pan", method: http.MethodGet, path: "/consumer/mobile2pan"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			setTestGatewayUserIdentityHeaders(req)
			resp, err := route.Test(req)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
			}
		})
	}
}

func TestCardVaultDoesNotOwnIdentityEBSOrNotificationRoutes(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleCardVault)
	route := GetMainEngine()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "login", method: http.MethodPost, path: "/consumer/login"},
		{name: "profile", method: http.MethodGet, path: "/consumer/auth/me"},
		{name: "balance", method: http.MethodPost, path: "/consumer/balance"},
		{name: "transactions", method: http.MethodGet, path: "/consumer/transactions"},
		{name: "notifications", method: http.MethodGet, path: "/consumer/notifications"},
		{name: "ebs card info", method: http.MethodPost, path: "/consumer/card_info"},
		{name: "quick pay execution", method: http.MethodPost, path: "/consumer/payment_token/quick_pay"},
		{name: "ebs card registration start", method: http.MethodPost, path: "/consumer/cards/new"},
		{name: "ebs card registration completion", method: http.MethodPost, path: "/consumer/cards/complete"},
		{name: "ebs meter lookup", method: http.MethodGet, path: "/consumer/nec2name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			setTestGatewayUserIdentityHeaders(req)
			resp, err := route.Test(req)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
			}
		})
	}
}

func TestCardVaultOwnsQuickPayInternalCommands(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleCardVault)
	route := GetMainEngine()

	tests := []cardVaultRoute{
		{name: "resolve quick pay token", method: http.MethodPost, path: "/internal/card-vault/quick-pay/resolve"},
		{name: "mark quick pay token paid", method: http.MethodPost, path: "/internal/card-vault/quick-pay/mark-paid"},
		{name: "masked cards", method: http.MethodPost, path: "/internal/card-vault/cards/masked"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			setTestGatewayUserIdentityHeaders(req)
			resp, err := route.Test(req)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			assertFiberRouteRegistered(t, resp, tt.method, tt.path)
		})
	}
}

func TestCardVaultOwnsCardRegistrationInternalCommand(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleCardVault)
	route := GetMainEngine()

	tests := []cardVaultRoute{
		{name: "completed card registration card", method: http.MethodPost, path: "/internal/card-vault/card-registration/cards"},
		{name: "card by mobile", method: http.MethodPost, path: "/internal/card-vault/cards/by-mobile"},
		{name: "card by mobile and pan", method: http.MethodPost, path: "/internal/card-vault/cards/by-mobile-pan"},
		{name: "masked card by mobile", method: http.MethodPost, path: "/internal/card-vault/cards/masked-by-mobile"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			setGatewayAdminIdentityHeader(req)
			resp, err := route.Test(req)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			assertFiberRouteRegistered(t, resp, tt.method, tt.path)
		})
	}
}
