package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/gofiber/fiber/v2"
	"google.golang.org/grpc"
)

type discoveryCaptureClient struct {
	walletv1.WalletPublicServiceClient
	tenant, walletID string
}

func (c *discoveryCaptureClient) ListWalletFundingMethods(_ context.Context, r *walletv1.ListWalletFundingMethodsRequest, _ ...grpc.CallOption) (*walletv1.ListWalletFundingMethodsResponse, error) {
	c.tenant, c.walletID = r.TenantId, r.WalletId
	return &walletv1.ListWalletFundingMethodsResponse{Methods: []*walletv1.WalletFundingMethod{{
		Id: "mojaloop:receive", ProviderId: "mojaloop", CurrencyUnitVersion: "9007199254740993", Mode: "external_transfer", UnavailableReason: "registration_required",
	}}}, nil
}

func TestFundingDiscoveryHTTPUsesAuthenticatedIdentityAndExactWireContract(t *testing.T) {
	client := &discoveryCaptureClient{}
	handler := NewGRPCUserHandler(client, ebs_fields.NoebsConfig{WalletEnabled: true})
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	RegisterGRPCUserRoutes(app.Group("/wallet", gateway.InternalUserIdentityMiddleware()), handler)
	request := httptest.NewRequest(http.MethodGet, "/wallet/funding-methods?wallet_id=selected-wallet", nil)
	setWalletPrincipalHeaders(request, walletUserPrincipalHeaderValues(time.Now().UTC().Add(time.Hour)))
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer closeWalletResponseBody(t, response.Body)
	if response.StatusCode != http.StatusOK || client.tenant != "tenant-1" || client.walletID != "selected-wallet" {
		t.Fatalf("authenticated routing: status=%d client=%+v", response.StatusCode, client)
	}
	payload := decodeJSONObject(t, response.Body)
	methods := payload["methods"].([]any)
	method := methods[0].(map[string]any)
	if method["provider_id"] != "mojaloop" || method["available"] != false || method["account_identifier"] != "" || method["currency_unit_version"] != "9007199254740993" {
		t.Fatalf("app contract lost exact ID, false or empty fields: %#v", method)
	}
	for _, query := range []string{"tenant_id=another-tenant", "user_id=7"} {
		request := httptest.NewRequest(http.MethodGet, "/wallet/funding-methods?wallet_id=selected-wallet&"+query, nil)
		setWalletPrincipalHeaders(request, walletUserPrincipalHeaderValues(time.Now().UTC().Add(time.Hour)))
		response, err := app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		closeWalletResponseBody(t, response.Body)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("accepted caller identity selector %s: %d", query, response.StatusCode)
		}
	}
}
