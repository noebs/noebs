package main

import (
	"net/http"
	"net/http/httptest"
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
			resp, err := route.Test(req)
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
	authorization := testAuthorizationHeader(t)
	route := GetMainEngine()

	for _, tt := range consumerBeneficiaryRoutes() {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Authorization", authorization)
			resp, err := route.Test(req)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			defer resp.Body.Close()
			assertFiberRouteRegistered(t, resp, tt.method, tt.path)
		})
	}
}

func TestConsumerBeneficiaryDoesNotOwnOtherServiceRoutes(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleBeneficiary)
	authorization := testAuthorizationHeader(t)
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
			req.Header.Set("Authorization", authorization)
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
