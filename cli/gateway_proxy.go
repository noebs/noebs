package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/proxy"
)

type gatewayAuthMode int

const (
	gatewayAuthPublic gatewayAuthMode = iota
	gatewayAuthUser
	gatewayAuthAdmin
)

type gatewayRouteSpec struct {
	method string
	path   string
	role   serviceRole
	auth   gatewayAuthMode
}

func registerAPIGatewayProxyRoutes(route *fiber.App, cfg ebs_fields.NoebsConfig, jwt gateway.JWTAuth, adminGuard fiber.Handler) error {
	proxies := map[serviceRole]fiber.Handler{}
	for _, spec := range gatewayProxyRouteSpecs() {
		handler, ok := proxies[spec.role]
		if !ok {
			target, err := serviceDiscoveryEndpoint(cfg, spec.role)
			if err != nil {
				return err
			}
			handler = gatewayProxyHandler(target)
			proxies[spec.role] = handler
		}

		handlers := make([]fiber.Handler, 0, 3)
		handlers = append(handlers, clearGatewayUserIdentityHeaders)
		switch spec.auth {
		case gatewayAuthPublic:
		case gatewayAuthUser:
			handlers = append(handlers, jwt.AuthMiddleware(), propagateGatewayUserIdentity)
		case gatewayAuthAdmin:
			handlers = append(handlers, adminGuard)
		default:
			return fmt.Errorf("unknown gateway auth mode %d for %s %s", spec.auth, spec.method, spec.path)
		}
		handlers = append(handlers, handler)
		route.Add(spec.method, spec.path, handlers...)
	}
	return nil
}

func serviceDiscoveryEndpoint(cfg ebs_fields.NoebsConfig, role serviceRole) (string, error) {
	endpoint := strings.TrimSpace(cfg.ServiceDiscovery[string(role)])
	if endpoint == "" {
		return "", fmt.Errorf("noebs.service_discovery missing %q", role)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse noebs.service_discovery.%s: %w", role, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("noebs.service_discovery.%s must use http or https", role)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("noebs.service_discovery.%s missing host", role)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func gatewayProxyHandler(endpoint string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		target := endpoint + c.OriginalURL()
		if err := proxy.Do(c, target); err != nil {
			return fiber.NewError(http.StatusBadGateway, err.Error())
		}
		return nil
	}
}

func clearGatewayUserIdentityHeaders(c *fiber.Ctx) error {
	c.Request().Header.Del(gateway.GatewayTenantIDHeader)
	c.Request().Header.Del(gateway.GatewayUserIDHeader)
	c.Request().Header.Del(gateway.GatewayMobileHeader)
	return c.Next()
}

func propagateGatewayUserIdentity(c *fiber.Ctx) error {
	tenantID, ok := c.Locals("tenant_id").(string)
	if !ok || strings.TrimSpace(tenantID) == "" {
		return fiber.NewError(http.StatusUnauthorized, "missing gateway tenant identity")
	}
	userID, ok := c.Locals("user_id").(int64)
	if !ok || userID <= 0 {
		return fiber.NewError(http.StatusUnauthorized, "missing gateway user identity")
	}
	c.Request().Header.Set(gateway.GatewayTenantIDHeader, tenantID)
	c.Request().Header.Set(gateway.GatewayUserIDHeader, strconv.FormatInt(userID, 10))
	if mobile, ok := c.Locals("mobile").(string); ok && strings.TrimSpace(mobile) != "" {
		c.Request().Header.Set(gateway.GatewayMobileHeader, mobile)
	}
	return c.Next()
}

func gatewayProxyRouteSpecs() []gatewayRouteSpec {
	return []gatewayRouteSpec{
		{method: fiber.MethodPost, path: "/generate_api_key", role: serviceRoleIdentityAuth, auth: gatewayAuthAdmin},
		{method: fiber.MethodPost, path: "/consumer/register", role: serviceRoleIdentityAuth, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/register_with_card", role: serviceRoleIdentityAuth, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/login", role: serviceRoleIdentityAuth, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/refresh", role: serviceRoleIdentityAuth, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/otp/generate", role: serviceRoleIdentityAuth, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/otp/generate_insecure", role: serviceRoleIdentityAuth, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/otp/login", role: serviceRoleIdentityAuth, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/otp/verify", role: serviceRoleIdentityAuth, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/otp/balance", role: serviceRoleIdentityAuth, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/auth/google", role: serviceRoleIdentityAuth, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/check_user", role: serviceRoleIdentityAuth, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/kyc", role: serviceRoleIdentityAuth, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/auth/complete_profile", role: serviceRoleIdentityAuth, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/consumer/auth/me", role: serviceRoleIdentityAuth, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/consumer/user", role: serviceRoleIdentityAuth, auth: gatewayAuthUser},
		{method: fiber.MethodPut, path: "/consumer/user", role: serviceRoleIdentityAuth, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/consumer/user/lang", role: serviceRoleIdentityAuth, auth: gatewayAuthUser},
		{method: fiber.MethodPut, path: "/consumer/user/lang", role: serviceRoleIdentityAuth, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/user/device", role: serviceRoleIdentityAuth, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/change_password", role: serviceRoleIdentityAuth, auth: gatewayAuthUser},

		{method: fiber.MethodPost, path: "/consumer/card_info", role: serviceRoleCardVault, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/pan_from_mobile", role: serviceRoleCardVault, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/cards/new", role: serviceRoleCardVault, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/cards/complete", role: serviceRoleCardVault, auth: gatewayAuthPublic},
		{method: fiber.MethodGet, path: "/consumer/nec2name", role: serviceRoleCardVault, auth: gatewayAuthPublic},
		{method: fiber.MethodGet, path: "/consumer/get_cards", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/add_card", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodPut, path: "/consumer/edit_card", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodDelete, path: "/consumer/delete_card", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/cards/set_main", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/consumer/users/cards", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/consumer/mobile2pan", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/consumer/payment_token", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/payment_token", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/payment_request", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/payment_token/quick_pay", role: serviceRoleCardVault, auth: gatewayAuthUser},

		{method: fiber.MethodPost, path: "/ebs/*", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/workingKey", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/cardTransfer", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/voucher", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/voucher/cash_in", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/cashout", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/voucher/cash_out", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/purchase", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/cashIn", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/cashOut", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/billInquiry", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/billPayment", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/bills", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/changePin", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/miniStatement", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/isAlive", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/balance", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/refund", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/toAccount", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/statement", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodGet, path: "/wrk", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/balance", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/status", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/is_alive", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/bill_payment", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/bills", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodGet, path: "/consumer/guess_biller", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/bill_inquiry", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/p2p", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/cashIn", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/cashOut", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/account", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/purchase", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/n/status", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/key", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/ipin", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/generate_qr", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/qr_payment", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/qr_status", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/qr_refund", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/qr_complete", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/ipin_key", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/generate_ipin", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/complete_ipin", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodPost, path: "/consumer/vouchers/generate", role: serviceRoleEBSAdapter, auth: gatewayAuthPublic},
		{method: fiber.MethodGet, path: "/consumer/transaction", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/consumer/transactions", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/p2p_mobile", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},

		{method: fiber.MethodPost, path: "/consumer/beneficiary", role: serviceRoleBeneficiary, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/consumer/beneficiary", role: serviceRoleBeneficiary, auth: gatewayAuthUser},
		{method: fiber.MethodDelete, path: "/consumer/beneficiary", role: serviceRoleBeneficiary, auth: gatewayAuthUser},

		{method: fiber.MethodGet, path: "/ws", role: serviceRoleNotification, auth: gatewayAuthPublic},
		{method: fiber.MethodGet, path: "/consumer/notifications", role: serviceRoleNotification, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/submit_contacts", role: serviceRoleNotification, auth: gatewayAuthUser},

		{method: fiber.MethodPost, path: "/psp/webhooks/:provider", role: serviceRolePSPWebhook, auth: gatewayAuthPublic},

		{method: fiber.MethodGet, path: "/wallet/methods", role: serviceRoleWalletAPI, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/wallet/wallets", role: serviceRoleWalletAPI, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/wallet/wallets/:id/transactions", role: serviceRoleWalletAPI, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/wallet/wallets/:id", role: serviceRoleWalletAPI, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/admin/wallet", role: serviceRoleWalletAPI, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/admin/wallet/", role: serviceRoleWalletAPI, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/admin/wallet/wallets", role: serviceRoleWalletAPI, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/admin/wallet/wallets/:id", role: serviceRoleWalletAPI, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/admin/wallet/transactions", role: serviceRoleWalletAPI, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/admin/wallet/transactions/:client_reference", role: serviceRoleWalletAPI, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/admin/wallet/pending", role: serviceRoleWalletAPI, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/admin/wallet/manual", role: serviceRoleWalletAPI, auth: gatewayAuthAdmin},
		{method: fiber.MethodPost, path: "/admin/wallet/manual", role: serviceRoleWalletAPI, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/admin/wallet/manual/:workflow_id", role: serviceRoleWalletAPI, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/admin/wallet/fees", role: serviceRoleWalletAPI, auth: gatewayAuthAdmin},
		{method: fiber.MethodPost, path: "/admin/wallet/fees", role: serviceRoleWalletAPI, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/admin/wallet/rates", role: serviceRoleWalletAPI, auth: gatewayAuthAdmin},
		{method: fiber.MethodPost, path: "/admin/wallet/rates", role: serviceRoleWalletAPI, auth: gatewayAuthAdmin},
		{method: fiber.MethodPost, path: "/admin/wallet/approve/:workflow_id", role: serviceRoleWalletAPI, auth: gatewayAuthAdmin},
		{method: fiber.MethodPost, path: "/admin/wallet/reject/:workflow_id", role: serviceRoleWalletAPI, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/admin/wallet/audit", role: serviceRoleWalletAPI, auth: gatewayAuthAdmin},

		{method: fiber.MethodGet, path: "/dashboard/assets/*", role: serviceRoleAdminReporting, auth: gatewayAuthPublic},
		{method: fiber.MethodGet, path: "/dashboard", role: serviceRoleAdminReporting, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/dashboard/", role: serviceRoleAdminReporting, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/dashboard/get_tid", role: serviceRoleAdminReporting, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/dashboard/get", role: serviceRoleAdminReporting, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/dashboard/all", role: serviceRoleAdminReporting, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/dashboard/all/:id", role: serviceRoleAdminReporting, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/dashboard/count", role: serviceRoleAdminReporting, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/dashboard/settlement", role: serviceRoleAdminReporting, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/dashboard/merchant", role: serviceRoleAdminReporting, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/dashboard/merchant/:id", role: serviceRoleAdminReporting, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/dashboard/status", role: serviceRoleAdminReporting, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/dashboard/test_browser", role: serviceRoleAdminReporting, auth: gatewayAuthAdmin},
		{method: fiber.MethodGet, path: "/dashboard/stream", role: serviceRoleAdminReporting, auth: gatewayAuthAdmin},
	}
}
