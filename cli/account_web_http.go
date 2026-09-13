package main

import (
	"context"
	_ "embed"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/adonese/noebs/internal/accountenrollment"
	"github.com/adonese/noebs/internal/backofficeauth"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/gofiber/adaptor/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

const (
	accountWebHomePath      = "/account/home"
	accountWebLoginPath     = "/account/login"
	accountWebCallbackPath  = "/account/oauth/callback"
	accountWebLogoutPath    = "/account/logout"
	accountWebLoggedOutPath = "/account/oauth/logout/callback"
)

//go:embed account_web.html
var accountWebHTML string

//go:embed account_web.css
var accountWebCSS string
var accountWebTemplate = template.Must(template.New("account").Parse(accountWebHTML))

type accountWebSession interface {
	BeginLogin(context.Context, string) (backofficeauth.LoginStart, error)
	CompleteLogin(context.Context, string, string, string) (backofficeauth.LoginComplete, error)
	Authenticate(context.Context, string) (backofficeauth.AuthenticatedSession, error)
	Logout(context.Context, string) (backofficeauth.LogoutComplete, error)
}

type accountWebHTTP struct {
	service                            accountWebSession
	cookies                            *backofficeauth.CookiePolicy
	csrf                               *backofficeauth.CSRFProtector
	host, issuer, tenantID, tenantName string
	enrollment                         func(context.Context, tenantauth.Identity, string, string, string, bool) (accountenrollment.Context, error)
	profiles                           accountWebProfiles
}

type accountWebRequestKey struct{}
type accountWebRequestInfo struct{ ID, SourceIP string }

type accountWebPage struct {
	Stage, CSRF, Message, TenantName, Fullname string
}

func registerAccountWebRoutes(router *fiber.App, h *accountWebHTTP) error {
	if router == nil || h == nil || h.service == nil || h.cookies == nil || h.csrf == nil || h.host == "" || h.issuer == "" || h.enrollment == nil || h.profiles == nil || h.tenantID == "" || h.tenantName == "" {
		return backofficeauth.ErrInvalidConfiguration
	}
	wrap := func(handler http.HandlerFunc) fiber.Handler {
		return func(c *fiber.Ctx) error {
			source, err := gatewayRequestSource(c)
			if err != nil {
				return fiber.NewError(http.StatusBadRequest, "invalid request source")
			}
			info := accountWebRequestInfo{ID: uuid.NewString(), SourceIP: source}
			return adaptor.HTTPHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handler(w, r.WithContext(context.WithValue(r.Context(), accountWebRequestKey{}, info)))
			})(c)
		}
	}
	for _, path := range []string{"/account", "/account/", accountWebHomePath} {
		router.Add(http.MethodGet, path, wrap(h.home))
	}
	router.Add(http.MethodGet, accountWebLoginPath, wrap(h.login))
	router.Add(http.MethodGet, accountWebCallbackPath, wrap(h.callback))
	router.Add(http.MethodGet, accountWebLoggedOutPath, wrap(h.loggedOut))
	router.Add(http.MethodPost, accountWebLogoutPath, wrap(h.logout))
	router.Add(http.MethodPost, "/account/enrollment", wrap(h.enroll))
	router.Add(http.MethodPost, "/account/profile", wrap(h.createProfile))
	router.Get("/account/style.css", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/css; charset=utf-8")
		c.Set("X-Content-Type-Options", "nosniff")
		return c.SendString(accountWebCSS)
	})
	return nil
}

func (h *accountWebHTTP) validRequest(w http.ResponseWriter, r *http.Request) bool {
	noStore(w)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	if r.Host != h.host || r.URL.RawPath != "" {
		h.failure(w, http.StatusBadRequest, "This account link is invalid. Start again to continue.")
		return false
	}
	return true
}

func (h *accountWebHTTP) render(w http.ResponseWriter, status int, page accountWebPage) {
	noStore(w)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = accountWebTemplate.Execute(w, page)
}

func (h *accountWebHTTP) failure(w http.ResponseWriter, status int, message string) {
	h.render(w, status, accountWebPage{Stage: "error", Message: message})
}

func (h *accountWebHTTP) setupFailure(w http.ResponseWriter, err error, csrf string) {
	status, message := http.StatusServiceUnavailable, "Account setup is temporarily unavailable. Open account setup to try again."
	var rejected *accountEnrollmentHTTPError
	if errors.As(err, &rejected) && rejected.Status == http.StatusForbidden {
		status, message = http.StatusForbidden, "This sign-in does not have access to this account. You can sign out and use another sign-in."
	}
	h.render(w, status, accountWebPage{Stage: "error", Message: message, CSRF: csrf})
}

func (h *accountWebHTTP) authenticate(ctx context.Context, r *http.Request) (backofficeauth.AuthenticatedSession, error) {
	raw, err := h.cookies.ReadSession(r)
	if err != nil {
		return backofficeauth.AuthenticatedSession{}, err
	}
	return h.service.Authenticate(ctx, raw)
}

func (h *accountWebHTTP) context(ctx context.Context, claims tenantauth.Claims, enroll bool) (accountenrollment.Context, error) {
	info, ok := ctx.Value(accountWebRequestKey{}).(accountWebRequestInfo)
	if !ok || info.ID == "" || info.SourceIP == "" {
		return accountenrollment.Context{}, backofficeauth.ErrInvalidInput
	}
	return h.enrollment(ctx, claims.Identity(), h.tenantID, info.ID, info.SourceIP, enroll)
}

func (h *accountWebHTTP) login(w http.ResponseWriter, r *http.Request) {
	if !h.validRequest(w, r) {
		return
	}
	if _, err := exactQuery(r.URL.RawQuery, nil); err != nil {
		h.failure(w, http.StatusBadRequest, "This sign-in link is invalid.")
		return
	}
	ctx, cancel := backofficeRequestContext(r)
	defer cancel()
	h.beginLogin(ctx, w, r, accountWebHomePath)
}

func (h *accountWebHTTP) beginLogin(ctx context.Context, w http.ResponseWriter, r *http.Request, returnPath string) {
	started, err := h.service.BeginLogin(ctx, returnPath)
	if err != nil {
		h.failure(w, http.StatusServiceUnavailable, "Sign-in is temporarily unavailable. Your account setup is saved; try again.")
		return
	}
	authorization, err := url.Parse(started.AuthorizationURL)
	if err != nil {
		h.failure(w, http.StatusServiceUnavailable, "Sign-in is temporarily unavailable.")
		return
	}
	query := authorization.Query()
	query.Set("acr_values", "urn:noebs:acr:primary")
	authorization.RawQuery = query.Encode()
	http.SetCookie(w, started.FlowCookie)
	http.Redirect(w, r, authorization.String(), http.StatusSeeOther)
}

func (h *accountWebHTTP) callback(w http.ResponseWriter, r *http.Request) {
	if !h.validRequest(w, r) {
		return
	}
	values, err := exactQuery(r.URL.RawQuery, map[string]queryCardinality{
		"state": {minimum: 1, maximum: 1}, "code": {minimum: 1, maximum: 1},
		"iss": {minimum: 1, maximum: 1}, "session_state": {minimum: 0, maximum: 1},
	})
	if err != nil || values.Get("state") == "" || values.Get("code") == "" || values.Get("iss") != h.issuer || (len(values["session_state"]) == 1 && values.Get("session_state") == "") {
		h.failure(w, http.StatusBadRequest, "Sign-in was not completed. Start again to continue.")
		return
	}
	browser, err := h.cookies.ReadFlow(r)
	if err != nil {
		h.failure(w, http.StatusBadRequest, "This sign-in attempt expired. Start again to continue.")
		return
	}
	ctx, cancel := backofficeRequestContext(r)
	defer cancel()
	completed, err := h.service.CompleteLogin(ctx, values.Get("state"), browser, values.Get("code"))
	if err != nil {
		h.failure(w, http.StatusUnauthorized, "This sign-in attempt could not be verified. Start again to continue.")
		return
	}
	http.SetCookie(w, completed.ClearFlowCookie)
	http.SetCookie(w, completed.SessionCookie)
	http.Redirect(w, r, completed.ReturnPath, http.StatusSeeOther)
}

func (h *accountWebHTTP) home(w http.ResponseWriter, r *http.Request) {
	if !h.validRequest(w, r) {
		return
	}
	if _, err := exactQuery(r.URL.RawQuery, nil); err != nil {
		h.failure(w, http.StatusBadRequest, "This account link is invalid.")
		return
	}
	ctx, cancel := backofficeRequestContext(r)
	defer cancel()
	session, err := h.authenticate(ctx, r)
	if err != nil {
		h.render(w, http.StatusOK, accountWebPage{Stage: "signin"})
		return
	}
	state, err := h.context(ctx, session.Claims, false)
	if err != nil {
		h.setupFailure(w, err, session.CSRFToken)
		return
	}
	page := accountWebPage{CSRF: session.CSRFToken, TenantName: h.tenantName}
	if state.RefreshRequired {
		page.Stage = "refresh"
	} else if state.EnrollmentStatus != "complete" {
		page.Stage = "enrollment"
	} else {
		principal, err := h.accountPrincipal(session.Claims, state)
		if err != nil {
			// Membership can change while the browser still holds an older access
			// token. Refresh only after the authority confirms this exact account;
			// an empty or revoked context must remain denied.
			if h.contextAdmitsAccount(state) {
				page.Stage = "refresh"
				h.render(w, http.StatusOK, page)
				return
			}
			h.render(w, http.StatusForbidden, accountWebPage{Stage: "error", Message: "This sign-in does not currently have access to this account. You can sign out and use another sign-in.", CSRF: session.CSRFToken})
			return
		}
		info := ctx.Value(accountWebRequestKey{}).(accountWebRequestInfo)
		_, err = h.profiles.Resolve(ctx, principal, info.ID, info.SourceIP)
		page.Stage = "ready"
		if errors.Is(err, errProfileProjectionNotFound) {
			page.Stage = "profile"
			page.Fullname = state.SuggestedFullname
		} else if err != nil {
			h.failure(w, http.StatusServiceUnavailable, "Your details could not be loaded. Open account setup to retry.")
			return
		}
	}
	h.render(w, http.StatusOK, page)
}

func (h *accountWebHTTP) validateForm(w http.ResponseWriter, r *http.Request, session backofficeauth.AuthenticatedSession, names ...string) bool {
	if _, err := exactQuery(r.URL.RawQuery, nil); err != nil {
		h.failure(w, http.StatusBadRequest, "This account request is invalid.")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		h.failure(w, http.StatusBadRequest, "This account request is invalid.")
		return false
	}
	allowed := map[string]bool{"_csrf": true}
	for _, name := range names {
		allowed[name] = true
	}
	if len(r.PostForm) != len(allowed) {
		h.failure(w, http.StatusBadRequest, "This account request is invalid.")
		return false
	}
	for name, values := range r.PostForm {
		if !allowed[name] || len(values) != 1 {
			h.failure(w, http.StatusBadRequest, "This account request is invalid.")
			return false
		}
	}
	if err := h.csrf.ValidateMutation(r, r.PostForm.Get("_csrf"), session.CSRFToken); err != nil {
		h.failure(w, http.StatusForbidden, "Your account session changed. Open account setup again to continue.")
		return false
	}
	return true
}

func (h *accountWebHTTP) enroll(w http.ResponseWriter, r *http.Request) {
	if !h.validRequest(w, r) {
		return
	}
	ctx, cancel := backofficeRequestContext(r)
	defer cancel()
	session, err := h.authenticate(ctx, r)
	if err != nil {
		h.failure(w, http.StatusUnauthorized, "Sign in again to resume your account setup.")
		return
	}
	if !h.validateForm(w, r, session) {
		return
	}
	state, err := h.context(ctx, session.Claims, true)
	if err != nil {
		h.setupFailure(w, err, session.CSRFToken)
		return
	}
	if state.RefreshRequired {
		h.beginLogin(ctx, w, r, accountWebHomePath)
		return
	}
	http.Redirect(w, r, accountWebHomePath, http.StatusSeeOther)
}

func (h *accountWebHTTP) contextAdmitsAccount(state accountenrollment.Context) bool {
	return !state.RefreshRequired && state.EnrollmentStatus == "complete" &&
		len(state.Tenants) == 1 && state.Tenants[0].ID == h.tenantID
}

func (h *accountWebHTTP) accountPrincipal(claims tenantauth.Claims, state accountenrollment.Context) (tenantauth.Principal, error) {
	if !h.contextAdmitsAccount(state) {
		return tenantauth.Principal{}, tenantauth.ErrForbidden
	}
	return tenantauth.Authorize(claims, h.tenantID, tenantauth.RoleUser)
}

func (h *accountWebHTTP) createProfile(w http.ResponseWriter, r *http.Request) {
	if !h.validRequest(w, r) {
		return
	}
	ctx, cancel := backofficeRequestContext(r)
	defer cancel()
	session, err := h.authenticate(ctx, r)
	if err != nil {
		h.failure(w, http.StatusUnauthorized, "Sign in again to resume your account setup.")
		return
	}
	if !h.validateForm(w, r, session, "fullname") {
		return
	}
	state, err := h.context(ctx, session.Claims, false)
	if err != nil {
		h.failure(w, http.StatusServiceUnavailable, "Account setup is temporarily unavailable. Try again.")
		return
	}
	principal, err := h.accountPrincipal(session.Claims, state)
	if err != nil {
		h.failure(w, http.StatusForbidden, "This account is unavailable. Open account setup to check your access.")
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("fullname"))
	if name == "" || len([]byte(name)) > 256 {
		h.failure(w, http.StatusBadRequest, "Enter a valid full name. Open account setup to try again.")
		return
	}
	info := ctx.Value(accountWebRequestKey{}).(accountWebRequestInfo)
	_, err = h.profiles.Resolve(ctx, principal, info.ID, info.SourceIP)
	if errors.Is(err, errProfileProjectionNotFound) {
		err = h.profiles.Create(ctx, principal, name, info.ID, info.SourceIP)
	}
	if err != nil {
		h.failure(w, http.StatusServiceUnavailable, "Your details could not be saved. Open account setup to retry; an existing account will be restored.")
		return
	}
	http.Redirect(w, r, accountWebHomePath, http.StatusSeeOther)
}

func (h *accountWebHTTP) logout(w http.ResponseWriter, r *http.Request) {
	if !h.validRequest(w, r) {
		return
	}
	ctx, cancel := backofficeRequestContext(r)
	defer cancel()
	session, err := h.authenticate(ctx, r)
	if err != nil {
		h.failure(w, http.StatusUnauthorized, "Your session has expired. Open account setup to sign in again.")
		return
	}
	if !h.validateForm(w, r, session) {
		return
	}
	raw, _ := h.cookies.ReadSession(r)
	completed, err := h.service.Logout(ctx, raw)
	if err != nil {
		h.failure(w, http.StatusServiceUnavailable, "Sign-out could not be completed. Try again.")
		return
	}
	http.SetCookie(w, completed.ClearSessionCookie)
	http.Redirect(w, r, completed.EndSessionURL, http.StatusSeeOther)
}

func (h *accountWebHTTP) loggedOut(w http.ResponseWriter, r *http.Request) {
	if !h.validRequest(w, r) {
		return
	}
	if _, err := exactQuery(r.URL.RawQuery, nil); err != nil {
		h.failure(w, http.StatusBadRequest, "This account link is invalid.")
		return
	}
	h.render(w, http.StatusOK, accountWebPage{Stage: "signin", Message: "You are signed out."})
}
