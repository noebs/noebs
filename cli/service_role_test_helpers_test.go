package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adonese/noebs/adminreporting"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/dashboard"
	"github.com/adonese/noebs/merchant"
	"github.com/adonese/noebs/wallet"
	walletpsp "github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
)

func setServiceRoleForTest(t *testing.T, role serviceRole) {
	t.Helper()
	previousRole := noebsConfig.ServiceRole
	previousServices := captureRoleServices()
	noebsConfig.ServiceRole = string(role)
	if err := initRoleServices(role); err != nil {
		t.Fatalf("initRoleServices(%s): %v", role, err)
	}
	t.Cleanup(func() {
		noebsConfig.ServiceRole = previousRole
		previousServices.restore()
	})
}

type roleServicesSnapshot struct {
	consumerService   consumer.Service
	adminReporting    adminreporting.Service
	dashService       dashboard.Service
	merchantServices  merchant.Service
	walletService     *wallet.Service
	pspWebhookStore   *walletstore.Store
	walletPSPRegistry *walletpsp.Registry
	walletPSPLoader   *walletpsp.Loader
}

func captureRoleServices() roleServicesSnapshot {
	return roleServicesSnapshot{
		consumerService:   consumerService,
		adminReporting:    adminReportingService,
		dashService:       dashService,
		merchantServices:  merchantServices,
		walletService:     walletService,
		pspWebhookStore:   pspWebhookStore,
		walletPSPRegistry: walletPSPRegistry,
		walletPSPLoader:   walletPSPLoader,
	}
}

func (s roleServicesSnapshot) restore() {
	consumerService = s.consumerService
	adminReportingService = s.adminReporting
	dashService = s.dashService
	merchantServices = s.merchantServices
	walletService = s.walletService
	pspWebhookStore = s.pspWebhookStore
	walletPSPRegistry = s.walletPSPRegistry
	walletPSPLoader = s.walletPSPLoader
}

func configureGatewayProxyForTest(t *testing.T) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Path", r.URL.RequestURI())
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	setGatewayDiscoveryForTest(t, upstream.URL)
	setServiceRoleForTest(t, serviceRoleAPIGateway)
}

func setGatewayDiscoveryForTest(t *testing.T, endpoint string) {
	t.Helper()
	previousDiscovery := noebsConfig.ServiceDiscovery
	noebsConfig.ServiceDiscovery = map[string]string{}
	for _, spec := range gatewayProxyRouteSpecs() {
		noebsConfig.ServiceDiscovery[string(spec.role)] = endpoint
	}
	t.Cleanup(func() {
		noebsConfig.ServiceDiscovery = previousDiscovery
	})
}

func setAdminKeyForTest(t *testing.T) string {
	t.Helper()
	previousKey := noebsConfig.AdminKey
	noebsConfig.AdminKey = "test-admin-key"
	t.Cleanup(func() {
		noebsConfig.AdminKey = previousKey
	})
	return noebsConfig.AdminKey
}

func assertGatewayProxied(t *testing.T, resp *http.Response) {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
}
