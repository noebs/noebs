package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/internal/transactionauth"
	walletrequest "github.com/adonese/noebs/wallet/request"
	"github.com/gofiber/adaptor/v2"
	"github.com/gofiber/fiber/v2"
)

const (
	walletAuthorizationHeader     = "X-Noebs-Transaction-Authorization"
	walletAuthorizationRequestTTL = 15 * time.Second
)

type walletAuthorizationHTTP struct {
	service      *transactionauth.Service
	issuer       string
	host         string
	publicOrigin string
	defaults     walletrequest.Defaults
}

type walletAuthorizationBeginRequest struct {
	Operation transactionauth.Operation `json:"operation"`
	Request   json.RawMessage           `json:"request"`
}

type capturedWalletAuthorizationKey struct{}

var walletAuthorizationLocalKey capturedWalletAuthorizationKey

func registerWalletAuthorizationRoutes(
	router *fiber.App,
	handler *walletAuthorizationHTTP,
	mobileAuth fiber.Handler,
	propagateMobileUser fiber.Handler,
) error {
	if router == nil || handler == nil || handler.service == nil || mobileAuth == nil || propagateMobileUser == nil {
		return transactionauth.ErrInvalidConfiguration
	}
	router.Post(
		"/wallet/authorizations",
		captureWalletAuthorizationHeader,
		clearGatewayIdentityHeaders,
		mobileAuth,
		propagateMobileUser,
		handler.begin,
	)
	router.Add(fiber.MethodGet, walletAuthorizationBrowserStartPath, adaptor.HTTPHandlerFunc(handler.startBrowser))
	router.Add(fiber.MethodGet, walletAuthorizationCallbackPath, adaptor.HTTPHandlerFunc(handler.callback))
	return nil
}

func (h *walletAuthorizationHTTP) begin(c *fiber.Ctx) error {
	noStoreFiber(c)
	if len(c.Request().URI().QueryString()) != 0 {
		return walletAuthorizationFailure(c, http.StatusBadRequest)
	}
	principal, ok := gateway.OIDCPrincipal(c)
	if !ok {
		return walletAuthorizationFailure(c, http.StatusUnauthorized)
	}
	var request walletAuthorizationBeginRequest
	if err := decodeExactJSON(c.Body(), &request); err != nil || !request.Operation.Valid() || len(request.Request) == 0 {
		return walletAuthorizationFailure(c, http.StatusBadRequest)
	}
	canonical, err := walletrequest.ParsePublic(request.Operation, principal.Tenant(), request.Request, h.defaults)
	if err != nil {
		return walletAuthorizationFailure(c, http.StatusBadRequest)
	}
	identity := principal.Identity()
	initiated, err := h.service.Begin(c.UserContext(), transactionauth.Binding{
		TenantID:       principal.Tenant(),
		Issuer:         identity.Issuer,
		Subject:        identity.Subject,
		Operation:      request.Operation,
		RequestDigest:  canonical.Digest,
		IdempotencyKey: canonical.IdempotencyKey,
	})
	if err != nil {
		return walletAuthorizationServiceFailure(c, err)
	}
	browserURL := h.publicOrigin + walletAuthorizationBrowserStartPath + "?request=" + url.QueryEscape(initiated.BrowserStartToken)
	return c.Status(http.StatusCreated).JSON(fiber.Map{
		"authorization_id": initiated.IntentToken,
		"browser_url":      browserURL,
		"expires_at":       initiated.ExpiresAt.Format(time.RFC3339Nano),
	})
}

func (h *walletAuthorizationHTTP) startBrowser(writer http.ResponseWriter, request *http.Request) {
	noStore(writer)
	if !h.canonicalHost(request) {
		walletAuthorizationHTTPError(writer, http.StatusBadRequest)
		return
	}
	values, err := exactQuery(request.URL.RawQuery, map[string]queryCardinality{
		"request": {minimum: 1, maximum: 1},
	})
	if err != nil || values.Get("request") == "" {
		walletAuthorizationHTTPError(writer, http.StatusBadRequest)
		return
	}
	ctx, cancel := walletAuthorizationContext(request)
	defer cancel()
	challenge, err := h.service.StartBrowser(ctx, values.Get("request"))
	if err != nil {
		walletAuthorizationHTTPServiceFailure(writer, err, http.StatusBadRequest)
		return
	}
	http.SetCookie(writer, newWalletAuthorizationFlowCookie(challenge.BrowserBinding, challenge.ExpiresAt))
	http.Redirect(writer, request, challenge.AuthorizationURL, http.StatusSeeOther)
}

func (h *walletAuthorizationHTTP) callback(writer http.ResponseWriter, request *http.Request) {
	noStore(writer)
	if !h.canonicalHost(request) {
		walletAuthorizationHTTPError(writer, http.StatusBadRequest)
		return
	}
	values, err := exactQuery(request.URL.RawQuery, map[string]queryCardinality{
		"state":         {minimum: 1, maximum: 1},
		"code":          {minimum: 1, maximum: 1},
		"iss":           {minimum: 1, maximum: 1},
		"session_state": {minimum: 0, maximum: 1},
	})
	if err != nil || values.Get("state") == "" || values.Get("code") == "" || values.Get("iss") != h.issuer ||
		(len(values["session_state"]) == 1 && values.Get("session_state") == "") {
		walletAuthorizationHTTPError(writer, http.StatusBadRequest)
		return
	}
	browserBinding, err := readWalletAuthorizationFlowCookie(request)
	if err != nil {
		walletAuthorizationHTTPError(writer, http.StatusBadRequest)
		return
	}
	ctx, cancel := walletAuthorizationContext(request)
	defer cancel()
	if _, err := h.service.Complete(ctx, values.Get("state"), browserBinding, values.Get("code")); err != nil {
		http.SetCookie(writer, clearWalletAuthorizationFlowCookie())
		walletAuthorizationHTTPServiceFailure(writer, err, http.StatusUnauthorized)
		return
	}
	http.SetCookie(writer, clearWalletAuthorizationFlowCookie())
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte("<!doctype html><title>Authorized</title><p>Transaction authorized. Return to Noebs to continue.</p>"))
}

func (h *walletAuthorizationHTTP) requireIntent(operation transactionauth.Operation) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if len(c.Request().URI().QueryString()) != 0 {
			return walletAuthorizationFailure(c, http.StatusBadRequest)
		}
		principal, ok := gateway.OIDCPrincipal(c)
		if !ok {
			return walletAuthorizationFailure(c, http.StatusUnauthorized)
		}
		values, _ := c.Locals(walletAuthorizationLocalKey).([]string)
		if len(values) != 1 || values[0] == "" {
			return walletAuthorizationFailure(c, http.StatusForbidden)
		}
		canonical, err := walletrequest.ParsePublic(operation, principal.Tenant(), c.Body(), h.defaults)
		if err != nil {
			return walletAuthorizationFailure(c, http.StatusBadRequest)
		}
		identity := principal.Identity()
		err = h.service.Claim(c.UserContext(), values[0], transactionauth.Binding{
			TenantID:       principal.Tenant(),
			Issuer:         identity.Issuer,
			Subject:        identity.Subject,
			Operation:      operation,
			RequestDigest:  canonical.Digest,
			IdempotencyKey: canonical.IdempotencyKey,
		})
		if err != nil {
			return walletAuthorizationServiceFailure(c, err)
		}
		c.Request().SetBodyRaw(canonical.Body)
		return c.Next()
	}
}

func captureWalletAuthorizationHeader(c *fiber.Ctx) error {
	raw := c.Request().Header.PeekAll(walletAuthorizationHeader)
	values := make([]string, len(raw))
	for index := range raw {
		values[index] = strings.TrimSpace(string(raw[index]))
	}
	c.Request().Header.Del(walletAuthorizationHeader)
	c.Locals(walletAuthorizationLocalKey, values)
	return c.Next()
}

func (h *walletAuthorizationHTTP) canonicalHost(request *http.Request) bool {
	return request != nil && request.Host == h.host
}

func newWalletAuthorizationFlowCookie(value string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     walletAuthorizationFlowCookieName,
		Value:    value,
		Path:     "/",
		Expires:  expiresAt.UTC(),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

func clearWalletAuthorizationFlowCookie() *http.Cookie {
	return &http.Cookie{
		Name:     walletAuthorizationFlowCookieName,
		Path:     "/",
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

func readWalletAuthorizationFlowCookie(request *http.Request) (string, error) {
	if request == nil {
		return "", transactionauth.ErrInvalidFlow
	}
	var values []string
	for _, cookie := range request.Cookies() {
		if cookie.Name == walletAuthorizationFlowCookieName {
			values = append(values, cookie.Value)
		}
	}
	if len(values) != 1 || values[0] == "" {
		return "", transactionauth.ErrInvalidFlow
	}
	return values[0], nil
}

func walletAuthorizationContext(request *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(request.Context(), walletAuthorizationRequestTTL)
}

func decodeExactJSON(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return transactionauth.ErrInvalidInput
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func noStoreFiber(c *fiber.Ctx) {
	c.Set("Cache-Control", "no-store")
	c.Set("Pragma", "no-cache")
	c.Set("X-Content-Type-Options", "nosniff")
}

func walletAuthorizationFailure(c *fiber.Ctx, status int) error {
	noStoreFiber(c)
	return c.Status(status).JSON(fiber.Map{
		"code":    "transaction_authorization_failed",
		"message": "transaction authorization failed",
	})
}

func walletAuthorizationServiceFailure(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, transactionauth.ErrStoreUnavailable),
		errors.Is(err, transactionauth.ErrOAuthExchange),
		errors.Is(err, transactionauth.ErrEntropyUnavailable),
		errors.Is(err, transactionauth.ErrEncryption),
		errors.Is(err, transactionauth.ErrUnknownKey):
		logWalletAuthorizationInfrastructureFailure(err)
		return walletAuthorizationFailure(c, http.StatusServiceUnavailable)
	case errors.Is(err, transactionauth.ErrAuthorizationDenied),
		errors.Is(err, transactionauth.ErrIntentNotFound),
		errors.Is(err, transactionauth.ErrInvalidFlow),
		errors.Is(err, transactionauth.ErrInvalidBrowserStart):
		return walletAuthorizationFailure(c, http.StatusForbidden)
	case errors.Is(err, transactionauth.ErrInvalidInput),
		errors.Is(err, transactionauth.ErrMissingTenantID),
		errors.Is(err, transactionauth.ErrInvalidTenantID),
		errors.Is(err, transactionauth.ErrMissingIssuer),
		errors.Is(err, transactionauth.ErrInvalidIssuer),
		errors.Is(err, transactionauth.ErrMissingSubject),
		errors.Is(err, transactionauth.ErrInvalidSubject),
		errors.Is(err, transactionauth.ErrInvalidOperation),
		errors.Is(err, transactionauth.ErrMissingRequestDigest),
		errors.Is(err, transactionauth.ErrMissingIdempotencyKey),
		errors.Is(err, transactionauth.ErrInvalidIdempotencyKey):
		return walletAuthorizationFailure(c, http.StatusBadRequest)
	default:
		return walletAuthorizationFailure(c, http.StatusInternalServerError)
	}
}

func walletAuthorizationHTTPServiceFailure(writer http.ResponseWriter, err error, invalidStatus int) {
	switch {
	case errors.Is(err, transactionauth.ErrStoreUnavailable),
		errors.Is(err, transactionauth.ErrOAuthExchange),
		errors.Is(err, transactionauth.ErrEntropyUnavailable),
		errors.Is(err, transactionauth.ErrEncryption),
		errors.Is(err, transactionauth.ErrUnknownKey):
		logWalletAuthorizationInfrastructureFailure(err)
		walletAuthorizationHTTPError(writer, http.StatusServiceUnavailable)
	case errors.Is(err, transactionauth.ErrInvalidBrowserStart),
		errors.Is(err, transactionauth.ErrInvalidFlow),
		errors.Is(err, transactionauth.ErrAuthorizationDenied),
		errors.Is(err, transactionauth.ErrInvalidIDToken),
		errors.Is(err, transactionauth.ErrInvalidInput):
		walletAuthorizationHTTPError(writer, invalidStatus)
	default:
		logWalletAuthorizationInfrastructureFailure(err)
		walletAuthorizationHTTPError(writer, http.StatusInternalServerError)
	}
}

func logWalletAuthorizationInfrastructureFailure(err error) {
	kind := "internal"
	switch {
	case errors.Is(err, transactionauth.ErrStoreUnavailable):
		kind = "store"
	case errors.Is(err, transactionauth.ErrOAuthExchange):
		kind = "oauth_exchange"
	case errors.Is(err, transactionauth.ErrEntropyUnavailable):
		kind = "entropy"
	case errors.Is(err, transactionauth.ErrEncryption), errors.Is(err, transactionauth.ErrUnknownKey):
		kind = "encryption"
	}
	logrusLogger.WithField("failure", kind).Error("wallet transaction authorization infrastructure failure")
}

func walletAuthorizationHTTPError(writer http.ResponseWriter, status int) {
	noStore(writer)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write([]byte(`{"code":"transaction_authorization_failed","message":"transaction authorization failed"}`))
}
