package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	fastws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/proxy"
	"github.com/valyala/fasthttp"
)

type gatewayAuthMode int

const (
	gatewayAuthPublic gatewayAuthMode = iota
	gatewayAuthPublicTenant
	gatewayAuthPublicQueryTenant
	gatewayAuthUser
	gatewayAuthAdmin
	gatewayAuthAdminTenant
	gatewayAuthAdminWalletQueryTenant
	gatewayAuthAdminWalletFormTenant
)

type gatewayRouteSpec struct {
	method    string
	path      string
	role      serviceRole
	auth      gatewayAuthMode
	websocket bool
}

const gatewayWebSocketHandshakeTimeout = 10 * time.Second

func registerAPIGatewayProxyRoutes(route *fiber.App, cfg ebs_fields.NoebsConfig, jwt gateway.JWTAuth, adminGuard fiber.Handler) error {
	publicTenantID, err := store.ValidateTenantID(cfg.DefaultTenantID)
	if err != nil {
		return fmt.Errorf("noebs.default_tenant_id: %w", err)
	}
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
		if spec.websocket {
			target, err := serviceDiscoveryEndpoint(cfg, spec.role)
			if err != nil {
				return err
			}
			handler = gatewayWebSocketProxyHandler(target)
		}

		handlers := make([]fiber.Handler, 0, 3)
		handlers = append(handlers, clearGatewayIdentityHeaders)
		switch spec.auth {
		case gatewayAuthPublic:
			handlers = append(handlers, clearPublicCredentialHeaders)
		case gatewayAuthPublicTenant:
			handlers = append(handlers, propagateGatewayPublicTenant(publicTenantID))
		case gatewayAuthPublicQueryTenant:
			handlers = append(handlers, propagateGatewayPublicQueryTenant(publicTenantID))
		case gatewayAuthUser:
			handlers = append(handlers, jwt.AuthMiddleware(), propagateGatewayUserIdentity)
		case gatewayAuthAdmin:
			handlers = append(handlers, adminGuard, propagateGatewayAdminIdentity)
		case gatewayAuthAdminTenant:
			handlers = append(handlers, adminGuard, propagateGatewayAdminTenantIdentity)
		case gatewayAuthAdminWalletQueryTenant:
			handlers = append(handlers, adminGuard, propagateGatewayAdminWalletQueryTenantIdentity)
		case gatewayAuthAdminWalletFormTenant:
			handlers = append(handlers, adminGuard, propagateGatewayAdminWalletFormTenantIdentity)
		default:
			return fmt.Errorf("unknown gateway auth mode %d for %s %s", spec.auth, spec.method, spec.path)
		}
		if spec.websocket {
			handlers = append(handlers, propagateGatewaySessionToken)
		}
		handlers = append(handlers, handler)
		route.Add(spec.method, spec.path, handlers...)
	}
	return nil
}

func gatewayWebSocketProxyHandler(endpoint string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if !fastws.FastHTTPIsWebSocketUpgrade(c.Context()) {
			return fiber.ErrUpgradeRequired
		}
		target, err := url.Parse(strings.TrimRight(endpoint, "/") + c.OriginalURL())
		if err != nil {
			return fiber.NewError(http.StatusBadGateway, "invalid websocket upstream")
		}
		switch target.Scheme {
		case "http":
			target.Scheme = "ws"
		case "https":
			target.Scheme = "wss"
		default:
			return fiber.NewError(http.StatusBadGateway, "invalid websocket upstream")
		}

		headers := make(http.Header)
		for _, name := range []string{
			gateway.GatewayTenantIDHeader,
			gateway.GatewayUserIDHeader,
			gateway.GatewayMobileHeader,
			gateway.GatewaySessionEpochHeader,
			gateway.GatewaySessionTokenHeader,
			gateway.GatewaySourceIPHeader,
		} {
			if value := strings.TrimSpace(c.Get(name)); value != "" {
				headers.Set(name, value)
			}
		}
		dialer := fastws.Dialer{
			HandshakeTimeout: gatewayWebSocketHandshakeTimeout,
			Subprotocols:     splitWebSocketSubprotocols(c.Get("Sec-WebSocket-Protocol")),
		}
		upstream, response, err := dialer.DialContext(c.UserContext(), target.String(), headers)
		if response != nil && response.Body != nil {
			defer response.Body.Close()
		}
		if err != nil {
			status := http.StatusBadGateway
			if response != nil && response.StatusCode >= 400 && response.StatusCode < 500 {
				status = response.StatusCode
			}
			return fiber.NewError(status, "websocket upstream rejected handshake")
		}

		if protocol := upstream.Subprotocol(); protocol != "" {
			c.Response().Header.Set("Sec-WebSocket-Protocol", protocol)
		}
		upgrader := fastws.FastHTTPUpgrader{
			HandshakeTimeout: gatewayWebSocketHandshakeTimeout,
			CheckOrigin:      func(*fasthttp.RequestCtx) bool { return true },
		}
		if err := upgrader.Upgrade(c.Context(), func(client *fastws.Conn) {
			proxyWebSocketConnections(client, upstream)
		}); err != nil {
			_ = upstream.Close()
			return err
		}
		return nil
	}
}

func splitWebSocketSubprotocols(raw string) []string {
	var protocols []string
	for _, protocol := range strings.Split(raw, ",") {
		if protocol = strings.TrimSpace(protocol); protocol != "" {
			protocols = append(protocols, protocol)
		}
	}
	return protocols
}

func proxyWebSocketConnections(client, upstream *fastws.Conn) {
	done := make(chan error, 2)
	go func() { done <- relayWebSocket(upstream, client) }()
	go func() { done <- relayWebSocket(client, upstream) }()
	<-done
	_ = client.Close()
	_ = upstream.Close()
	<-done
}

func relayWebSocket(destination, source *fastws.Conn) error {
	for {
		messageType, reader, err := source.NextReader()
		if err != nil {
			forwardWebSocketClose(destination, err)
			return err
		}
		writer, err := destination.NextWriter(messageType)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, reader)
		closeErr := writer.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
}

func forwardWebSocketClose(destination *fastws.Conn, err error) {
	var closeErr *fastws.CloseError
	if !errors.As(err, &closeErr) {
		return
	}
	deadline := time.Now().Add(time.Second)
	_ = destination.WriteControl(
		fastws.CloseMessage,
		fastws.FormatCloseMessage(closeErr.Code, closeErr.Text),
		deadline,
	)
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

func clearGatewayIdentityHeaders(c *fiber.Ctx) error {
	c.Request().Header.Del(gateway.GatewayTenantIDHeader)
	c.Request().Header.Del(gateway.GatewayUserIDHeader)
	c.Request().Header.Del(gateway.GatewayMobileHeader)
	c.Request().Header.Del(gateway.GatewaySessionEpochHeader)
	c.Request().Header.Del(gateway.GatewaySessionTokenHeader)
	c.Request().Header.Del(gateway.GatewaySourceIPHeader)
	c.Request().Header.Del(gateway.GatewayAdminIdentityHeader)
	c.Request().Header.Del(gateway.GatewayAdminRoleHeader)
	c.Request().Header.Del(gateway.GatewayAdminPermissionsHeader)
	return c.Next()
}

func clearPublicCredentialHeaders(c *fiber.Ctx) error {
	stripPublicCredentialHeaders(c)
	return c.Next()
}

func stripPublicCredentialHeaders(c *fiber.Ctx) {
	c.Request().Header.Del("Authorization")
	c.Request().Header.Del("X-Tenant-ID")
	c.Request().Header.Del("X-Admin-Key")
	c.Request().Header.Del("X-Admin-Role")
	c.Request().Header.Del("X-Admin-Permissions")
}

func propagateGatewayPublicTenant(publicTenantID string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		tenantID, err := requirePublicTenant(c.Get("X-Tenant-ID"), publicTenantID)
		if err != nil {
			return tenantValidationError(c, err)
		}
		c.Request().Header.Set(gateway.GatewayTenantIDHeader, tenantID)
		c.Request().Header.Set(gateway.GatewaySourceIPHeader, gatewayRequestSource(c))
		stripPublicCredentialHeaders(c)
		return c.Next()
	}
}

func propagateGatewayPublicQueryTenant(publicTenantID string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		tenantID, err := requirePublicTenant(string(c.Request().URI().QueryArgs().Peek("tenant_id")), publicTenantID)
		if err != nil {
			return tenantValidationError(c, err)
		}
		c.Request().Header.Set(gateway.GatewayTenantIDHeader, tenantID)
		c.Request().Header.Set(gateway.GatewaySourceIPHeader, gatewayRequestSource(c))
		stripPublicCredentialHeaders(c)
		return c.Next()
	}
}

func requirePublicTenant(requested, configured string) (string, error) {
	tenantID, err := store.ValidateTenantID(requested)
	if err != nil {
		return "", err
	}
	if tenantID != configured {
		return "", store.ErrInvalidTenantID
	}
	return tenantID, nil
}

func tenantValidationError(c *fiber.Ctx, err error) error {
	code := "invalid_tenant_id"
	if errors.Is(err, store.ErrMissingTenantID) {
		code = "missing_tenant_id"
	}
	return c.Status(http.StatusBadRequest).JSON(fiber.Map{"code": code, "message": err.Error()})
}

func propagateGatewayUserIdentity(c *fiber.Ctx) error {
	if c.Request().URI().QueryArgs().Has("tenant_id") {
		return unexpectedTenantIDError(c)
	}
	tenantID, ok := c.Locals("tenant_id").(string)
	if !ok || strings.TrimSpace(tenantID) == "" {
		return fiber.NewError(http.StatusUnauthorized, "missing gateway tenant identity")
	}
	userID, ok := c.Locals("user_id").(int64)
	if !ok || userID <= 0 {
		return fiber.NewError(http.StatusUnauthorized, "missing gateway user identity")
	}
	sessionEpoch, ok := c.Locals("session_epoch").(int64)
	if !ok || sessionEpoch <= 0 {
		return fiber.NewError(http.StatusUnauthorized, "missing gateway session identity")
	}
	c.Request().Header.Set(gateway.GatewayTenantIDHeader, tenantID)
	c.Request().Header.Set(gateway.GatewayUserIDHeader, strconv.FormatInt(userID, 10))
	c.Request().Header.Set(gateway.GatewaySessionEpochHeader, strconv.FormatInt(sessionEpoch, 10))
	c.Request().Header.Set(gateway.GatewaySourceIPHeader, gatewayRequestSource(c))
	if mobile, ok := c.Locals("mobile").(string); ok && strings.TrimSpace(mobile) != "" {
		c.Request().Header.Set(gateway.GatewayMobileHeader, mobile)
	}
	stripPublicCredentialHeaders(c)
	return c.Next()
}

func propagateGatewaySessionToken(c *fiber.Ctx) error {
	token, ok := c.Locals("session_token").(string)
	if !ok || strings.TrimSpace(token) == "" {
		return fiber.NewError(http.StatusUnauthorized, "missing gateway session token")
	}
	c.Request().Header.Set(gateway.GatewaySessionTokenHeader, token)
	return c.Next()
}

func gatewayRequestSource(c *fiber.Ctx) string {
	forwarded := strings.Split(c.Get(fiber.HeaderXForwardedFor), ",")
	for i := len(forwarded) - 1; i >= 0; i-- {
		if ip := net.ParseIP(strings.TrimSpace(forwarded[i])); ip != nil {
			return ip.String()
		}
	}
	if ip := net.ParseIP(strings.TrimSpace(c.IP())); ip != nil {
		return ip.String()
	}
	return net.IPv4zero.String()
}

func unexpectedTenantIDError(c *fiber.Ctx) error {
	return c.Status(http.StatusBadRequest).JSON(fiber.Map{
		"code":    "unexpected_tenant_id",
		"message": "tenant_id is not accepted on authenticated routes",
	})
}

func propagateGatewayAdminIdentity(c *fiber.Ctx) error {
	stripPublicCredentialHeaders(c)
	c.Request().Header.Set(gateway.GatewayAdminIdentityHeader, gateway.GatewayAdminIdentityValue)
	c.Request().Header.Set(gateway.GatewayAdminRoleHeader, gateway.GatewayAdminRoleValue)
	return c.Next()
}

func propagateGatewayAdminTenantIdentity(c *fiber.Ctx) error {
	tenantID, err := store.ValidateTenantID(c.Get("X-Tenant-ID"))
	if err != nil {
		return tenantValidationError(c, err)
	}
	stripPublicCredentialHeaders(c)
	c.Request().Header.Set(gateway.GatewayTenantIDHeader, tenantID)
	c.Request().Header.Set(gateway.GatewayAdminIdentityHeader, gateway.GatewayAdminIdentityValue)
	c.Request().Header.Set(gateway.GatewayAdminRoleHeader, gateway.GatewayAdminRoleValue)
	return c.Next()
}

func propagateGatewayAdminWalletQueryTenantIdentity(c *fiber.Ctx) error {
	return propagateGatewayAdminWalletTenantIdentity(c, string(c.Request().URI().QueryArgs().Peek("tenant_id")))
}

func propagateGatewayAdminWalletFormTenantIdentity(c *fiber.Ctx) error {
	return propagateGatewayAdminWalletTenantIdentity(c, string(c.Request().PostArgs().Peek("tenant_id")))
}

func propagateGatewayAdminWalletTenantIdentity(c *fiber.Ctx, rawTenantID string) error {
	tenantID, err := store.ValidateTenantID(rawTenantID)
	if err != nil {
		return tenantValidationError(c, err)
	}
	stripPublicCredentialHeaders(c)
	c.Request().Header.Set(gateway.GatewayTenantIDHeader, tenantID)
	c.Request().Header.Set(gateway.GatewayAdminIdentityHeader, gateway.GatewayAdminIdentityValue)
	c.Request().Header.Set(gateway.GatewayAdminRoleHeader, gateway.GatewayAdminRoleValue)
	return c.Next()
}

func gatewayProxyRouteSpecs() []gatewayRouteSpec {
	return []gatewayRouteSpec{
		{method: fiber.MethodPost, path: "/generate_api_key", role: serviceRoleIdentityAuth, auth: gatewayAuthAdmin},
		{method: fiber.MethodPost, path: "/consumer/register", role: serviceRoleIdentityAuth, auth: gatewayAuthPublicTenant},
		{method: fiber.MethodPost, path: "/consumer/login", role: serviceRoleIdentityAuth, auth: gatewayAuthPublicTenant},
		{method: fiber.MethodPost, path: "/consumer/refresh", role: serviceRoleIdentityAuth, auth: gatewayAuthPublicTenant},
		{method: fiber.MethodPost, path: "/consumer/otp/generate", role: serviceRoleIdentityAuth, auth: gatewayAuthPublicTenant},
		{method: fiber.MethodPost, path: "/consumer/otp/login", role: serviceRoleIdentityAuth, auth: gatewayAuthPublicTenant},
		{method: fiber.MethodPost, path: "/consumer/otp/verify", role: serviceRoleIdentityAuth, auth: gatewayAuthPublicTenant},
		{method: fiber.MethodPost, path: "/consumer/recovery/request", role: serviceRoleIdentityAuth, auth: gatewayAuthPublicTenant},
		{method: fiber.MethodPost, path: "/consumer/recovery/verify", role: serviceRoleIdentityAuth, auth: gatewayAuthPublicTenant},
		{method: fiber.MethodPost, path: "/consumer/recovery/reset", role: serviceRoleIdentityAuth, auth: gatewayAuthPublicTenant},
		{method: fiber.MethodPost, path: "/consumer/auth/google", role: serviceRoleIdentityAuth, auth: gatewayAuthPublicTenant},
		{method: fiber.MethodPost, path: "/consumer/check_user", role: serviceRoleIdentityAuth, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/kyc", role: serviceRoleIdentityAuth, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/auth/complete_profile", role: serviceRoleIdentityAuth, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/consumer/auth/me", role: serviceRoleIdentityAuth, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/consumer/user", role: serviceRoleIdentityAuth, auth: gatewayAuthUser},
		{method: fiber.MethodPut, path: "/consumer/user", role: serviceRoleIdentityAuth, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/consumer/user/lang", role: serviceRoleIdentityAuth, auth: gatewayAuthUser},
		{method: fiber.MethodPut, path: "/consumer/user/lang", role: serviceRoleIdentityAuth, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/user/device", role: serviceRoleIdentityAuth, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/change_password", role: serviceRoleIdentityAuth, auth: gatewayAuthUser},

		{method: fiber.MethodGet, path: "/consumer/cards", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodPatch, path: "/consumer/cards/:card_id", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodDelete, path: "/consumer/cards/:card_id", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodPut, path: "/consumer/cards/:card_id/main", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/cards/enrollment-intents", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/cards/enrollment-intents/:enrollment_id/confirm", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},

		{method: fiber.MethodGet, path: "/consumer/get_cards", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/add_card", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodPut, path: "/consumer/edit_card", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodDelete, path: "/consumer/delete_card", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/cards/set_main", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/consumer/payment_token", role: serviceRoleCardVault, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/payment_token", role: serviceRoleCardVault, auth: gatewayAuthUser},

		{method: fiber.MethodPost, path: "/consumer/card_info", role: serviceRoleEBSAdapter, auth: gatewayAuthPublicTenant},
		{method: fiber.MethodPost, path: "/consumer/otp/balance", role: serviceRoleEBSAdapter, auth: gatewayAuthPublicTenant},
		{method: fiber.MethodPost, path: "/consumer/register_with_card", role: serviceRoleEBSAdapter, auth: gatewayAuthPublicTenant},
		{method: fiber.MethodPost, path: "/consumer/cards/new", role: serviceRoleEBSAdapter, auth: gatewayAuthPublicTenant},
		{method: fiber.MethodPost, path: "/consumer/cards/complete", role: serviceRoleEBSAdapter, auth: gatewayAuthPublicTenant},
		{method: fiber.MethodGet, path: "/consumer/nec2name", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/balance", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/status", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/is_alive", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/bill_payment", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/bills", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/consumer/biller", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/bill_inquiry", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/p2p", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/cashIn", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/cashOut", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/account", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/purchase", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/n/status", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/key", role: serviceRoleEBSAdapter, auth: gatewayAuthPublicTenant},
		{method: fiber.MethodPost, path: "/consumer/ipin", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/generate_qr", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/qr_payment", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/qr_status", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/qr_refund", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/qr_complete", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/ipin_key", role: serviceRoleEBSAdapter, auth: gatewayAuthPublicTenant},
		{method: fiber.MethodPost, path: "/consumer/generate_ipin", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/complete_ipin", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/vouchers/generate", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/consumer/transaction", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/consumer/transactions", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/p2p_mobile", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/payment_token/quick_pay", role: serviceRoleEBSAdapter, auth: gatewayAuthUser},

		{method: fiber.MethodPost, path: "/consumer/beneficiary", role: serviceRoleBeneficiary, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/consumer/beneficiary", role: serviceRoleBeneficiary, auth: gatewayAuthUser},
		{method: fiber.MethodDelete, path: "/consumer/beneficiary", role: serviceRoleBeneficiary, auth: gatewayAuthUser},

		{method: fiber.MethodGet, path: "/ws", role: serviceRoleNotification, auth: gatewayAuthUser, websocket: true},
		{method: fiber.MethodGet, path: "/consumer/notifications", role: serviceRoleNotification, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/consumer/submit_contacts", role: serviceRoleNotification, auth: gatewayAuthUser},

		{method: fiber.MethodPost, path: "/psp/webhooks/:provider", role: serviceRolePSPWebhook, auth: gatewayAuthPublicQueryTenant},

		{method: fiber.MethodGet, path: "/wallet/methods", role: serviceRoleWalletAPI, auth: gatewayAuthUser},
		{method: fiber.MethodPost, path: "/wallet/wallets", role: serviceRoleWalletAPI, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/wallet/wallets/:id/transactions", role: serviceRoleWalletAPI, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/wallet/wallets/:id", role: serviceRoleWalletAPI, auth: gatewayAuthUser},
		{method: fiber.MethodGet, path: "/admin/wallet", role: serviceRoleWalletAPI, auth: gatewayAuthAdminWalletQueryTenant},
		{method: fiber.MethodGet, path: "/admin/wallet/", role: serviceRoleWalletAPI, auth: gatewayAuthAdminWalletQueryTenant},
		{method: fiber.MethodGet, path: "/admin/wallet/wallets", role: serviceRoleWalletAPI, auth: gatewayAuthAdminWalletQueryTenant},
		{method: fiber.MethodGet, path: "/admin/wallet/wallets/:id", role: serviceRoleWalletAPI, auth: gatewayAuthAdminWalletQueryTenant},
		{method: fiber.MethodGet, path: "/admin/wallet/transactions", role: serviceRoleWalletAPI, auth: gatewayAuthAdminWalletQueryTenant},
		{method: fiber.MethodGet, path: "/admin/wallet/transactions/:client_reference", role: serviceRoleWalletAPI, auth: gatewayAuthAdminWalletQueryTenant},
		{method: fiber.MethodGet, path: "/admin/wallet/pending", role: serviceRoleWalletAPI, auth: gatewayAuthAdminWalletQueryTenant},
		{method: fiber.MethodGet, path: "/admin/wallet/manual", role: serviceRoleWalletAPI, auth: gatewayAuthAdminWalletQueryTenant},
		{method: fiber.MethodPost, path: "/admin/wallet/manual", role: serviceRoleWalletAPI, auth: gatewayAuthAdminWalletFormTenant},
		{method: fiber.MethodGet, path: "/admin/wallet/manual/:workflow_id", role: serviceRoleWalletAPI, auth: gatewayAuthAdminWalletQueryTenant},
		{method: fiber.MethodGet, path: "/admin/wallet/fees", role: serviceRoleWalletAPI, auth: gatewayAuthAdminWalletQueryTenant},
		{method: fiber.MethodPost, path: "/admin/wallet/fees", role: serviceRoleWalletAPI, auth: gatewayAuthAdminWalletFormTenant},
		{method: fiber.MethodGet, path: "/admin/wallet/rates", role: serviceRoleWalletAPI, auth: gatewayAuthAdminWalletQueryTenant},
		{method: fiber.MethodPost, path: "/admin/wallet/rates", role: serviceRoleWalletAPI, auth: gatewayAuthAdminWalletFormTenant},
		{method: fiber.MethodPost, path: "/admin/wallet/approve/:workflow_id", role: serviceRoleWalletAPI, auth: gatewayAuthAdminWalletFormTenant},
		{method: fiber.MethodPost, path: "/admin/wallet/reject/:workflow_id", role: serviceRoleWalletAPI, auth: gatewayAuthAdminWalletFormTenant},
		{method: fiber.MethodGet, path: "/admin/wallet/audit", role: serviceRoleWalletAPI, auth: gatewayAuthAdminWalletQueryTenant},

		{method: fiber.MethodGet, path: "/dashboard/assets/*", role: serviceRoleAdminReporting, auth: gatewayAuthPublic},
		{method: fiber.MethodGet, path: "/dashboard", role: serviceRoleAdminReporting, auth: gatewayAuthAdminTenant},
		{method: fiber.MethodGet, path: "/dashboard/", role: serviceRoleAdminReporting, auth: gatewayAuthAdminTenant},
		{method: fiber.MethodGet, path: "/dashboard/get_tid", role: serviceRoleAdminReporting, auth: gatewayAuthAdminTenant},
		{method: fiber.MethodGet, path: "/dashboard/get", role: serviceRoleAdminReporting, auth: gatewayAuthAdminTenant},
		{method: fiber.MethodGet, path: "/dashboard/all", role: serviceRoleAdminReporting, auth: gatewayAuthAdminTenant},
		{method: fiber.MethodGet, path: "/dashboard/all/:id", role: serviceRoleAdminReporting, auth: gatewayAuthAdminTenant},
		{method: fiber.MethodGet, path: "/dashboard/count", role: serviceRoleAdminReporting, auth: gatewayAuthAdminTenant},
		{method: fiber.MethodGet, path: "/dashboard/settlement", role: serviceRoleAdminReporting, auth: gatewayAuthAdminTenant},
		{method: fiber.MethodGet, path: "/dashboard/merchant", role: serviceRoleAdminReporting, auth: gatewayAuthAdminTenant},
		{method: fiber.MethodGet, path: "/dashboard/merchant/:id", role: serviceRoleAdminReporting, auth: gatewayAuthAdminTenant},
		{method: fiber.MethodGet, path: "/dashboard/status", role: serviceRoleAdminReporting, auth: gatewayAuthAdminTenant},
		{method: fiber.MethodGet, path: "/dashboard/test_browser", role: serviceRoleAdminReporting, auth: gatewayAuthAdminTenant},
		{method: fiber.MethodGet, path: "/dashboard/stream", role: serviceRoleAdminReporting, auth: gatewayAuthAdminTenant},
	}
}
