package main

import (
	"net/http"
	"testing"

	consumerhandler "github.com/adonese/noebs/consumer/handler"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gofiber/fiber/v2"
)

func TestEnabledGatewayProxyRouteSpecsDefaultAlphaFeaturesOff(t *testing.T) {
	off := gatewaySpecKeys(enabledGatewayProxyRouteSpecs(ebs_fields.NoebsConfig{}))
	for _, key := range []string{
		gatewayRouteKey(http.MethodGet, "/consumer/cards"),
		gatewayRouteKey(http.MethodPost, "/consumer/cards/enrollment-intents"),
		gatewayRouteKey(http.MethodPost, "/consumer/balance"),
		gatewayRouteKey(http.MethodGet, "/ws"),
	} {
		if off[key] {
			t.Errorf("alpha route enabled by default: %s", key)
		}
	}
	if !off[gatewayRouteKey(http.MethodGet, "/consumer/user")] {
		t.Fatal("stable consumer route was removed with alpha features")
	}

	features := []struct {
		name string
		key  string
		cfg  ebs_fields.NoebsConfig
	}{
		{
			name: "card management",
			key:  gatewayRouteKey(http.MethodGet, "/consumer/cards"),
			cfg:  ebs_fields.NoebsConfig{OpaqueCardManagementEnabled: true},
		},
		{
			name: "opaque balance",
			key:  gatewayRouteKey(http.MethodPost, "/consumer/balance"),
			cfg:  ebs_fields.NoebsConfig{OpaqueBalanceEnabled: true},
		},
		{
			name: "chat",
			key:  gatewayRouteKey(http.MethodGet, "/ws"),
			cfg:  ebs_fields.NoebsConfig{ChatEnabled: true},
		},
	}
	for _, feature := range features {
		t.Run(feature.name, func(t *testing.T) {
			enabled := gatewaySpecKeys(enabledGatewayProxyRouteSpecs(feature.cfg))
			for _, candidate := range features {
				if got := enabled[candidate.key]; got != (candidate.name == feature.name) {
					t.Errorf("%s enabled = %t", candidate.name, got)
				}
			}
		})
	}

	management := gatewaySpecKeys(enabledGatewayProxyRouteSpecs(features[0].cfg))
	for _, key := range []string{
		gatewayRouteKey(http.MethodPatch, "/consumer/cards/:card_id"),
		gatewayRouteKey(http.MethodPost, "/consumer/cards/enrollment-intents"),
		gatewayRouteKey(http.MethodPost, "/consumer/cards/enrollment-intents/:enrollment_id/confirm"),
	} {
		if !management[key] {
			t.Errorf("card-management route is absent: %s", key)
		}
	}
}

func TestOwningServicesDoNotMountDisabledAlphaRoutes(t *testing.T) {
	pass := func(c *fiber.Ctx) error { return c.Next() }
	handler := &consumerhandler.Handler{}

	cardVault := fiber.New()
	registerCardVaultRoutes(cardVault, pass, pass, false, handler)
	for _, route := range cardVaultActiveRoutes() {
		assertFiberRouteAbsent(t, cardVault, route)
	}
	for _, route := range cardVaultInternalRoutes() {
		assertFiberRoutePresent(t, cardVault, route.method, route.path)
	}

	ebsAdapter := fiber.New()
	registerEBSAdapterRoutes(ebsAdapter, pass, false, false, handler)
	for _, route := range []roleRoute{
		{method: http.MethodPost, path: "/consumer/cards/enrollment-intents"},
		{method: http.MethodPost, path: "/consumer/cards/enrollment-intents/:enrollment_id/confirm", requestPath: "/consumer/cards/enrollment-intents/id/confirm"},
		{method: http.MethodPost, path: "/consumer/balance"},
	} {
		assertFiberRouteAbsent(t, ebsAdapter, route)
	}
	assertFiberRoutePresent(t, ebsAdapter, http.MethodPost, "/consumer/status")

	notification := fiber.New()
	registerNotificationChatRoutes(notification, pass, pass, false, handler)
	assertFiberRouteAbsent(t, notification, roleRoute{method: http.MethodGet, path: "/ws"})
	assertFiberRoutePresent(t, notification, http.MethodPost, "/internal/notification-chat/push-data")
}

func gatewaySpecKeys(specs []gatewayRouteSpec) map[string]bool {
	keys := make(map[string]bool, len(specs))
	for _, spec := range specs {
		keys[gatewayRouteKey(spec.method, spec.path)] = true
	}
	return keys
}
