package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type ebsAdapterRoute struct {
	name   string
	method string
	path   string
}

func ebsAdapterRoutes() []ebsAdapterRoute {
	return []ebsAdapterRoute{
		{name: "consumer card info", method: http.MethodPost, path: "/consumer/card_info"},
		{name: "consumer recovery balance", method: http.MethodPost, path: "/consumer/otp/balance"},
		{name: "consumer register with card", method: http.MethodPost, path: "/consumer/register_with_card"},
		{name: "consumer card registration start", method: http.MethodPost, path: "/consumer/cards/new"},
		{name: "consumer card registration completion", method: http.MethodPost, path: "/consumer/cards/complete"},
		{name: "consumer meter lookup", method: http.MethodGet, path: "/consumer/nec2name"},
		{name: "consumer balance", method: http.MethodPost, path: "/consumer/balance"},
		{name: "consumer status", method: http.MethodPost, path: "/consumer/status"},
		{name: "consumer alive", method: http.MethodPost, path: "/consumer/is_alive"},
		{name: "consumer bill payment", method: http.MethodPost, path: "/consumer/bill_payment"},
		{name: "consumer bills", method: http.MethodPost, path: "/consumer/bills"},
		{name: "consumer cached biller", method: http.MethodGet, path: "/consumer/biller"},
		{name: "consumer bill inquiry", method: http.MethodPost, path: "/consumer/bill_inquiry"},
		{name: "consumer p2p", method: http.MethodPost, path: "/consumer/p2p"},
		{name: "consumer cash in", method: http.MethodPost, path: "/consumer/cashIn"},
		{name: "consumer cash out", method: http.MethodPost, path: "/consumer/cashOut"},
		{name: "consumer account", method: http.MethodPost, path: "/consumer/account"},
		{name: "consumer purchase", method: http.MethodPost, path: "/consumer/purchase"},
		{name: "consumer notification status", method: http.MethodPost, path: "/consumer/n/status"},
		{name: "consumer key", method: http.MethodPost, path: "/consumer/key"},
		{name: "consumer ipin change", method: http.MethodPost, path: "/consumer/ipin"},
		{name: "consumer qr registration", method: http.MethodPost, path: "/consumer/generate_qr"},
		{name: "consumer qr payment", method: http.MethodPost, path: "/consumer/qr_payment"},
		{name: "consumer qr status", method: http.MethodPost, path: "/consumer/qr_status"},
		{name: "consumer qr refund", method: http.MethodPost, path: "/consumer/qr_refund"},
		{name: "consumer qr complete", method: http.MethodPost, path: "/consumer/qr_complete"},
		{name: "consumer ipin key", method: http.MethodPost, path: "/consumer/ipin_key"},
		{name: "consumer generate ipin", method: http.MethodPost, path: "/consumer/generate_ipin"},
		{name: "consumer complete ipin", method: http.MethodPost, path: "/consumer/complete_ipin"},
		{name: "consumer voucher", method: http.MethodPost, path: "/consumer/vouchers/generate"},
		{name: "consumer transaction", method: http.MethodGet, path: "/consumer/transaction"},
		{name: "consumer transactions", method: http.MethodGet, path: "/consumer/transactions"},
		{name: "consumer mobile transfer", method: http.MethodPost, path: "/consumer/p2p_mobile"},
		{name: "consumer quick pay token", method: http.MethodPost, path: "/consumer/payment_token/quick_pay"},
		{name: "merchant proxy", method: http.MethodPost, path: "/ebs/balance"},
		{name: "merchant working key", method: http.MethodPost, path: "/workingKey"},
		{name: "merchant card transfer", method: http.MethodPost, path: "/cardTransfer"},
		{name: "merchant voucher", method: http.MethodPost, path: "/voucher"},
		{name: "merchant voucher cash in", method: http.MethodPost, path: "/voucher/cash_in"},
		{name: "merchant cashout", method: http.MethodPost, path: "/cashout"},
		{name: "merchant voucher cash out", method: http.MethodPost, path: "/voucher/cash_out"},
		{name: "merchant purchase", method: http.MethodPost, path: "/purchase"},
		{name: "merchant cash in", method: http.MethodPost, path: "/cashIn"},
		{name: "merchant cash out", method: http.MethodPost, path: "/cashOut"},
		{name: "merchant bill inquiry", method: http.MethodPost, path: "/billInquiry"},
		{name: "merchant bill payment", method: http.MethodPost, path: "/billPayment"},
		{name: "merchant bills", method: http.MethodPost, path: "/bills"},
		{name: "merchant change pin", method: http.MethodPost, path: "/changePin"},
		{name: "merchant mini statement", method: http.MethodPost, path: "/miniStatement"},
		{name: "merchant alive", method: http.MethodPost, path: "/isAlive"},
		{name: "merchant balance", method: http.MethodPost, path: "/balance"},
		{name: "merchant refund", method: http.MethodPost, path: "/refund"},
		{name: "merchant account transfer", method: http.MethodPost, path: "/toAccount"},
		{name: "merchant statement", method: http.MethodPost, path: "/statement"},
	}
}

func assertFiberRouteRegistered(t *testing.T, resp *http.Response, method, path string) {
	t.Helper()
	if resp.StatusCode != http.StatusNotFound {
		return
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if strings.Contains(string(body), "Cannot "+method+" "+path) {
		t.Fatalf("route not registered: %s %s", method, path)
	}
}

func TestEBSAdapterRoutesAreProxiedByAPIGateway(t *testing.T) {
	ensureInit()
	configureGatewayProxyForTest(t)
	authorization := testAuthorizationHeader(t)
	route := GetMainEngine()

	for _, tt := range ebsAdapterRoutes() {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Authorization", authorization)
			req.Header.Set("X-Tenant-ID", "test-tenant")
			resp, err := route.Test(req)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			assertGatewayProxied(t, resp)
		})
	}
}

func TestEBSAdapterRoutesAreOwnedByEBSAdapter(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleEBSAdapter)
	route := GetMainEngine()

	for _, tt := range ebsAdapterRoutes() {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			setTestGatewayUserIdentityHeaders(req)
			resp, err := route.Test(req, 5_000)
			if err != nil {
				t.Fatalf("route.Test() error = %v", err)
			}
			defer resp.Body.Close()
			assertFiberRouteRegistered(t, resp, tt.method, tt.path)
		})
	}
}

func TestEBSAdapterDoesNotOwnIdentityCardNotificationOrWalletRoutes(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleEBSAdapter)
	route := GetMainEngine()

	tests := []ebsAdapterRoute{
		{name: "identity login", method: http.MethodPost, path: "/consumer/login"},
		{name: "card list", method: http.MethodGet, path: "/consumer/get_cards"},
		{name: "notifications", method: http.MethodGet, path: "/consumer/notifications"},
		{name: "wallet", method: http.MethodPost, path: "/wallet"},
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

func TestEBSAdapterDoesNotOwnLegacyGuessBillerRoute(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleEBSAdapter)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/consumer/guess_biller", nil)
	setTestGatewayUserIdentityHeaders(req)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestEBSAdapterDoesNotExposePublicMobilePANLookup(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleEBSAdapter)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodPost, "/consumer/pan_from_mobile", nil)
	setTestGatewayUserIdentityHeaders(req)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	for _, spec := range gatewayProxyRouteSpecs() {
		if spec.path == "/consumer/pan_from_mobile" {
			t.Fatalf("%s must not be proxied as a public route; use card-vault internal lookup commands", spec.path)
		}
	}
}

func TestEBSAdapterDoesNotExposeLegacyManualTestRoute(t *testing.T) {
	ensureInit()
	setServiceRoleForTest(t, serviceRoleEBSAdapter)
	route := GetMainEngine()

	req := httptest.NewRequest(http.MethodGet, "/wrk", nil)
	resp, err := route.Test(req)
	if err != nil {
		t.Fatalf("route.Test() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	for _, spec := range gatewayProxyRouteSpecs() {
		if spec.path == "/wrk" {
			t.Fatalf("%s must not be proxied as a public route; use explicit EBS request routes", spec.path)
		}
	}
}
