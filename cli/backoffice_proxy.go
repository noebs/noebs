package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/backofficeauth"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/workloadauth"
	"github.com/gofiber/fiber/v2"
)

type backofficeRouteSpec struct {
	method       string
	path         string
	upstreamPath string
	role         serviceRole
	permission   tenantauth.Permission
	roles        []tenantauth.Role
}

var backofficeReadRoles = []tenantauth.Role{
	tenantauth.RoleBackoffice,
	tenantauth.RoleTenantAdmin,
}

var backofficeWriteRoles = []tenantauth.Role{
	tenantauth.RoleTenantAdmin,
}

func registerBackofficeProxyRoutes(router *fiber.App, cfg ebs_fields.NoebsConfig, handler *backofficeHTTP) error {
	if router == nil || handler == nil || workloadSigners == nil {
		return backofficeauth.ErrInvalidConfiguration
	}
	proxies := make(map[serviceRole]fiber.Handler)
	for _, spec := range backofficeRouteSpecs() {
		policy, err := tenantauth.NewPermissionPolicy(spec.permission, spec.roles...)
		if err != nil {
			return err
		}
		proxyHandler, exists := proxies[spec.role]
		if !exists {
			endpoint, err := serviceDiscoveryEndpoint(cfg, spec.role)
			if err != nil {
				return err
			}
			proxyHandler = gatewayProxyHandler(endpoint, internalTransportClientTLS)
			proxies[spec.role] = proxyHandler
		}
		router.Add(
			spec.method,
			spec.path,
			clearGatewayIdentityHeaders,
			handler.authorizeTenantRoute(spec.permission, policy),
			rewriteGatewayPath(spec.upstreamPath),
			signGatewayWorkload(string(spec.role), workloadSigners),
			proxyHandler,
		)
	}
	return nil
}

func (h *backofficeHTTP) authorizeTenantRoute(permission tenantauth.Permission, policy tenantauth.Policy) fiber.Handler {
	return func(c *fiber.Ctx) error {
		defer setBackofficeFiberHeaders(c)
		if string(c.Request().Header.Host()) != h.host {
			return backofficeFiberError(c, http.StatusBadRequest)
		}
		if c.Request().URI().QueryArgs().Has("tenant_id") || c.Request().PostArgs().Has("tenant_id") {
			return backofficeFiberError(c, http.StatusBadRequest)
		}
		sessionRequest := &http.Request{Header: make(http.Header)}
		for _, value := range c.Request().Header.PeekAll(fiber.HeaderCookie) {
			sessionRequest.Header.Add(fiber.HeaderCookie, string(value))
		}
		rawSession, err := h.cookies.ReadSession(sessionRequest)
		if err != nil {
			return h.requireLogin(c)
		}
		ctx, cancel := context.WithTimeout(c.UserContext(), backofficeRequestTTL)
		defer cancel()
		authenticated, err := h.service.Authenticate(ctx, rawSession)
		if err != nil {
			c.Cookie(fiberCookie(h.cookies.ClearSession()))
			return h.requireLogin(c)
		}
		rawTenant := c.Params("tenant")
		tenantID, err := canonicalTenantID(h.catalog, rawTenant)
		if err != nil {
			return backofficeFiberError(c, http.StatusNotFound)
		}
		principal, err := policy.Authorize(authenticated.Claims, tenantID)
		if err != nil {
			return backofficeFiberError(c, http.StatusForbidden)
		}
		if !backofficeSafeMethod(c.Method()) {
			submitted, request, err := backofficeMutation(c)
			if err != nil || h.csrf.ValidateMutation(request, submitted, authenticated.CSRFToken) != nil {
				return backofficeFiberError(c, http.StatusForbidden)
			}
		}
		sourceIP, err := gatewayRequestSource(c)
		if err != nil {
			return backofficeFiberError(c, http.StatusBadRequest)
		}
		headers := make(http.Header)
		if err := setGatewayPrincipalHeaders(headers, principal, permission, 0, sourceIP); err != nil {
			return backofficeFiberError(c, http.StatusUnauthorized)
		}
		for _, name := range workloadauth.IdentityHeaderNames() {
			c.Request().Header.Set(name, headers.Get(name))
		}
		c.Request().Header.Set(backofficeauth.HeaderCSRFToken, authenticated.CSRFToken)
		stripPublicCredentialHeaders(c)
		c.Request().Header.Del(fiber.HeaderCookie)
		c.Request().Header.Del("X-CSRF-Token")
		c.SetUserContext(ctx)
		return c.Next()
	}
}

func (h *backofficeHTTP) requireLogin(c *fiber.Ctx) error {
	if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
		return backofficeFiberError(c, http.StatusUnauthorized)
	}
	return c.Redirect(
		backofficeLoginPath+"?return_to="+url.QueryEscape(c.Path()),
		http.StatusSeeOther,
	)
}

func backofficeMutation(c *fiber.Ctx) (string, *http.Request, error) {
	headers := c.Request().Header.PeekAll("X-CSRF-Token")
	forms := c.Request().PostArgs().PeekMulti("_csrf")
	if len(headers) > 1 || len(forms) > 1 || len(headers)+len(forms) != 1 {
		return "", nil, backofficeauth.ErrInvalidInput
	}
	submitted := ""
	if len(headers) == 1 {
		submitted = string(headers[0])
	} else {
		submitted = string(forms[0])
	}
	request := &http.Request{Method: c.Method(), Header: make(http.Header)}
	for _, name := range []string{"Origin", "Referer", "Sec-Fetch-Site"} {
		for _, value := range c.Request().Header.PeekAll(name) {
			request.Header.Add(name, string(value))
		}
	}
	return submitted, request, nil
}

func backofficeSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func backofficeFiberError(c *fiber.Ctx, status int) error {
	setBackofficeFiberHeaders(c)
	return c.Status(status).JSON(fiber.Map{
		"code":    "backoffice_authentication_failed",
		"message": "back-office authentication failed",
	})
}

func setBackofficeFiberHeaders(c *fiber.Ctx) {
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set("Pragma", "no-cache")
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Referrer-Policy", "no-referrer")
	c.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' https://stackpath.bootstrapcdn.com; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	c.Set("X-Frame-Options", "DENY")
}

func fiberCookie(cookie *http.Cookie) *fiber.Cookie {
	return &fiber.Cookie{
		Name:     cookie.Name,
		Value:    cookie.Value,
		Path:     cookie.Path,
		Expires:  cookie.Expires,
		MaxAge:   cookie.MaxAge,
		Secure:   cookie.Secure,
		HTTPOnly: cookie.HttpOnly,
		SameSite: "Lax",
	}
}

func rewriteGatewayPath(template string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		segments := strings.Split(strings.TrimPrefix(template, "/"), "/")
		for index, segment := range segments {
			switch {
			case strings.HasPrefix(segment, ":"):
				value := c.Params(strings.TrimPrefix(segment, ":"))
				if !validGatewayPathSegment(value) {
					return c.SendStatus(http.StatusBadRequest)
				}
				segments[index] = url.PathEscape(value)
			case segment == "*":
				wildcard := strings.Split(c.Params("*"), "/")
				if len(wildcard) == 0 {
					return c.SendStatus(http.StatusBadRequest)
				}
				for partIndex, part := range wildcard {
					if !validGatewayPathSegment(part) {
						return c.SendStatus(http.StatusBadRequest)
					}
					wildcard[partIndex] = url.PathEscape(part)
				}
				segments = append(segments[:index], wildcard...)
			}
		}
		target := "/" + strings.Join(segments, "/")
		if query := c.Request().URI().QueryString(); len(query) != 0 {
			target += "?" + string(query)
		}
		c.Request().SetRequestURI(target)
		return c.Next()
	}
}

func validGatewayPathSegment(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("-._~", char) {
			continue
		}
		return false
	}
	return true
}

func backofficeTenantPath(tenantID, area string) string {
	return "/backoffice/t/" + url.PathEscape(tenantID) + "/" + area
}

func backofficeRouteSpecs() []backofficeRouteSpec {
	read := func(method, path, upstream string, role serviceRole, permission tenantauth.Permission) backofficeRouteSpec {
		return backofficeRouteSpec{method: method, path: path, upstreamPath: upstream, role: role, permission: permission, roles: backofficeReadRoles}
	}
	write := func(path, upstream string, permission tenantauth.Permission) backofficeRouteSpec {
		return backofficeRouteSpec{method: http.MethodPost, path: path, upstreamPath: upstream, role: serviceRoleWalletAPI, permission: permission, roles: backofficeWriteRoles}
	}
	routes := []backofficeRouteSpec{
		read(http.MethodGet, "/backoffice/t/:tenant/reporting", "/dashboard", serviceRoleAdminReporting, tenantauth.PermissionReportingRead),
		read(http.MethodGet, "/backoffice/t/:tenant/reporting/", "/dashboard/", serviceRoleAdminReporting, tenantauth.PermissionReportingRead),
		read(http.MethodGet, "/backoffice/t/:tenant/reporting/get_tid", "/dashboard/get_tid", serviceRoleAdminReporting, tenantauth.PermissionReportingRead),
		read(http.MethodGet, "/backoffice/t/:tenant/reporting/get", "/dashboard/get", serviceRoleAdminReporting, tenantauth.PermissionReportingRead),
		read(http.MethodGet, "/backoffice/t/:tenant/reporting/all", "/dashboard/all", serviceRoleAdminReporting, tenantauth.PermissionReportingRead),
		read(http.MethodGet, "/backoffice/t/:tenant/reporting/all/:id", "/dashboard/all/:id", serviceRoleAdminReporting, tenantauth.PermissionReportingRead),
		read(http.MethodGet, "/backoffice/t/:tenant/reporting/count", "/dashboard/count", serviceRoleAdminReporting, tenantauth.PermissionReportingRead),
		read(http.MethodGet, "/backoffice/t/:tenant/reporting/merchant", "/dashboard/merchant", serviceRoleAdminReporting, tenantauth.PermissionReportingRead),
		read(http.MethodGet, "/backoffice/t/:tenant/reporting/status", "/dashboard/status", serviceRoleAdminReporting, tenantauth.PermissionReportingRead),
		read(http.MethodGet, "/backoffice/t/:tenant/reporting/test_browser", "/dashboard/test_browser", serviceRoleAdminReporting, tenantauth.PermissionReportingRead),
		read(http.MethodGet, "/backoffice/t/:tenant/reporting/stream", "/dashboard/stream", serviceRoleAdminReporting, tenantauth.PermissionReportingRead),

		read(http.MethodGet, "/backoffice/t/:tenant/wallet", "/admin/wallet", serviceRoleWalletAPI, tenantauth.PermissionWalletRead),
		read(http.MethodGet, "/backoffice/t/:tenant/wallet/", "/admin/wallet/", serviceRoleWalletAPI, tenantauth.PermissionWalletRead),
		read(http.MethodGet, "/backoffice/t/:tenant/wallet/wallets", "/admin/wallet/wallets", serviceRoleWalletAPI, tenantauth.PermissionWalletRead),
		read(http.MethodGet, "/backoffice/t/:tenant/wallet/wallets/:id", "/admin/wallet/wallets/:id", serviceRoleWalletAPI, tenantauth.PermissionWalletRead),
		read(http.MethodGet, "/backoffice/t/:tenant/wallet/transactions", "/admin/wallet/transactions", serviceRoleWalletAPI, tenantauth.PermissionWalletRead),
		read(http.MethodGet, "/backoffice/t/:tenant/wallet/transactions/:client_reference", "/admin/wallet/transactions/:client_reference", serviceRoleWalletAPI, tenantauth.PermissionWalletRead),
		read(http.MethodGet, "/backoffice/t/:tenant/wallet/pending", "/admin/wallet/pending", serviceRoleWalletAPI, tenantauth.PermissionWalletRead),
		read(http.MethodGet, "/backoffice/t/:tenant/wallet/manual", "/admin/wallet/manual", serviceRoleWalletAPI, tenantauth.PermissionWalletRead),
		read(http.MethodGet, "/backoffice/t/:tenant/wallet/manual/:workflow_id", "/admin/wallet/manual/:workflow_id", serviceRoleWalletAPI, tenantauth.PermissionWalletRead),
		read(http.MethodGet, "/backoffice/t/:tenant/wallet/fees", "/admin/wallet/fees", serviceRoleWalletAPI, tenantauth.PermissionWalletRead),
		read(http.MethodGet, "/backoffice/t/:tenant/wallet/rates", "/admin/wallet/rates", serviceRoleWalletAPI, tenantauth.PermissionWalletRead),
		read(http.MethodGet, "/backoffice/t/:tenant/wallet/audit", "/admin/wallet/audit", serviceRoleWalletAPI, tenantauth.PermissionWalletAuditRead),

		write("/backoffice/t/:tenant/wallet/manual", "/admin/wallet/manual", tenantauth.PermissionWalletManualCreate),
		write("/backoffice/t/:tenant/wallet/fees", "/admin/wallet/fees", tenantauth.PermissionWalletFeesWrite),
		write("/backoffice/t/:tenant/wallet/rates", "/admin/wallet/rates", tenantauth.PermissionWalletRatesWrite),
		write("/backoffice/t/:tenant/wallet/approve/:workflow_id", "/admin/wallet/approve/:workflow_id", tenantauth.PermissionWalletWorkflowApprove),
		write("/backoffice/t/:tenant/wallet/reject/:workflow_id", "/admin/wallet/reject/:workflow_id", tenantauth.PermissionWalletWorkflowReject),
	}
	return routes
}
