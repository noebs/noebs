package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCardVaultRoutesAreProxiedByAPIGateway(t *testing.T) {
	ensureInit()
	configureGatewayProxyForTest(t)
	authorization := testAuthorizationHeader(t)
	route := GetMainEngine()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "card info", method: http.MethodPost, path: "/consumer/card_info"},
		{name: "pan from mobile", method: http.MethodPost, path: "/consumer/pan_from_mobile"},
		{name: "card registration", method: http.MethodPost, path: "/consumer/cards/new"},
		{name: "card registration completion", method: http.MethodPost, path: "/consumer/cards/complete"},
		{name: "nec lookup", method: http.MethodGet, path: "/consumer/nec2name"},
		{name: "card list", method: http.MethodGet, path: "/consumer/get_cards"},
		{name: "add card", method: http.MethodPost, path: "/consumer/add_card"},
		{name: "edit card", method: http.MethodPut, path: "/consumer/edit_card"},
		{name: "delete card", method: http.MethodDelete, path: "/consumer/delete_card"},
		{name: "main card", method: http.MethodPost, path: "/consumer/cards/set_main"},
		{name: "cards by mobile", method: http.MethodGet, path: "/consumer/users/cards"},
		{name: "mobile to pan", method: http.MethodGet, path: "/consumer/mobile2pan"},
		{name: "get payment token", method: http.MethodGet, path: "/consumer/payment_token"},
		{name: "create payment token", method: http.MethodPost, path: "/consumer/payment_token"},
		{name: "payment request", method: http.MethodPost, path: "/consumer/payment_request"},
		{name: "quick pay token", method: http.MethodPost, path: "/consumer/payment_token/quick_pay"},
	}
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

func TestCardVaultRoutesAreOwnedByCardVault(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleCardVault)
	route := GetMainEngine()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "card info", method: http.MethodPost, path: "/consumer/card_info"},
		{name: "pan from mobile", method: http.MethodPost, path: "/consumer/pan_from_mobile"},
		{name: "card registration", method: http.MethodPost, path: "/consumer/cards/new"},
		{name: "card registration completion", method: http.MethodPost, path: "/consumer/cards/complete"},
		{name: "nec lookup", method: http.MethodGet, path: "/consumer/nec2name"},
		{name: "card list", method: http.MethodGet, path: "/consumer/get_cards"},
		{name: "add card", method: http.MethodPost, path: "/consumer/add_card"},
		{name: "edit card", method: http.MethodPut, path: "/consumer/edit_card"},
		{name: "delete card", method: http.MethodDelete, path: "/consumer/delete_card"},
		{name: "main card", method: http.MethodPost, path: "/consumer/cards/set_main"},
		{name: "cards by mobile", method: http.MethodGet, path: "/consumer/users/cards"},
		{name: "mobile to pan", method: http.MethodGet, path: "/consumer/mobile2pan"},
		{name: "get payment token", method: http.MethodGet, path: "/consumer/payment_token"},
		{name: "create payment token", method: http.MethodPost, path: "/consumer/payment_token"},
		{name: "payment request", method: http.MethodPost, path: "/consumer/payment_request"},
		{name: "quick pay token", method: http.MethodPost, path: "/consumer/payment_token/quick_pay"},
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
