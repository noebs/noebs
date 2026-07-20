package main

import (
	"context"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/adonese/noebs/internal/backofficeauth"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/gofiber/adaptor/v2"
	"github.com/gofiber/fiber/v2"
)

const (
	backofficeLoginPath     = "/backoffice/login"
	backofficeCallbackPath  = "/backoffice/oauth/callback"
	backofficeLogoutPath    = "/backoffice/logout"
	backofficeLoggedOutPath = "/backoffice/oauth/logout/callback"
	backofficeHomePath      = "/backoffice/home"
	backofficeRequestTTL    = 15 * time.Second
)

type backofficeHTTP struct {
	service *backofficeauth.Service
	cookies *backofficeauth.CookiePolicy
	csrf    *backofficeauth.CSRFProtector
	host    string
	issuer  string
	catalog tenantcatalog.Catalog
}

func registerBackofficeLifecycleRoutes(router *fiber.App, handler *backofficeHTTP) error {
	if router == nil || handler == nil || handler.service == nil || handler.cookies == nil || handler.csrf == nil || handler.host == "" || handler.issuer == "" || len(handler.catalog.All()) == 0 {
		return backofficeauth.ErrInvalidConfiguration
	}
	router.Add(fiber.MethodGet, backofficeLoginPath, adaptor.HTTPHandlerFunc(handler.login))
	router.Add(fiber.MethodGet, backofficeCallbackPath, adaptor.HTTPHandlerFunc(handler.callback))
	router.Post(backofficeLogoutPath, adaptor.HTTPHandlerFunc(handler.logout))
	router.Get(backofficeLoggedOutPath, adaptor.HTTPHandlerFunc(handler.loggedOut))
	router.Get(backofficeHomePath, adaptor.HTTPHandlerFunc(handler.home))
	return nil
}

func (h *backofficeHTTP) login(writer http.ResponseWriter, request *http.Request) {
	noStore(writer)
	if !h.canonicalHost(request) {
		backofficeError(writer, http.StatusBadRequest)
		return
	}
	values, err := exactQuery(request.URL.RawQuery, map[string]queryCardinality{
		"return_to": {minimum: 0, maximum: 1},
	})
	if err != nil {
		backofficeError(writer, http.StatusBadRequest)
		return
	}
	returnPath := backofficeHomePath
	if candidates := values["return_to"]; len(candidates) == 1 {
		returnPath = candidates[0]
	}
	ctx, cancel := backofficeRequestContext(request)
	defer cancel()
	started, err := h.service.BeginLogin(ctx, returnPath)
	if err != nil {
		backofficeError(writer, http.StatusBadRequest)
		return
	}
	http.SetCookie(writer, started.FlowCookie)
	http.Redirect(writer, request, started.AuthorizationURL, http.StatusSeeOther)
}

func (h *backofficeHTTP) callback(writer http.ResponseWriter, request *http.Request) {
	noStore(writer)
	if !h.canonicalHost(request) {
		backofficeError(writer, http.StatusBadRequest)
		return
	}
	values, err := exactQuery(request.URL.RawQuery, map[string]queryCardinality{
		"state":         {minimum: 1, maximum: 1},
		"code":          {minimum: 1, maximum: 1},
		"iss":           {minimum: 1, maximum: 1},
		"session_state": {minimum: 0, maximum: 1},
	})
	if err != nil || values["state"][0] == "" || values["code"][0] == "" || values["iss"][0] != h.issuer ||
		(len(values["session_state"]) == 1 && values["session_state"][0] == "") {
		backofficeError(writer, http.StatusBadRequest)
		return
	}
	browserBinding, err := h.cookies.ReadFlow(request)
	if err != nil {
		backofficeError(writer, http.StatusBadRequest)
		return
	}
	ctx, cancel := backofficeRequestContext(request)
	defer cancel()
	completed, err := h.service.CompleteLogin(
		ctx,
		values["state"][0],
		browserBinding,
		values["code"][0],
	)
	if err != nil {
		backofficeError(writer, http.StatusUnauthorized)
		return
	}
	http.SetCookie(writer, completed.ClearFlowCookie)
	http.SetCookie(writer, completed.SessionCookie)
	http.Redirect(writer, request, completed.ReturnPath, http.StatusSeeOther)
}

func (h *backofficeHTTP) logout(writer http.ResponseWriter, request *http.Request) {
	noStore(writer)
	if !h.canonicalHost(request) {
		backofficeError(writer, http.StatusBadRequest)
		return
	}
	if _, err := exactQuery(request.URL.RawQuery, nil); err != nil {
		backofficeError(writer, http.StatusBadRequest)
		return
	}
	rawSession, err := h.cookies.ReadSession(request)
	if err != nil {
		backofficeError(writer, http.StatusUnauthorized)
		return
	}
	ctx, cancel := backofficeRequestContext(request)
	defer cancel()
	authenticated, err := h.service.Authenticate(ctx, rawSession)
	if err != nil {
		backofficeError(writer, http.StatusUnauthorized)
		return
	}
	submitted, err := submittedCSRFToken(request)
	if err != nil || h.csrf.ValidateMutation(request, submitted, authenticated.CSRFToken) != nil {
		backofficeError(writer, http.StatusForbidden)
		return
	}
	completed, err := h.service.Logout(ctx, rawSession)
	if err != nil {
		backofficeError(writer, http.StatusUnauthorized)
		return
	}
	http.SetCookie(writer, completed.ClearSessionCookie)
	http.Redirect(writer, request, completed.EndSessionURL, http.StatusSeeOther)
}

func (h *backofficeHTTP) loggedOut(writer http.ResponseWriter, request *http.Request) {
	noStore(writer)
	if !h.canonicalHost(request) {
		backofficeError(writer, http.StatusBadRequest)
		return
	}
	if _, err := exactQuery(request.URL.RawQuery, nil); err != nil {
		backofficeError(writer, http.StatusBadRequest)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte("<!doctype html><title>Signed out</title><p>You are signed out.</p>"))
}

func (h *backofficeHTTP) home(writer http.ResponseWriter, request *http.Request) {
	noStore(writer)
	if !h.canonicalHost(request) {
		backofficeError(writer, http.StatusBadRequest)
		return
	}
	if _, err := exactQuery(request.URL.RawQuery, nil); err != nil {
		backofficeError(writer, http.StatusBadRequest)
		return
	}
	rawSession, err := h.cookies.ReadSession(request)
	if err != nil {
		h.redirectToLogin(writer, request, backofficeHomePath)
		return
	}
	ctx, cancel := backofficeRequestContext(request)
	defer cancel()
	authenticated, err := h.service.Authenticate(ctx, rawSession)
	if err != nil {
		http.SetCookie(writer, h.cookies.ClearSession())
		h.redirectToLogin(writer, request, backofficeHomePath)
		return
	}
	page := backofficeHomeView{CSRFToken: authenticated.CSRFToken}
	for _, membership := range authenticated.Claims.Memberships() {
		if _, err := h.catalog.Require(membership.TenantID); err != nil {
			continue
		}
		operator := slices.Contains(membership.Roles, tenantauth.RoleBackoffice) ||
			slices.Contains(membership.Roles, tenantauth.RoleTenantAdmin)
		if !operator {
			continue
		}
		entry := backofficeTenantView{TenantID: membership.TenantID}
		if slices.Contains(membership.Permissions, tenantauth.PermissionReportingRead) {
			entry.ReportingURL = backofficeTenantPath(membership.TenantID, "reporting")
		}
		if slices.Contains(membership.Permissions, tenantauth.PermissionWalletRead) {
			entry.WalletURL = backofficeTenantPath(membership.TenantID, "wallet")
		}
		if entry.ReportingURL != "" || entry.WalletURL != "" {
			page.Tenants = append(page.Tenants, entry)
		}
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := backofficeHomeTemplate.Execute(writer, page); err != nil {
		return
	}
}

func (h *backofficeHTTP) redirectToLogin(writer http.ResponseWriter, request *http.Request, returnPath string) {
	location := backofficeLoginPath + "?return_to=" + url.QueryEscape(returnPath)
	http.Redirect(writer, request, location, http.StatusSeeOther)
}

func (h *backofficeHTTP) canonicalHost(request *http.Request) bool {
	return request != nil && request.Host == h.host
}

func backofficeRequestContext(request *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(request.Context(), backofficeRequestTTL)
}

type backofficeHomeView struct {
	CSRFToken string
	Tenants   []backofficeTenantView
}

type backofficeTenantView struct {
	TenantID     string
	ReportingURL string
	WalletURL    string
}

var backofficeHomeTemplate = template.Must(template.New("backoffice-home").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Noebs back office</title><link rel="stylesheet" href="/backoffice/assets/style.css"></head><body>
<main><h1>Noebs back office</h1>{{if .Tenants}}<ul>{{range .Tenants}}<li><strong>{{.TenantID}}</strong>
{{if .ReportingURL}}<a href="{{.ReportingURL}}">Reporting</a>{{end}}
{{if .WalletURL}}<a href="{{.WalletURL}}">Wallet</a>{{end}}</li>{{end}}</ul>{{else}}<p>No authorized tenant tools.</p>{{end}}
<form method="post" action="/backoffice/logout"><input type="hidden" name="_csrf" value="{{.CSRFToken}}"><button type="submit">Sign out</button></form>
</main></body></html>`))

type queryCardinality struct {
	minimum int
	maximum int
}

func exactQuery(raw string, contract map[string]queryCardinality) (url.Values, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, err
	}
	for key, candidates := range values {
		cardinality, ok := contract[key]
		if !ok || len(candidates) < cardinality.minimum || len(candidates) > cardinality.maximum {
			return nil, backofficeauth.ErrInvalidInput
		}
	}
	for key, cardinality := range contract {
		if len(values[key]) < cardinality.minimum || len(values[key]) > cardinality.maximum {
			return nil, backofficeauth.ErrInvalidInput
		}
	}
	return values, nil
}

func submittedCSRFToken(request *http.Request) (string, error) {
	headers := request.Header.Values("X-CSRF-Token")
	if len(headers) > 1 {
		return "", backofficeauth.ErrInvalidInput
	}
	if err := request.ParseForm(); err != nil {
		return "", err
	}
	forms := request.PostForm["_csrf"]
	if len(forms) > 1 || len(headers)+len(forms) != 1 {
		return "", backofficeauth.ErrInvalidInput
	}
	if len(headers) == 1 {
		return headers[0], nil
	}
	return forms[0], nil
}

func noStore(writer http.ResponseWriter) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Pragma", "no-cache")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' https://stackpath.bootstrapcdn.com; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	writer.Header().Set("X-Frame-Options", "DENY")
}

func backofficeError(writer http.ResponseWriter, status int) {
	noStore(writer)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write([]byte(`{"code":"backoffice_authentication_failed","message":"back-office authentication failed"}`))
}
