package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func consumerBeneficiaryRoutes() []struct {
	name   string
	method string
	path   string
} {
	return []struct {
		name   string
		method string
		path   string
	}{
		{name: "create beneficiary", method: http.MethodPost, path: "/consumer/beneficiary"},
		{name: "list beneficiaries", method: http.MethodGet, path: "/consumer/beneficiary"},
		{name: "delete beneficiary", method: http.MethodDelete, path: "/consumer/beneficiary"},
	}
}

func TestConsumerBeneficiaryRoutesAreProxiedByAPIGateway(t *testing.T) {
	ensureInit()
	configureGatewayProxyForTest(t)
	authorization := testAuthorizationHeader(t)
	route := GetMainEngine()

	for _, tt := range consumerBeneficiaryRoutes() {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Authorization", authorization)
			resp, err := route.Test(req, routeTestTimeout)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			assertGatewayProxied(t, resp)
		})
	}
}

func TestConsumerBeneficiaryRoutesAreOwnedByConsumerBeneficiary(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleBeneficiary)
	route := GetMainEngine()

	for _, tt := range consumerBeneficiaryRoutes() {
		t.Run(tt.name, func(t *testing.T) {
			const panSentinel = "6011000073184629"
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(`{"data":"`+panSentinel+`"} trailing`))
			setTestGatewayUserIdentityHeaders(req)
			resp, err := route.Test(req, routeTestTimeout)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusGone {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode, http.StatusGone, body)
			}
			var payload struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload.Code != "beneficiary_contract_retired" || strings.Contains(payload.Message, panSentinel) {
				t.Fatalf("response = %+v", payload)
			}
		})
	}
}

func TestConsumerBeneficiaryDoesNotOwnOtherServiceRoutes(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleBeneficiary)
	route := GetMainEngine()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "login", method: http.MethodPost, path: "/consumer/login"},
		{name: "card list", method: http.MethodGet, path: "/consumer/get_cards"},
		{name: "ebs balance", method: http.MethodPost, path: "/consumer/balance"},
		{name: "notifications", method: http.MethodGet, path: "/consumer/notifications"},
		{name: "wallet", method: http.MethodPost, path: "/wallet/wallets"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			setTestGatewayUserIdentityHeaders(req)
			resp, err := route.Test(req, routeTestTimeout)
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
