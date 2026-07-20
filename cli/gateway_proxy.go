package main

import (
	"crypto/tls"
	"encoding/base64"
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
	"github.com/adonese/noebs/internal/backofficeauth"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/adonese/noebs/internal/transactionauth"
	"github.com/adonese/noebs/internal/workloadauth"
	fastws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/proxy"
	"github.com/valyala/fasthttp"
)

type gatewayAuthMode int

const (
	gatewayAuthPublic gatewayAuthMode = iota
	gatewayAuthMobilePrincipal
	gatewayAuthMobileUser
	gatewayAuthTenantWebhook
)

type gatewayRouteSpec struct {
	method         string
	path           string
	upstreamPath   string
	capabilityPath string
	role           serviceRole
	auth           gatewayAuthMode
	transaction    transactionauth.Operation
	websocket      bool
}

const gatewayWebSocketHandshakeTimeout = 10 * time.Second

func registerAPIGatewayProxyRoutes(
	route *fiber.App,
	cfg ebs_fields.NoebsConfig,
	catalog tenantcatalog.Catalog,
	transactionAuthorization *walletAuthorizationHTTP,
) error {
	if oidcVerifier == nil {
		return errors.New("OIDC verifier is not initialized")
	}
	mobileAuth, err := gateway.NewOIDCAuthMiddleware(gateway.OIDCAuthConfig{
		Verifier:       oidcVerifier,
		SelectTenant:   selectActiveTenant(catalog),
		AllowedClients: []string{"noebs-mobile"},
		AllowedRoles:   []tenantauth.Role{tenantauth.RoleUser},
	})
	if err != nil {
		return fmt.Errorf("configure mobile OIDC authorization: %w", err)
	}
	profileResolver, err := newIdentityProfileProjectionResolver(cfg, workloadSigners)
	if err != nil {
		return fmt.Errorf("configure identity profile projection resolver: %w", err)
	}
	if err := registerWalletAuthorizationRoutes(
		route,
		transactionAuthorization,
		mobileAuth,
		propagateGatewayOIDCPrincipal(profileResolver),
	); err != nil {
		return fmt.Errorf("configure wallet transaction authorization: %w", err)
	}
	webhookResolver, err := newGatewayWebhookResolver(cfg.PSPWebhookRoutes, catalog)
	if err != nil {
		return fmt.Errorf("configure PSP webhook routes: %w", err)
	}
	proxies := map[serviceRole]fiber.Handler{}
	for _, spec := range gatewayProxyRouteSpecs() {
		handler, ok := proxies[spec.role]
		if !ok {
			target, err := serviceDiscoveryEndpoint(cfg, spec.role)
			if err != nil {
				return err
			}
			handler = gatewayProxyHandler(target, internalTransportClientTLS)
			proxies[spec.role] = handler
		}
		if spec.websocket {
			target, err := serviceDiscoveryEndpoint(cfg, spec.role)
			if err != nil {
				return err
			}
			handler = gatewayWebSocketProxyHandler(target, internalTransportClientTLS)
		}

		handlers := make([]fiber.Handler, 0, 8)
		handlers = append(handlers, captureWalletAuthorizationHeader, clearGatewayIdentityHeaders)
		switch spec.auth {
		case gatewayAuthPublic:
			handlers = append(handlers, clearPublicCredentialHeaders)
		case gatewayAuthMobilePrincipal:
			handlers = append(handlers, mobileAuth, propagateGatewayOIDCPrincipal(nil))
		case gatewayAuthMobileUser:
			handlers = append(handlers, mobileAuth, propagateGatewayOIDCPrincipal(profileResolver))
		case gatewayAuthTenantWebhook:
			handlers = append(handlers, clearPublicCredentialHeaders, webhookResolver.Resolve)
		default:
			return fmt.Errorf("unknown gateway auth mode %d for %s %s", spec.auth, spec.method, spec.path)
		}
		if spec.transaction != "" {
			if transactionAuthorization == nil || !spec.transaction.Valid() {
				return fmt.Errorf("invalid transaction authorization for %s %s", spec.method, spec.path)
			}
			handlers = append(handlers, transactionAuthorization.requireIntent(spec.transaction))
		}
		if spec.upstreamPath != "" {
			handlers = append(handlers, rewriteGatewayPath(spec.upstreamPath))
		}
		handlers = append(handlers, signGatewayWorkload(string(spec.role), workloadSigners))
		handlers = append(handlers, handler)
		route.Add(spec.method, spec.path, handlers...)
	}
	return nil
}

func gatewayWebSocketProxyHandler(endpoint string, tlsConfig *tls.Config) fiber.Handler {
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

		expiresUnix, err := strconv.ParseInt(c.Get(gateway.GatewayTokenExpiresAtHeader), 10, 64)
		if err != nil {
			return fiber.NewError(http.StatusUnauthorized, "invalid gateway token expiry")
		}
		headers := make(http.Header)
		headers.Set(workloadauth.HeaderRequestID, c.Get(workloadauth.HeaderRequestID))
		for _, name := range workloadauth.IdentityHeaderNames() {
			headers.Set(name, c.Get(name))
		}
		for _, name := range workloadauth.WorkloadHeaderNames() {
			if value := strings.TrimSpace(c.Get(name)); value != "" {
				headers.Set(name, value)
			}
		}
		dialer := fastws.Dialer{
			HandshakeTimeout: gatewayWebSocketHandshakeTimeout,
			Subprotocols:     splitWebSocketSubprotocols(c.Get("Sec-WebSocket-Protocol")),
			TLSClientConfig:  cloneTLSConfig(tlsConfig),
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
			proxyWebSocketConnections(client, upstream, time.Unix(expiresUnix, 0))
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

func proxyWebSocketConnections(client, upstream *fastws.Conn, expiresAt time.Time) {
	done := make(chan error, 2)
	go func() { done <- relayWebSocket(upstream, client) }()
	go func() { done <- relayWebSocket(client, upstream) }()
	timer := time.NewTimer(time.Until(expiresAt))
	completed := 0
	select {
	case <-done:
		completed = 1
	case <-timer.C:
		deadline := time.Now().Add(time.Second)
		message := fastws.FormatCloseMessage(fastws.ClosePolicyViolation, "access token expired")
		_ = client.WriteControl(fastws.CloseMessage, message, deadline)
		_ = upstream.WriteControl(fastws.CloseMessage, message, deadline)
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	_ = client.Close()
	_ = upstream.Close()
	for completed < 2 {
		<-done
		completed++
	}
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

func gatewayProxyHandler(endpoint string, tlsConfig *tls.Config) fiber.Handler {
	client := &fasthttp.Client{
		NoDefaultUserAgentHeader: true,
		DisablePathNormalizing:   true,
		TLSConfig:                cloneTLSConfig(tlsConfig),
	}
	return func(c *fiber.Ctx) error {
		target := endpoint + string(c.Context().RequestURI())
		if err := proxy.Do(c, target, client); err != nil {
			return fiber.NewError(http.StatusBadGateway, err.Error())
		}
		return nil
	}
}

func cloneTLSConfig(config *tls.Config) *tls.Config {
	if config == nil {
		return nil
	}
	return config.Clone()
}

func clearGatewayIdentityHeaders(c *fiber.Ctx) error {
	for _, name := range workloadauth.WorkloadHeaderNames() {
		c.Request().Header.Del(name)
	}
	for _, name := range workloadauth.IdentityHeaderNames() {
		c.Request().Header.Del(name)
	}
	c.Request().Header.Del(backofficeauth.HeaderCSRFToken)
	return c.Next()
}

func signGatewayWorkload(audience string, signers *workloadauth.SignerSet) fiber.Handler {
	return func(c *fiber.Ctx) error {
		req, err := fiberWorkloadRequest(c)
		if err != nil {
			return fiber.NewError(http.StatusBadGateway, "cannot sign internal request")
		}
		if err := signers.Sign(audience, req, c.Body()); err != nil {
			return fiber.NewError(http.StatusBadGateway, "cannot sign internal request")
		}
		for _, name := range workloadauth.WorkloadHeaderNames() {
			c.Request().Header.Set(name, req.Header.Get(name))
		}
		return c.Next()
	}
}

func clearPublicCredentialHeaders(c *fiber.Ctx) error {
	stripPublicCredentialHeaders(c)
	return c.Next()
}

func stripPublicCredentialHeaders(c *fiber.Ctx) {
	c.Request().Header.Del("Authorization")
	c.Request().Header.Del("X-Active-Tenant")
	c.Request().Header.Del("X-Tenant-ID")
	c.Request().Header.Del("X-Admin-Key")
	c.Request().Header.Del("X-Admin-Role")
	c.Request().Header.Del("X-Admin-Permissions")
}

func selectActiveTenant(catalog tenantcatalog.Catalog) func(*fiber.Ctx) (string, error) {
	return func(c *fiber.Ctx) (string, error) {
		values := c.Request().Header.PeekAll("X-Active-Tenant")
		if len(values) != 1 {
			return "", tenantauth.ErrMissingTenant
		}
		tenantID, err := canonicalTenantID(catalog, string(values[0]))
		if err != nil {
			return "", tenantauth.ErrUnknownTenant
		}
		return tenantID, nil
	}
}

func canonicalTenantID(catalog tenantcatalog.Catalog, raw string) (string, error) {
	tenant, err := catalog.Require(raw)
	if err != nil {
		return "", err
	}
	return string(tenant.ID), nil
}

func propagateGatewayOIDCPrincipal(resolver profileProjectionResolver) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if c.Request().URI().QueryArgs().Has("tenant_id") || c.Request().PostArgs().Has("tenant_id") {
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{
				"code":    "unexpected_tenant_id",
				"message": "tenant_id is not accepted on authenticated routes",
			})
		}
		principal, ok := gateway.OIDCPrincipal(c)
		if !ok {
			return fiber.NewError(http.StatusUnauthorized, "missing OIDC principal")
		}
		sourceIP, err := gatewayRequestSource(c)
		if err != nil {
			return fiber.NewError(http.StatusBadRequest, "invalid request source")
		}
		userID := int64(0)
		if resolver != nil {
			resolved, err := resolver.Resolve(
				c.UserContext(),
				principal,
				c.Get(workloadauth.HeaderRequestID),
				sourceIP,
			)
			if errors.Is(err, errProfileProjectionNotFound) {
				return c.Status(http.StatusForbidden).JSON(fiber.Map{
					"code":    "profile_required",
					"message": "profile projection is required",
				})
			}
			if err != nil {
				return fiber.NewError(http.StatusBadGateway, "identity profile projection unavailable")
			}
			userID = resolved
		}
		headers := make(http.Header)
		if err := setGatewayPrincipalHeaders(headers, principal, "", userID, sourceIP); err != nil {
			return fiber.NewError(http.StatusUnauthorized, "invalid OIDC principal")
		}
		for _, name := range workloadauth.IdentityHeaderNames() {
			c.Request().Header.Set(name, headers.Get(name))
		}
		stripPublicCredentialHeaders(c)
		return c.Next()
	}
}

type gatewayWebhookResolver struct {
	routes map[string]ebs_fields.PSPWebhookRoute
}

func newGatewayWebhookResolver(routes map[string]ebs_fields.PSPWebhookRoute, catalog tenantcatalog.Catalog) (*gatewayWebhookResolver, error) {
	if err := validatePSPWebhookRoutes(routes); err != nil {
		return nil, err
	}
	cloned := make(map[string]ebs_fields.PSPWebhookRoute, len(routes))
	for callbackID, route := range routes {
		if _, err := catalog.Require(route.TenantID); err != nil {
			return nil, fmt.Errorf("PSP webhook callback %q has unknown tenant: %w", callbackID, err)
		}
		cloned[callbackID] = route
	}
	return &gatewayWebhookResolver{routes: cloned}, nil
}

func validatePSPWebhookRoutes(routes map[string]ebs_fields.PSPWebhookRoute) error {
	if len(routes) == 0 {
		return errors.New("noebs.psp_webhook_routes is required")
	}
	pairs := make(map[string]struct{}, len(routes))
	for callbackID, route := range routes {
		decoded, err := base64.RawURLEncoding.DecodeString(callbackID)
		if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != callbackID {
			return errors.New("PSP webhook callback IDs must be canonical 32-byte base64url values")
		}
		tenantID, err := tenantcatalog.ParseID(route.TenantID)
		if err != nil || string(tenantID) != route.TenantID {
			return fmt.Errorf("PSP webhook callback %q has invalid tenant", callbackID)
		}
		providerID, err := tenantcatalog.ParseID(route.ProviderCode)
		if err != nil || string(providerID) != route.ProviderCode {
			return fmt.Errorf("PSP webhook callback %q has invalid provider", callbackID)
		}
		pair := route.TenantID + "\x00" + route.ProviderCode
		if _, exists := pairs[pair]; exists {
			return fmt.Errorf("PSP webhook route %s/%s has more than one callback", route.TenantID, route.ProviderCode)
		}
		pairs[pair] = struct{}{}
	}
	return nil
}

func (r *gatewayWebhookResolver) Resolve(c *fiber.Ctx) error {
	if len(c.Request().URI().QueryString()) != 0 {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{
			"code":    "unexpected_webhook_query",
			"message": "webhook query parameters are not accepted",
		})
	}
	callbackID := c.Params("callback_id")
	route, ok := r.routes[callbackID]
	if !ok {
		return c.SendStatus(http.StatusNotFound)
	}
	c.Request().Header.Set(workloadauth.HeaderTenantID, route.TenantID)
	sourceIP, err := gatewayRequestSource(c)
	if err != nil {
		return fiber.NewError(http.StatusBadRequest, "invalid request source")
	}
	c.Request().Header.Set(workloadauth.HeaderSourceIP, sourceIP)
	c.Request().SetRequestURI("/psp/webhooks/" + url.PathEscape(route.ProviderCode))
	return c.Next()
}

func gatewayRequestSource(c *fiber.Ctx) (string, error) {
	values := c.Request().Header.PeekAll(fiber.HeaderXForwardedFor)
	if len(values) != 1 {
		return "", errors.New("gateway request source must have one value")
	}
	source := string(values[0])
	ip := net.ParseIP(source)
	if ip == nil || ip.String() != source {
		return "", errors.New("gateway request source must be a canonical IP address")
	}
	return source, nil
}

func gatewayProxyRouteSpecs() []gatewayRouteSpec {
	return []gatewayRouteSpec{
		{method: fiber.MethodPost, path: "/consumer/auth/profile", role: serviceRoleIdentityAuth, auth: gatewayAuthMobilePrincipal},
		{method: fiber.MethodPost, path: "/consumer/kyc", role: serviceRoleIdentityAuth, auth: gatewayAuthMobileUser},
		{method: fiber.MethodGet, path: "/consumer/user", role: serviceRoleIdentityAuth, auth: gatewayAuthMobileUser},
		{method: fiber.MethodPut, path: "/consumer/user", role: serviceRoleIdentityAuth, auth: gatewayAuthMobileUser},
		{method: fiber.MethodGet, path: "/consumer/user/lang", role: serviceRoleIdentityAuth, auth: gatewayAuthMobileUser},
		{method: fiber.MethodPut, path: "/consumer/user/lang", role: serviceRoleIdentityAuth, auth: gatewayAuthMobileUser},
		{method: fiber.MethodPost, path: "/consumer/user/device", role: serviceRoleIdentityAuth, auth: gatewayAuthMobileUser},

		{method: fiber.MethodGet, path: "/consumer/cards", role: serviceRoleCardVault, auth: gatewayAuthMobileUser},
		{method: fiber.MethodPatch, path: "/consumer/cards/:card_id", role: serviceRoleCardVault, auth: gatewayAuthMobileUser},
		{method: fiber.MethodDelete, path: "/consumer/cards/:card_id", role: serviceRoleCardVault, auth: gatewayAuthMobileUser},
		{method: fiber.MethodPut, path: "/consumer/cards/:card_id/main", role: serviceRoleCardVault, auth: gatewayAuthMobileUser},

		{method: fiber.MethodPost, path: "/consumer/cards/enrollment-intents", role: serviceRoleEBSAdapter, auth: gatewayAuthMobileUser},
		{method: fiber.MethodPost, path: "/consumer/cards/enrollment-intents/:enrollment_id/confirm", role: serviceRoleEBSAdapter, auth: gatewayAuthMobileUser},
		{method: fiber.MethodPost, path: "/consumer/balance", role: serviceRoleEBSAdapter, auth: gatewayAuthMobileUser},
		{method: fiber.MethodPost, path: "/consumer/status", role: serviceRoleEBSAdapter, auth: gatewayAuthMobileUser},
		{method: fiber.MethodPost, path: "/consumer/is_alive", role: serviceRoleEBSAdapter, auth: gatewayAuthMobileUser},
		{method: fiber.MethodGet, path: "/consumer/biller", role: serviceRoleEBSAdapter, auth: gatewayAuthMobileUser},
		{method: fiber.MethodPost, path: "/consumer/n/status", role: serviceRoleEBSAdapter, auth: gatewayAuthMobileUser},
		{method: fiber.MethodGet, path: "/consumer/nec2name", role: serviceRoleEBSAdapter, auth: gatewayAuthMobileUser},
		{method: fiber.MethodPost, path: "/consumer/generate_qr", role: serviceRoleEBSAdapter, auth: gatewayAuthMobileUser},
		{method: fiber.MethodPost, path: "/consumer/qr_status", role: serviceRoleEBSAdapter, auth: gatewayAuthMobileUser},
		{method: fiber.MethodPost, path: "/consumer/qr_refund", role: serviceRoleEBSAdapter, auth: gatewayAuthMobileUser},
		{method: fiber.MethodPost, path: "/consumer/qr_complete", role: serviceRoleEBSAdapter, auth: gatewayAuthMobileUser},
		{method: fiber.MethodGet, path: "/consumer/transaction", role: serviceRoleEBSAdapter, auth: gatewayAuthMobileUser},
		{method: fiber.MethodGet, path: "/consumer/transactions", role: serviceRoleEBSAdapter, auth: gatewayAuthMobileUser},

		{method: fiber.MethodGet, path: "/ws", role: serviceRoleNotification, auth: gatewayAuthMobileUser, websocket: true},

		{method: fiber.MethodPost, path: "/psp/webhooks/:callback_id", capabilityPath: "/psp/webhooks/:provider", role: serviceRolePSPWebhook, auth: gatewayAuthTenantWebhook},

		{method: fiber.MethodGet, path: "/wallet/methods", role: serviceRoleWalletAPI, auth: gatewayAuthMobileUser},
		{method: fiber.MethodPost, path: "/wallet/wallets", role: serviceRoleWalletAPI, auth: gatewayAuthMobileUser},
		{method: fiber.MethodGet, path: "/wallet/wallets/:id/transactions", role: serviceRoleWalletAPI, auth: gatewayAuthMobileUser},
		{method: fiber.MethodGet, path: "/wallet/wallets/:id", role: serviceRoleWalletAPI, auth: gatewayAuthMobileUser},
		{method: fiber.MethodPost, path: "/wallet/deposits", role: serviceRoleWalletAPI, auth: gatewayAuthMobileUser},
		{method: fiber.MethodPost, path: "/wallet/p2p", role: serviceRoleWalletAPI, auth: gatewayAuthMobileUser, transaction: transactionauth.OperationWalletP2P},
		{method: fiber.MethodPost, path: "/wallet/withdrawals", role: serviceRoleWalletAPI, auth: gatewayAuthMobileUser, transaction: transactionauth.OperationWalletWithdrawal},

		{method: fiber.MethodGet, path: "/backoffice/assets/*", upstreamPath: "/dashboard/assets/*", role: serviceRoleAdminReporting, auth: gatewayAuthPublic},
	}
}
