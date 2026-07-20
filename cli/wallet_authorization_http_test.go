package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/oidcauth"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/transactionauth"
	"github.com/adonese/noebs/internal/workloadauth"
	walletrequest "github.com/adonese/noebs/wallet/request"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

const (
	walletAuthorizationTestIssuer   = "https://identity.example/realms/noebs"
	walletAuthorizationTestSubject  = "subject-1"
	walletAuthorizationTestTenant   = "tenant-a"
	walletAuthorizationTestAudience = "noebs-api"
	walletAuthorizationTestClient   = "noebs-mobile"
	walletAuthorizationTestKeyID    = "wallet-authorization-test-key"
)

var walletAuthorizationTestNow = time.Now().UTC().Truncate(time.Second)

func TestWalletAuthorizationClaimIsExactOneUseGate(t *testing.T) {
	handler, repository := newWalletAuthorizationHTTPTestHandler(t)
	mobileAuth, accessToken := walletAuthorizationMobileAuthForTest(t)
	var hits atomic.Int32
	var upstreamBody []byte
	var upstreamCredential string
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post(
		"/wallet/p2p",
		captureWalletAuthorizationHeader,
		clearGatewayIdentityHeaders,
		mobileAuth,
		handler.requireIntent(transactionauth.OperationWalletP2P),
		func(c *fiber.Ctx) error {
			hits.Add(1)
			upstreamBody = bytes.Clone(c.Body())
			upstreamCredential = c.Get(walletAuthorizationHeader)
			return c.SendStatus(http.StatusNoContent)
		},
	)

	body := []byte(`{
		"currency":" SDG ",
		"from_wallet_id":"550e8400-e29b-41d4-a716-446655440000",
		"to_wallet_id":"550e8400-e29b-41d4-a716-446655440001",
		"amount":100,
		"description":" transfer ",
		"idempotency_key":"p2p-1",
		"reference_id":"p2p-1",
		"to_owner_type":"user",
		"to_owner_id":"42"
	}`)
	canonical, err := walletrequest.ParsePublic(
		transactionauth.OperationWalletP2P,
		walletAuthorizationTestTenant,
		body,
		handler.defaults,
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := walletAuthorizationTestBinding(transactionauth.OperationWalletP2P, canonical)
	intent := repository.authorize(t, 1, binding)

	response := performWalletAuthorizationRequest(t, app, accessToken, "/wallet/p2p", body)
	if response.StatusCode != http.StatusForbidden || hits.Load() != 0 {
		body, _ := io.ReadAll(response.Body)
		closeResponseBody(t, response.Body)
		t.Fatalf("missing credential status=%d hits=%d body=%s", response.StatusCode, hits.Load(), body)
	}
	closeResponseBody(t, response.Body)

	mutated := bytes.Replace(body, []byte(`"amount":100`), []byte(`"amount":101`), 1)
	response = performWalletAuthorizationRequest(t, app, accessToken, "/wallet/p2p", mutated, intent)
	if response.StatusCode != http.StatusForbidden || hits.Load() != 0 {
		closeResponseBody(t, response.Body)
		t.Fatalf("mismatched request status=%d hits=%d", response.StatusCode, hits.Load())
	}
	closeResponseBody(t, response.Body)

	response = performWalletAuthorizationRequest(t, app, accessToken, "/wallet/p2p", body, intent)
	if response.StatusCode != http.StatusNoContent || hits.Load() != 1 {
		closeResponseBody(t, response.Body)
		t.Fatalf("authorized request status=%d hits=%d", response.StatusCode, hits.Load())
	}
	closeResponseBody(t, response.Body)
	if !bytes.Equal(upstreamBody, canonical.Body) {
		t.Fatalf("upstream body = %s, want canonical %s", upstreamBody, canonical.Body)
	}
	if upstreamCredential != "" {
		t.Fatalf("upstream transaction credential = %q", upstreamCredential)
	}

	response = performWalletAuthorizationRequest(t, app, accessToken, "/wallet/p2p", body, intent)
	if response.StatusCode != http.StatusForbidden || hits.Load() != 1 {
		closeResponseBody(t, response.Body)
		t.Fatalf("replay status=%d hits=%d", response.StatusCode, hits.Load())
	}
	closeResponseBody(t, response.Body)

	duplicateIntent := repository.authorize(t, 4, binding)
	response = performWalletAuthorizationRequest(t, app, accessToken, "/wallet/p2p", body, duplicateIntent, duplicateIntent)
	if response.StatusCode != http.StatusForbidden || hits.Load() != 1 {
		closeResponseBody(t, response.Body)
		t.Fatalf("duplicate credential status=%d hits=%d", response.StatusCode, hits.Load())
	}
	closeResponseBody(t, response.Body)
	response = performWalletAuthorizationRequest(t, app, accessToken, "/wallet/p2p", body, duplicateIntent)
	if response.StatusCode != http.StatusNoContent || hits.Load() != 2 {
		closeResponseBody(t, response.Body)
		t.Fatalf("claim after duplicate credential status=%d hits=%d", response.StatusCode, hits.Load())
	}
	closeResponseBody(t, response.Body)

	queryIntent := repository.authorize(t, 2, binding)
	response = performWalletAuthorizationRequest(t, app, accessToken, "/wallet/p2p?mode=fast", body, queryIntent)
	if response.StatusCode != http.StatusBadRequest || hits.Load() != 2 {
		closeResponseBody(t, response.Body)
		t.Fatalf("query status=%d hits=%d", response.StatusCode, hits.Load())
	}
	closeResponseBody(t, response.Body)
	response = performWalletAuthorizationRequest(t, app, accessToken, "/wallet/p2p", body, queryIntent)
	if response.StatusCode != http.StatusNoContent || hits.Load() != 3 {
		closeResponseBody(t, response.Body)
		t.Fatalf("claim after rejected query status=%d hits=%d", response.StatusCode, hits.Load())
	}
	closeResponseBody(t, response.Body)
}

func TestConcurrentWalletAuthorizationClaimHasOneUpstreamRequest(t *testing.T) {
	handler, repository := newWalletAuthorizationHTTPTestHandler(t)
	mobileAuth, accessToken := walletAuthorizationMobileAuthForTest(t)
	var hits atomic.Int32
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post(
		"/wallet/p2p",
		captureWalletAuthorizationHeader,
		clearGatewayIdentityHeaders,
		mobileAuth,
		handler.requireIntent(transactionauth.OperationWalletP2P),
		func(c *fiber.Ctx) error {
			hits.Add(1)
			return c.SendStatus(http.StatusNoContent)
		},
	)
	body := []byte(`{"currency":"SDG","from_wallet_id":"550e8400-e29b-41d4-a716-446655440000","to_wallet_id":"550e8400-e29b-41d4-a716-446655440001","amount":100,"idempotency_key":"p2p-concurrent","reference_id":"p2p-concurrent","to_owner_type":"user","to_owner_id":"42"}`)
	canonical, err := walletrequest.ParsePublic(transactionauth.OperationWalletP2P, walletAuthorizationTestTenant, body, handler.defaults)
	if err != nil {
		t.Fatal(err)
	}
	intent := repository.authorize(t, 3, walletAuthorizationTestBinding(transactionauth.OperationWalletP2P, canonical))

	const attempts = 24
	start := make(chan struct{})
	statuses := make(chan int, attempts)
	var wait sync.WaitGroup
	for range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			response := performWalletAuthorizationRequest(t, app, accessToken, "/wallet/p2p", body, intent)
			statuses <- response.StatusCode
			closeResponseBody(t, response.Body)
		}()
	}
	close(start)
	wait.Wait()
	close(statuses)
	winners := 0
	for status := range statuses {
		switch status {
		case http.StatusNoContent:
			winners++
		case http.StatusForbidden:
		default:
			t.Errorf("concurrent status = %d", status)
		}
	}
	if winners != 1 || hits.Load() != 1 {
		t.Fatalf("winners=%d upstream hits=%d", winners, hits.Load())
	}
}

func TestWalletAuthorizationBeginRejectsQueryBeforeCreatingIntent(t *testing.T) {
	handler, repository := newWalletAuthorizationHTTPTestHandler(t)
	mobileAuth, accessToken := walletAuthorizationMobileAuthForTest(t)
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Post("/wallet/authorizations", captureWalletAuthorizationHeader, clearGatewayIdentityHeaders, mobileAuth, handler.begin)
	body := []byte(`{"operation":"wallet.p2p","request":{"currency":"SDG","from_wallet_id":"550e8400-e29b-41d4-a716-446655440000","to_wallet_id":"550e8400-e29b-41d4-a716-446655440001","amount":100,"idempotency_key":"p2p-1","reference_id":"p2p-1","to_owner_type":"user","to_owner_id":"42"}}`)
	response := performWalletAuthorizationRequest(t, app, accessToken, "/wallet/authorizations?mode=fast", body)
	if response.StatusCode != http.StatusBadRequest || repository.count() != 0 {
		closeResponseBody(t, response.Body)
		t.Fatalf("query status=%d intents=%d", response.StatusCode, repository.count())
	}
	closeResponseBody(t, response.Body)

	response = performWalletAuthorizationRequest(t, app, accessToken, "/wallet/authorizations", body)
	defer closeResponseBody(t, response.Body)
	if response.StatusCode != http.StatusCreated || repository.count() != 1 {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("begin status=%d intents=%d body=%s", response.StatusCode, repository.count(), payload)
	}
	var payload struct {
		AuthorizationID string `json:"authorization_id"`
		BrowserURL      string `json:"browser_url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.AuthorizationID == "" || payload.BrowserURL == "" {
		t.Fatalf("begin response = %+v", payload)
	}
}

func TestWalletAuthorizationBrowserCallbackLifecycle(t *testing.T) {
	oauth := &walletAuthorizationLifecycleOAuth{
		identity: transactionauth.VerifiedIdentity{
			Issuer:             walletAuthorizationTestIssuer,
			Subject:            walletAuthorizationTestSubject,
			ACR:                walletAuthorizerRequiredACR,
			AuthenticationTime: walletAuthorizationTestNow.Add(-time.Second),
		},
	}
	handler, repository := newWalletAuthorizationHTTPTestHandlerWithOAuth(t, oauth)
	binding := transactionauth.Binding{
		TenantID:       walletAuthorizationTestTenant,
		Issuer:         walletAuthorizationTestIssuer,
		Subject:        walletAuthorizationTestSubject,
		Operation:      transactionauth.OperationWalletP2P,
		RequestDigest:  transactionauth.Digest(sha256.Sum256([]byte("canonical-request"))),
		IdempotencyKey: "p2p-browser-callback",
	}
	initiated, err := handler.service.Begin(t.Context(), binding)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	if err := registerWalletAuthorizationRoutes(
		app,
		handler,
		func(c *fiber.Ctx) error { return c.Next() },
		func(c *fiber.Ctx) error { return c.Next() },
	); err != nil {
		t.Fatal(err)
	}
	headStart := httptest.NewRequest(
		http.MethodHead,
		"https://api.noebs.sd"+walletAuthorizationBrowserStartPath+"?request="+url.QueryEscape(initiated.BrowserStartToken),
		nil,
	)
	response, err := app.Test(headStart)
	if err != nil {
		t.Fatal(err)
	}
	starts, flows := repository.pendingFlowCounts()
	if response.StatusCode != http.StatusMethodNotAllowed || response.Header.Get("Set-Cookie") != "" || starts != 1 || flows != 0 {
		closeResponseBody(t, response.Body)
		t.Fatalf("HEAD browser start status=%d cookie=%q starts=%d flows=%d", response.StatusCode, response.Header.Get("Set-Cookie"), starts, flows)
	}
	closeResponseBody(t, response.Body)

	invalidStart := httptest.NewRequest(
		http.MethodGet,
		"https://api.noebs.sd"+walletAuthorizationBrowserStartPath+"?request="+url.QueryEscape(initiated.BrowserStartToken)+"&request=duplicate",
		nil,
	)
	response, err = app.Test(invalidStart)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest || response.Header.Get("Set-Cookie") != "" {
		closeResponseBody(t, response.Body)
		t.Fatalf("invalid browser start status=%d cookie=%q", response.StatusCode, response.Header.Get("Set-Cookie"))
	}
	assertWalletAuthorizationNoStore(t, response)
	closeResponseBody(t, response.Body)
	wrongHost := httptest.NewRequest(
		http.MethodGet,
		"https://attacker.example"+walletAuthorizationBrowserStartPath+"?request="+url.QueryEscape(initiated.BrowserStartToken),
		nil,
	)
	response, err = app.Test(wrongHost)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest || response.Header.Get("Set-Cookie") != "" {
		closeResponseBody(t, response.Body)
		t.Fatalf("wrong-host browser start status=%d cookie=%q", response.StatusCode, response.Header.Get("Set-Cookie"))
	}
	assertWalletAuthorizationNoStore(t, response)
	closeResponseBody(t, response.Body)

	start := httptest.NewRequest(
		http.MethodGet,
		"https://api.noebs.sd"+walletAuthorizationBrowserStartPath+"?request="+url.QueryEscape(initiated.BrowserStartToken),
		nil,
	)
	response, err = app.Test(start)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != oauth.authorizationURL {
		closeResponseBody(t, response.Body)
		t.Fatalf("browser start status=%d location=%q, want %q", response.StatusCode, response.Header.Get("Location"), oauth.authorizationURL)
	}
	assertWalletAuthorizationNoStore(t, response)
	flowCookie := walletAuthorizationResponseCookie(t, response, walletAuthorizationFlowCookieName)
	if flowCookie.Value == "" || flowCookie.Path != "/" || !flowCookie.Secure || !flowCookie.HttpOnly ||
		flowCookie.SameSite != http.SameSiteLaxMode || !flowCookie.Expires.Equal(walletAuthorizationTestNow.Add(walletAuthorizationFlowTTL)) {
		closeResponseBody(t, response.Body)
		t.Fatalf("flow cookie = %+v", flowCookie)
	}
	closeResponseBody(t, response.Body)
	if oauth.state == "" || oauth.nonce == "" || oauth.verifier == "" {
		t.Fatalf("OAuth challenge omitted state, nonce, or PKCE verifier: %+v", oauth)
	}
	headCallback := httptest.NewRequest(
		http.MethodHead,
		"https://api.noebs.sd"+walletAuthorizationCallbackPath+"?state="+url.QueryEscape(oauth.state)+"&code=code&iss="+url.QueryEscape(walletAuthorizationTestIssuer),
		nil,
	)
	headCallback.AddCookie(flowCookie)
	response, err = app.Test(headCallback)
	if err != nil {
		t.Fatal(err)
	}
	starts, flows = repository.pendingFlowCounts()
	if response.StatusCode != http.StatusMethodNotAllowed || response.Header.Get("Set-Cookie") != "" || oauth.exchangeCalls != 0 || starts != 0 || flows != 1 {
		closeResponseBody(t, response.Body)
		t.Fatalf("HEAD callback status=%d cookie=%q exchanges=%d starts=%d flows=%d", response.StatusCode, response.Header.Get("Set-Cookie"), oauth.exchangeCalls, starts, flows)
	}
	closeResponseBody(t, response.Body)

	badIssuer := httptest.NewRequest(
		http.MethodGet,
		"https://api.noebs.sd"+walletAuthorizationCallbackPath+"?state="+url.QueryEscape(oauth.state)+"&code=code&iss="+url.QueryEscape("https://attacker.example/realms/noebs"),
		nil,
	)
	badIssuer.AddCookie(flowCookie)
	response, err = app.Test(badIssuer)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest || response.Header.Get("Set-Cookie") != "" || oauth.exchangeCalls != 0 {
		closeResponseBody(t, response.Body)
		t.Fatalf("bad issuer status=%d cookie=%q exchanges=%d", response.StatusCode, response.Header.Get("Set-Cookie"), oauth.exchangeCalls)
	}
	assertWalletAuthorizationNoStore(t, response)
	closeResponseBody(t, response.Body)

	callback := httptest.NewRequest(
		http.MethodGet,
		"https://api.noebs.sd"+walletAuthorizationCallbackPath+"?state="+url.QueryEscape(oauth.state)+"&code=code&iss="+url.QueryEscape(walletAuthorizationTestIssuer)+"&session_state=session-1",
		nil,
	)
	callback.AddCookie(flowCookie)
	response, err = app.Test(callback)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") || oauth.exchangeCalls != 1 {
		closeResponseBody(t, response.Body)
		t.Fatalf("callback status=%d content-type=%q exchanges=%d", response.StatusCode, response.Header.Get("Content-Type"), oauth.exchangeCalls)
	}
	assertWalletAuthorizationNoStore(t, response)
	cleared := walletAuthorizationResponseCookie(t, response, walletAuthorizationFlowCookieName)
	if cleared.MaxAge != -1 || !cleared.Secure || !cleared.HttpOnly || cleared.Path != "/" {
		closeResponseBody(t, response.Body)
		t.Fatalf("cleared flow cookie = %+v", cleared)
	}
	closeResponseBody(t, response.Body)
	if err := handler.service.Claim(t.Context(), initiated.IntentToken, binding); err != nil {
		t.Fatalf("claim after callback: %v", err)
	}
	if repository.count() != 0 {
		t.Fatalf("claimed intent count = %d, want 0", repository.count())
	}
}

func TestWalletAuthorizationCallbackServiceFailureClearsCookie(t *testing.T) {
	oauth := &walletAuthorizationLifecycleOAuth{
		identity: transactionauth.VerifiedIdentity{
			Issuer:             walletAuthorizationTestIssuer,
			Subject:            walletAuthorizationTestSubject,
			ACR:                walletAuthorizerRequiredACR,
			AuthenticationTime: walletAuthorizationTestNow.Add(-time.Second),
		},
	}
	handler, _ := newWalletAuthorizationHTTPTestHandlerWithOAuth(t, oauth)
	initiated, err := handler.service.Begin(t.Context(), transactionauth.Binding{
		TenantID:       walletAuthorizationTestTenant,
		Issuer:         walletAuthorizationTestIssuer,
		Subject:        walletAuthorizationTestSubject,
		Operation:      transactionauth.OperationWalletP2P,
		RequestDigest:  transactionauth.Digest(sha256.Sum256([]byte("callback-failure-request"))),
		IdempotencyKey: "callback-failure",
	})
	if err != nil {
		t.Fatal(err)
	}
	startWriter := httptest.NewRecorder()
	startRequest := httptest.NewRequest(
		http.MethodGet,
		"https://api.noebs.sd"+walletAuthorizationBrowserStartPath+"?request="+url.QueryEscape(initiated.BrowserStartToken),
		nil,
	)
	handler.startBrowser(startWriter, startRequest)
	startResponse := startWriter.Result()
	if startResponse.StatusCode != http.StatusSeeOther {
		closeResponseBody(t, startResponse.Body)
		t.Fatalf("browser start status = %d", startResponse.StatusCode)
	}
	flowCookie := walletAuthorizationResponseCookie(t, startResponse, walletAuthorizationFlowCookieName)
	closeResponseBody(t, startResponse.Body)

	callbackWriter := httptest.NewRecorder()
	callbackRequest := httptest.NewRequest(
		http.MethodGet,
		"https://api.noebs.sd"+walletAuthorizationCallbackPath+"?state="+url.QueryEscape(oauth.state)+"&code=bad-code&iss="+url.QueryEscape(walletAuthorizationTestIssuer),
		nil,
	)
	callbackRequest.AddCookie(flowCookie)
	handler.callback(callbackWriter, callbackRequest)
	callbackResponse := callbackWriter.Result()
	if callbackResponse.StatusCode != http.StatusServiceUnavailable {
		closeResponseBody(t, callbackResponse.Body)
		t.Fatalf("callback status = %d, want %d", callbackResponse.StatusCode, http.StatusServiceUnavailable)
	}
	assertWalletAuthorizationNoStore(t, callbackResponse)
	cleared := walletAuthorizationResponseCookie(t, callbackResponse, walletAuthorizationFlowCookieName)
	if cleared.MaxAge != -1 || !cleared.Secure || !cleared.HttpOnly || cleared.Path != "/" {
		closeResponseBody(t, callbackResponse.Body)
		t.Fatalf("cleared flow cookie = %+v", cleared)
	}
	payload, err := io.ReadAll(callbackResponse.Body)
	if err != nil {
		t.Fatal(err)
	}
	closeResponseBody(t, callbackResponse.Body)
	if bytes.Contains(payload, []byte("OAuth")) || bytes.Contains(payload, []byte("bad-code")) {
		t.Fatalf("callback leaked service failure: %s", payload)
	}
}

func TestRegisteredAPIGatewayWalletAuthorizationGate(t *testing.T) {
	handler, repository := newWalletAuthorizationHTTPTestHandler(t)
	body := []byte(`{
		"currency":" SDG ",
		"from_wallet_id":"550e8400-e29b-41d4-a716-446655440000",
		"to_wallet_id":"550e8400-e29b-41d4-a716-446655440001",
		"amount":100,
		"idempotency_key":"proxy-p2p",
		"reference_id":"proxy-p2p",
		"to_owner_type":"user",
		"to_owner_id":"42"
	}`)
	canonical, err := walletrequest.ParsePublic(
		transactionauth.OperationWalletP2P,
		walletAuthorizationTestTenant,
		body,
		handler.defaults,
	)
	if err != nil {
		t.Fatal(err)
	}
	intent := repository.authorize(t, 9, walletAuthorizationTestBinding(transactionauth.OperationWalletP2P, canonical))

	identityVerifier := newTestWorkloadVerifier(t, string(serviceRoleIdentityAuth), string(serviceRoleAPIGateway))
	walletVerifier := newTestWorkloadVerifier(t, string(serviceRoleWalletAPI), string(serviceRoleAPIGateway))
	var projectionHits atomic.Int32
	var walletHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestBody, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch request.URL.Path {
		case "/internal/identity-auth/principals/resolve":
			projectionHits.Add(1)
			if _, err := identityVerifier.Verify(request, requestBody); err != nil {
				t.Errorf("verify profile projection request: %v", err)
				writer.WriteHeader(http.StatusUnauthorized)
				return
			}
			if request.Header.Get(workloadauth.HeaderTenantID) != walletAuthorizationTestTenant ||
				request.Header.Get(workloadauth.HeaderSubject) != walletAuthorizationTestSubject ||
				request.Header.Get(workloadauth.HeaderUserID) != "" {
				t.Errorf("profile projection identity headers = %v", request.Header)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"user_id":42}`))
		case "/wallet/p2p":
			walletHits.Add(1)
			if _, err := walletVerifier.Verify(request, requestBody); err != nil {
				t.Errorf("verify wallet request: %v", err)
				writer.WriteHeader(http.StatusUnauthorized)
				return
			}
			if request.Header.Get("Authorization") != "" || request.Header.Get(walletAuthorizationHeader) != "" ||
				request.Header.Get(workloadauth.HeaderTenantID) != walletAuthorizationTestTenant ||
				request.Header.Get(workloadauth.HeaderUserID) != "42" || !bytes.Equal(requestBody, canonical.Body) {
				t.Errorf("wallet request headers=%v body=%s, want canonical body=%s", request.Header, requestBody, canonical.Body)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			writer.WriteHeader(http.StatusNoContent)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)

	discovery := make(map[string]string)
	for _, spec := range gatewayProxyRouteSpecs() {
		discovery[string(spec.role)] = upstream.URL
	}
	previousSigners := workloadSigners
	workloadSigners = newTestWorkloadSigners(
		t,
		string(serviceRoleAPIGateway),
		string(serviceRoleIdentityAuth),
		string(serviceRoleCardVault),
		string(serviceRoleEBSAdapter),
		string(serviceRolePSPWebhook),
		string(serviceRoleAdminReporting),
		string(serviceRoleNotification),
		string(serviceRoleWalletAPI),
	)
	t.Cleanup(func() { workloadSigners = previousSigners })
	verifier, accessToken := walletAuthorizationOIDCVerifierAndTokenForTest(t)
	previousOIDCVerifier := oidcVerifier
	oidcVerifier = verifier
	t.Cleanup(func() { oidcVerifier = previousOIDCVerifier })

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(gateway.RequestID())
	if err := registerAPIGatewayProxyRoutes(app, ebs_fields.NoebsConfig{
		DefaultTenantID: walletAuthorizationTestTenant,
		PSPWebhookRoutes: map[string]ebs_fields.PSPWebhookRoute{
			testPSPWebhookCallbackID: {TenantID: walletAuthorizationTestTenant, ProviderCode: "test-provider"},
		},
		ServiceDiscovery: discovery,
	}, gatewayTestTenantCatalog(t), handler); err != nil {
		t.Fatal(err)
	}

	perform := func() *http.Response {
		request := httptest.NewRequest(http.MethodPost, "/wallet/p2p", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+accessToken)
		request.Header.Set("X-Active-Tenant", walletAuthorizationTestTenant)
		request.Header.Set("X-Forwarded-For", "203.0.113.9")
		request.Header.Set(walletAuthorizationHeader, intent)
		response, err := app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	response := perform()
	if response.StatusCode != http.StatusNoContent || projectionHits.Load() != 1 || walletHits.Load() != 1 {
		payload, _ := io.ReadAll(response.Body)
		closeResponseBody(t, response.Body)
		t.Fatalf("first request status=%d projection hits=%d wallet hits=%d body=%s", response.StatusCode, projectionHits.Load(), walletHits.Load(), payload)
	}
	closeResponseBody(t, response.Body)

	response = perform()
	if response.StatusCode != http.StatusForbidden || projectionHits.Load() != 2 || walletHits.Load() != 1 {
		payload, _ := io.ReadAll(response.Body)
		closeResponseBody(t, response.Body)
		t.Fatalf("replay status=%d projection hits=%d wallet hits=%d body=%s", response.StatusCode, projectionHits.Load(), walletHits.Load(), payload)
	}
	closeResponseBody(t, response.Body)
}

func TestWalletAuthorizationInfrastructureErrorsAreGenericUnavailable(t *testing.T) {
	writer := httptest.NewRecorder()
	walletAuthorizationHTTPServiceFailure(writer, errors.Join(transactionauth.ErrStoreUnavailable, errors.New("database host secret.internal")), http.StatusBadRequest)
	if writer.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", writer.Code, http.StatusServiceUnavailable)
	}
	if bytes.Contains(writer.Body.Bytes(), []byte("database")) || bytes.Contains(writer.Body.Bytes(), []byte("secret.internal")) {
		t.Fatalf("infrastructure detail leaked: %s", writer.Body.Bytes())
	}
}

func newWalletAuthorizationHTTPTestHandler(t testing.TB) (*walletAuthorizationHTTP, *walletAuthorizationMemoryRepository) {
	return newWalletAuthorizationHTTPTestHandlerWithOAuth(t, walletAuthorizationNoopOAuth{})
}

func newWalletAuthorizationHTTPTestHandlerWithOAuth(
	t testing.TB,
	oauth transactionauth.CodeExchanger,
) (*walletAuthorizationHTTP, *walletAuthorizationMemoryRepository) {
	t.Helper()
	repository := &walletAuthorizationMemoryRepository{
		intents: map[transactionauth.Digest]transactionauth.IntentRecord{},
		starts:  map[transactionauth.Digest]transactionauth.Digest{},
		flows:   map[transactionauth.Digest]transactionauth.FlowRecord{},
	}
	keys, err := transactionauth.NewKeyring(transactionauth.KeyringConfig{
		ActiveKeyID: "test-key",
		Keys:        map[string][]byte{"test-key": bytes.Repeat([]byte{0x42}, 32)},
		Entropy:     rand.Reader,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := transactionauth.NewService(transactionauth.ServiceConfig{
		Repository:       repository,
		OAuth:            oauth,
		Keys:             keys,
		Clock:            walletAuthorizationFixedClock{},
		Entropy:          rand.Reader,
		RequiredACR:      walletAuthorizerRequiredACR,
		BrowserStartTTL:  walletAuthorizationBrowserStartTTL,
		FlowTTL:          walletAuthorizationFlowTTL,
		AuthorizationTTL: walletAuthorizationTTL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &walletAuthorizationHTTP{
		service:      service,
		issuer:       walletAuthorizationTestIssuer,
		host:         "api.noebs.sd",
		publicOrigin: "https://api.noebs.sd",
		defaults: walletrequest.Defaults{
			HoldExpirySeconds:      3600,
			ApprovalTimeoutSeconds: 3600,
			ApprovalThreshold:      100000,
		},
	}, repository
}

func walletAuthorizationMobileAuthForTest(t testing.TB) (fiber.Handler, string) {
	t.Helper()
	verifier, signed := walletAuthorizationOIDCVerifierAndTokenForTest(t)
	middleware, err := gateway.NewOIDCAuthMiddleware(gateway.OIDCAuthConfig{
		Verifier: verifier,
		SelectTenant: func(c *fiber.Ctx) (string, error) {
			return c.Get("X-Active-Tenant"), nil
		},
		AllowedClients: []string{walletAuthorizationTestClient},
		AllowedRoles:   []tenantauth.Role{tenantauth.RoleUser},
	})
	if err != nil {
		t.Fatal(err)
	}
	return middleware, signed
}

func walletAuthorizationOIDCVerifierAndTokenForTest(t testing.TB) (*oidcauth.Verifier, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := oidcauth.NewStaticKeySet(map[string]*rsa.PublicKey{walletAuthorizationTestKeyID: &key.PublicKey})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := oidcauth.NewVerifier(oidcauth.Config{
		Issuer:            walletAuthorizationTestIssuer,
		Audience:          walletAuthorizationTestAudience,
		AllowedClients:    []string{walletAuthorizationTestClient},
		AccessTokenType:   "Bearer",
		MaxFutureIssuedAt: 30 * time.Second,
		Clock:             walletAuthorizationFixedClock{},
		Keys:              keys,
	})
	if err != nil {
		t.Fatal(err)
	}
	claims := jwt.MapClaims{
		"iss": walletAuthorizationTestIssuer,
		"sub": walletAuthorizationTestSubject,
		"aud": walletAuthorizationTestAudience,
		"exp": jwt.NewNumericDate(walletAuthorizationTestNow.Add(5 * time.Minute)),
		"iat": jwt.NewNumericDate(walletAuthorizationTestNow),
		"azp": walletAuthorizationTestClient,
		"typ": "Bearer",
		"organization": map[string]any{
			walletAuthorizationTestTenant: map[string]any{
				"id": "org-a",
				"resource_access": map[string]any{
					walletAuthorizationTestAudience: map[string]any{"roles": []string{"user"}},
				},
			},
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = walletAuthorizationTestKeyID
	token.Header["typ"] = "JWT"
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return verifier, signed
}

func performWalletAuthorizationRequest(
	t testing.TB,
	app *fiber.App,
	accessToken string,
	target string,
	body []byte,
	intent ...string,
) *http.Response {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	request.Header.Set(fiber.HeaderAuthorization, "Bearer "+accessToken)
	request.Header.Set("X-Active-Tenant", walletAuthorizationTestTenant)
	for _, value := range intent {
		request.Header.Add(walletAuthorizationHeader, value)
	}
	response, err := app.Test(request)
	if err != nil {
		t.Errorf("app.Test(): %v", err)
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(bytes.NewReader(nil)),
			Header:     make(http.Header),
		}
	}
	return response
}

func walletAuthorizationTestBinding(operation transactionauth.Operation, canonical walletrequest.Canonical) transactionauth.Binding {
	return transactionauth.Binding{
		TenantID:       walletAuthorizationTestTenant,
		Issuer:         walletAuthorizationTestIssuer,
		Subject:        walletAuthorizationTestSubject,
		Operation:      operation,
		RequestDigest:  canonical.Digest,
		IdempotencyKey: canonical.IdempotencyKey,
	}
}

type walletAuthorizationFixedClock struct{}

func (walletAuthorizationFixedClock) Now() time.Time { return walletAuthorizationTestNow }

type walletAuthorizationNoopOAuth struct{}

func (walletAuthorizationNoopOAuth) AuthorizationURL(string, string, string) (string, error) {
	return "", transactionauth.ErrInvalidInput
}

func (walletAuthorizationNoopOAuth) Exchange(context.Context, string, string, transactionauth.Digest) (transactionauth.VerifiedIdentity, error) {
	return transactionauth.VerifiedIdentity{}, transactionauth.ErrOAuthExchange
}

type walletAuthorizationLifecycleOAuth struct {
	state            string
	nonce            string
	verifier         string
	authorizationURL string
	exchangeCalls    int
	identity         transactionauth.VerifiedIdentity
}

func (o *walletAuthorizationLifecycleOAuth) AuthorizationURL(state, nonce, verifier string) (string, error) {
	o.state = state
	o.nonce = nonce
	o.verifier = verifier
	o.authorizationURL = "https://identity.example/authorize?state=" + url.QueryEscape(state)
	return o.authorizationURL, nil
}

func (o *walletAuthorizationLifecycleOAuth) Exchange(
	_ context.Context,
	code string,
	verifier string,
	nonceHash transactionauth.Digest,
) (transactionauth.VerifiedIdentity, error) {
	o.exchangeCalls++
	if code != "code" || verifier != o.verifier || nonceHash != transactionauth.Digest(sha256.Sum256([]byte(o.nonce))) {
		return transactionauth.VerifiedIdentity{}, transactionauth.ErrOAuthExchange
	}
	return o.identity, nil
}

type walletAuthorizationMemoryRepository struct {
	mu      sync.Mutex
	intents map[transactionauth.Digest]transactionauth.IntentRecord
	starts  map[transactionauth.Digest]transactionauth.Digest
	flows   map[transactionauth.Digest]transactionauth.FlowRecord
}

func (r *walletAuthorizationMemoryRepository) CreateIntent(_ context.Context, intent transactionauth.IntentRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.intents[intent.IntentHash]; exists {
		return transactionauth.ErrIntentConflict
	}
	r.intents[intent.IntentHash] = intent
	r.starts[intent.BrowserStartHash] = intent.IntentHash
	return nil
}

func (r *walletAuthorizationMemoryRepository) StartFlow(
	_ context.Context,
	startHash transactionauth.Digest,
	flow transactionauth.NewFlowRecord,
	now time.Time,
) (transactionauth.FlowRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	intentHash, ok := r.starts[startHash]
	if !ok {
		return transactionauth.FlowRecord{}, transactionauth.ErrInvalidBrowserStart
	}
	intent := r.intents[intentHash]
	if !intent.ExpiresAt.After(now) {
		return transactionauth.FlowRecord{}, transactionauth.ErrInvalidBrowserStart
	}
	delete(r.starts, startHash)
	intent.BrowserStartHash = transactionauth.Digest{}
	r.intents[intentHash] = intent
	if flow.ExpiresAt.After(intent.ExpiresAt) {
		flow.ExpiresAt = intent.ExpiresAt
	}
	stored := transactionauth.FlowRecord{NewFlowRecord: flow, IntentHash: intentHash, Binding: intent.Binding}
	r.flows[flow.StateHash] = stored
	return stored, nil
}

func (r *walletAuthorizationMemoryRepository) ConsumeFlow(
	_ context.Context,
	stateHash transactionauth.Digest,
	browserHash transactionauth.Digest,
	now time.Time,
) (transactionauth.FlowRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	flow, ok := r.flows[stateHash]
	if !ok || flow.BrowserHash != browserHash || !flow.ExpiresAt.After(now) {
		return transactionauth.FlowRecord{}, transactionauth.ErrInvalidFlow
	}
	delete(r.flows, stateHash)
	intent, ok := r.intents[flow.IntentHash]
	if !ok || !intent.ExpiresAt.After(now) || !intent.AuthorizedAt.IsZero() {
		return transactionauth.FlowRecord{}, transactionauth.ErrInvalidFlow
	}
	flow.Binding = intent.Binding
	return flow, nil
}

func (r *walletAuthorizationMemoryRepository) AuthorizeIntent(
	_ context.Context,
	intentHash transactionauth.Digest,
	authorizedAt time.Time,
	authenticationTime time.Time,
	expiresAt time.Time,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	intent, ok := r.intents[intentHash]
	if !ok || !intent.AuthorizedAt.IsZero() || !intent.ExpiresAt.After(authorizedAt) {
		return transactionauth.ErrAuthorizationDenied
	}
	intent.AuthorizedAt = authorizedAt
	intent.AuthenticationTime = authenticationTime
	intent.ExpiresAt = expiresAt
	r.intents[intentHash] = intent
	return nil
}

func (r *walletAuthorizationMemoryRepository) ClaimIntent(
	_ context.Context,
	intentHash transactionauth.Digest,
	binding transactionauth.Binding,
	now time.Time,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	intent, exists := r.intents[intentHash]
	if !exists || intent.Binding != binding || intent.AuthorizedAt.IsZero() || !intent.ExpiresAt.After(now) {
		return transactionauth.ErrIntentNotFound
	}
	delete(r.intents, intentHash)
	return nil
}

func (r *walletAuthorizationMemoryRepository) DeleteExpired(_ context.Context, before time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var deleted int64
	for hash, intent := range r.intents {
		if !intent.ExpiresAt.After(before) {
			delete(r.intents, hash)
			deleted++
		}
	}
	return deleted, nil
}

func (r *walletAuthorizationMemoryRepository) authorize(
	t testing.TB,
	fill byte,
	binding transactionauth.Binding,
) string {
	t.Helper()
	raw := bytes.Repeat([]byte{fill}, 32)
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest := transactionauth.Digest(sha256.Sum256([]byte(token)))
	now := walletAuthorizationTestNow
	r.mu.Lock()
	r.intents[digest] = transactionauth.IntentRecord{
		IntentHash:         digest,
		Binding:            binding,
		CreatedAt:          now.Add(-time.Minute),
		ExpiresAt:          now.Add(time.Minute),
		AuthorizedAt:       now.Add(-time.Second),
		AuthenticationTime: now.Add(-time.Second),
	}
	r.mu.Unlock()
	return token
}

func (r *walletAuthorizationMemoryRepository) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.intents)
}

func (r *walletAuthorizationMemoryRepository) pendingFlowCounts() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.starts), len(r.flows)
}

func assertWalletAuthorizationNoStore(t testing.TB, response *http.Response) {
	t.Helper()
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Pragma") != "no-cache" ||
		response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("security headers = %v", response.Header)
	}
}

func walletAuthorizationResponseCookie(t testing.TB, response *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response omitted cookie %q: %v", name, response.Header.Values("Set-Cookie"))
	return nil
}
