package backofficeauth

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/coreos/go-oidc/v3/oidc"
)

func TestServiceLoginAuthenticateRefreshAndLogoutAcrossRepositories(t *testing.T) {
	initialNow := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	clock := &testClock{now: initialNow}
	initialClaims := claimsForTest(t, initialNow.Add(-time.Minute), initialNow.Add(5*time.Minute))
	refreshedClaims := claimsForTest(t, initialNow.Add(4*time.Minute), initialNow.Add(15*time.Minute))
	var exchangeCalls atomic.Int32
	var refreshCalls atomic.Int32
	tokenServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.Form.Get("grant_type") {
		case "authorization_code":
			exchangeCalls.Add(1)
			_, _ = io.WriteString(writer, `{"access_token":"access-initial","refresh_token":"refresh-initial","refresh_expires_in":1800,"id_token":"id-login","token_type":"Bearer"}`)
		case "refresh_token":
			refreshCalls.Add(1)
			if request.Form.Get("refresh_token") != "refresh-initial" {
				t.Errorf("refresh token = %q", request.Form.Get("refresh_token"))
			}
			_, _ = io.WriteString(writer, `{"access_token":"access-refreshed","refresh_token":"refresh-refreshed","refresh_expires_in":1800,"token_type":"Bearer"}`)
		default:
			writer.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer tokenServer.Close()

	var loginNonce string
	oauthClient := oauthClientForTest(t, clock, tokenServer.URL,
		idTokenVerifierFunc(func(_ context.Context, raw string) (*oidc.IDToken, error) {
			if raw != "id-login" {
				return nil, errors.New("unexpected ID token")
			}
			return &oidc.IDToken{
				Issuer:   "https://api.noebs.sd/auth/realms/noebs",
				Audience: []string{"noebs-backoffice"},
				Subject:  "operator-1",
				Expiry:   initialNow.Add(10 * time.Minute),
				IssuedAt: initialNow.Add(-time.Minute),
				Nonce:    loginNonce,
			}, nil
		}),
		accessTokenVerifierFunc(func(_ context.Context, raw string) (tenantauth.Claims, error) {
			switch raw {
			case "access-initial":
				return initialClaims, nil
			case "access-refreshed":
				return refreshedClaims, nil
			default:
				return tenantauth.Claims{}, errors.New("unknown access token")
			}
		}),
	)
	repository := newMemoryRepository()
	service := serviceForTest(t, clock, repository, oauthClient)

	start, err := service.BeginLogin(context.Background(), "/backoffice/t/acme/wallet")
	if err != nil {
		t.Fatal(err)
	}
	authorizationURL, err := url.Parse(start.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	state := authorizationURL.Query().Get("state")
	loginNonce = authorizationURL.Query().Get("nonce")
	if state == "" || loginNonce == "" || start.FlowCookie == nil {
		t.Fatalf("login start = %+v", start)
	}
	if _, err := service.CompleteLogin(context.Background(), state, opaqueForTest(99), "authorization-code"); !errors.Is(err, ErrInvalidFlow) {
		t.Fatalf("wrong browser binding error = %v", err)
	}
	if exchangeCalls.Load() != 0 {
		t.Fatalf("wrong browser binding reached token endpoint")
	}
	complete, err := service.CompleteLogin(context.Background(), state, start.FlowCookie.Value, "authorization-code")
	if err != nil {
		t.Fatal(err)
	}
	if complete.SessionCookie == nil || complete.ClearFlowCookie == nil || complete.ClearFlowCookie.MaxAge != -1 ||
		complete.ReturnPath != "/backoffice/t/acme/wallet" ||
		complete.SessionCookie.Value == "access-initial" || complete.SessionCookie.Value == "refresh-initial" ||
		len(complete.Claims.Memberships()) != 1 {
		t.Fatalf("login complete = %+v", complete)
	}
	if _, err := service.CompleteLogin(context.Background(), state, start.FlowCookie.Value, "authorization-code"); !errors.Is(err, ErrInvalidFlow) {
		t.Fatalf("replayed callback error = %v", err)
	}
	if exchangeCalls.Load() != 1 {
		t.Fatalf("authorization exchanges = %d", exchangeCalls.Load())
	}

	authenticated, err := service.Authenticate(context.Background(), complete.SessionCookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	if authenticated.Claims.Identity().Subject != "operator-1" || authenticated.CSRFToken == "" || refreshCalls.Load() != 0 {
		t.Fatalf("authenticated session = %+v, refreshes=%d", authenticated, refreshCalls.Load())
	}
	csrf, err := NewCSRFProtector("https://dsa.adonese.sd")
	if err != nil {
		t.Fatal(err)
	}
	mutation := mutationRequest(http.MethodPost)
	mutation.Header.Set("Origin", "https://dsa.adonese.sd")
	mutation.Header.Set("Sec-Fetch-Site", "same-origin")
	if err := csrf.ValidateMutation(mutation, authenticated.CSRFToken, authenticated.CSRFToken); err != nil {
		t.Fatalf("authenticated session CSRF token: %v", err)
	}

	clock.Set(initialNow.Add(4*time.Minute + 30*time.Second))
	const concurrentRequests = 32
	var wg sync.WaitGroup
	errorsCh := make(chan error, concurrentRequests)
	csrfTokens := make(chan string, concurrentRequests)
	for request := 0; request < concurrentRequests; request++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session, err := service.Authenticate(context.Background(), complete.SessionCookie.Value)
			if err != nil {
				errorsCh <- err
				return
			}
			csrfTokens <- session.CSRFToken
		}()
	}
	wg.Wait()
	close(errorsCh)
	close(csrfTokens)
	for err := range errorsCh {
		t.Error(err)
	}
	for csrfToken := range csrfTokens {
		if csrfToken != authenticated.CSRFToken {
			t.Errorf("CSRF token changed during refresh")
		}
	}
	if refreshCalls.Load() != 1 {
		t.Fatalf("concurrent refresh calls = %d, want 1", refreshCalls.Load())
	}

	logout, err := service.Logout(context.Background(), complete.SessionCookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	if logout.ClearSessionCookie.MaxAge != -1 || !logout.ClearSessionCookie.Secure || !logout.ClearSessionCookie.HttpOnly {
		t.Fatalf("logout cookie = %#v", logout.ClearSessionCookie)
	}
	logoutURL, err := url.Parse(logout.EndSessionURL)
	if err != nil {
		t.Fatal(err)
	}
	if logoutURL.Query().Get("id_token_hint") != "id-login" ||
		logoutURL.Query().Get("client_id") != "noebs-backoffice" ||
		logoutURL.Query().Get("post_logout_redirect_uri") != "https://dsa.adonese.sd/backoffice/logged-out" {
		t.Fatalf("logout URL = %q", logout.EndSessionURL)
	}
	if _, err := service.Authenticate(context.Background(), complete.SessionCookie.Value); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("authentication after logout error = %v", err)
	}
}

func TestServiceRejectsNonCanonicalReturnPaths(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)}
	server := staticTokenServer(t, `{"error":"unused"}`)
	defer server.Close()
	oauthClient := oauthClientForTest(t, clock, server.URL,
		idTokenVerifierFunc(func(context.Context, string) (*oidc.IDToken, error) { return nil, errors.New("unused") }),
		accessTokenVerifierFunc(func(context.Context, string) (tenantauth.Claims, error) {
			return tenantauth.Claims{}, errors.New("unused")
		}),
	)
	service := serviceForTest(t, clock, newMemoryRepository(), oauthClient)
	for _, returnPath := range []string{
		"", "/backoffice", "/backoffice/", "/dashboard", "//evil.example/backoffice/tenants",
		"/backoffice/../admin", "/backoffice/%2e%2e/admin", "/backoffice/tenants?next=https://evil.example",
		"https://evil.example/backoffice/tenants", "/backoffice\\tenants",
	} {
		if _, err := service.BeginLogin(context.Background(), returnPath); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("BeginLogin(%q) error = %v", returnPath, err)
		}
	}
}

func TestServiceDeletesSessionWhenRefreshIsRevoked(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	clock := &testClock{now: now}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(writer, `{"error":"invalid_grant"}`)
	}))
	defer server.Close()
	oauthClient := oauthClientForTest(t, clock, server.URL,
		idTokenVerifierFunc(func(context.Context, string) (*oidc.IDToken, error) { return nil, errors.New("unused") }),
		accessTokenVerifierFunc(func(context.Context, string) (tenantauth.Claims, error) {
			return tenantauth.Claims{}, errors.New("unused")
		}),
	)
	repository := newMemoryRepository()
	service := serviceForTest(t, clock, repository, oauthClient)
	rawSessionID := opaqueForTest(42)
	sessionHash, err := digestOpaque(rawSessionID)
	if err != nil {
		t.Fatal(err)
	}
	csrfSecret := make([]byte, opaqueTokenBytes)
	if _, err := rand.Read(csrfSecret); err != nil {
		t.Fatal(err)
	}
	sealed, err := service.sealTokenMaterial(sessionHash, tokenMaterial{
		Version: tokenMaterialVersion, AccessToken: "access-old", RefreshToken: "refresh-old", IDToken: "id-old", CSRFSecret: csrfSecret,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateSession(context.Background(), SessionRecord{
		SessionHash: sessionHash, Issuer: "https://api.noebs.sd/auth/realms/noebs", Subject: "operator-1", Tokens: sealed,
		AccessExpiresAt: now.Add(30 * time.Second), RefreshExpiresAt: now.Add(30 * time.Minute),
		IdleExpiresAt: now.Add(30 * time.Minute), AbsoluteExpiresAt: now.Add(8 * time.Hour),
		LastSeenAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), rawSessionID); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("revoked session error = %v", err)
	}
	if _, err := repository.LoadSession(context.Background(), sessionHash); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("revoked session remained in store: %v", err)
	}
}

func TestServiceRechecksDeadlineAfterSessionLoad(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name              string
		idleExpiresAt     time.Time
		absoluteExpiresAt time.Time
	}{
		{name: "idle", idleExpiresAt: now.Add(time.Minute), absoluteExpiresAt: now.Add(8 * time.Hour)},
		{name: "absolute", idleExpiresAt: now.Add(time.Minute), absoluteExpiresAt: now.Add(time.Minute)},
	} {
		t.Run(test.name, func(t *testing.T) {
			clock := &testClock{now: now}
			server := staticTokenServer(t, `{"error":"unused"}`)
			defer server.Close()
			oauthClient := oauthClientForTest(t, clock, server.URL,
				idTokenVerifierFunc(func(context.Context, string) (*oidc.IDToken, error) { return nil, errors.New("unused") }),
				accessTokenVerifierFunc(func(context.Context, string) (tenantauth.Claims, error) {
					return tenantauth.Claims{}, errors.New("expired session reached token verification")
				}),
			)
			base := newMemoryRepository()
			repository := &advanceAfterLoadRepository{
				memoryRepository: base,
				clock:            clock,
				advanceTo:        now.Add(2 * time.Minute),
			}
			service := serviceForTest(t, clock, repository, oauthClient)
			rawSessionID := opaqueForTest(51)
			putSessionForTest(
				t, service, base, rawSessionID, now,
				now.Add(5*time.Minute), now.Add(10*time.Minute), test.idleExpiresAt, test.absoluteExpiresAt,
			)
			if _, err := service.Authenticate(context.Background(), rawSessionID); !errors.Is(err, ErrSessionExpired) {
				t.Fatalf("deadline crossed during load error = %v", err)
			}
			sessionHash, _ := digestOpaque(rawSessionID)
			if _, err := base.LoadSession(context.Background(), sessionHash); !errors.Is(err, ErrSessionNotFound) {
				t.Fatalf("expired session remained after load: %v", err)
			}
		})
	}
}

func TestServiceConcurrentTouchUsesAuthoritativeIdleDeadline(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	clock := &testClock{now: now}
	claims := claimsForTest(t, now.Add(-time.Minute), now.Add(time.Hour))
	server := staticTokenServer(t, `{"error":"unused"}`)
	defer server.Close()
	oauthClient := oauthClientForTest(t, clock, server.URL,
		idTokenVerifierFunc(func(context.Context, string) (*oidc.IDToken, error) { return nil, errors.New("unused") }),
		accessTokenVerifierFunc(func(_ context.Context, raw string) (tenantauth.Claims, error) {
			if raw != "access-old" {
				return tenantauth.Claims{}, errors.New("unexpected access token")
			}
			return claims, nil
		}),
	)
	base := newMemoryRepository()
	repository := &interleavedTouchRepository{
		memoryRepository:   base,
		bothLoaded:         make(chan struct{}),
		firstTouched:       make(chan struct{}),
		secondTouchWaiting: make(chan struct{}),
		releaseSecondTouch: make(chan struct{}),
	}
	service := serviceForTest(t, clock, repository, oauthClient)
	rawSessionID := opaqueForTest(53)
	originalIdleExpiry := now.Add(10 * time.Second)
	extendedIdleExpiry := now.Add(30 * time.Minute)
	putSessionForTest(
		t, service, base, rawSessionID, now.Add(-29*time.Minute),
		now.Add(time.Hour), now.Add(time.Hour), originalIdleExpiry, now.Add(8*time.Hour),
	)

	type result struct {
		session AuthenticatedSession
		err     error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			session, err := service.Authenticate(context.Background(), rawSessionID)
			results <- result{session: session, err: err}
		}()
	}
	waitForSignal(t, repository.firstTouched, "first session touch")
	waitForSignal(t, repository.secondTouchWaiting, "second session touch")
	clock.Set(originalIdleExpiry.Add(time.Second))
	close(repository.releaseSecondTouch)

	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if !result.session.IdleExpiresAt.Equal(extendedIdleExpiry) {
			t.Errorf("idle expiry = %s, want %s", result.session.IdleExpiresAt, extendedIdleExpiry)
		}
	}
	sessionHash, _ := digestOpaque(rawSessionID)
	stored, err := base.LoadSession(context.Background(), sessionHash)
	if err != nil {
		t.Fatalf("extended session was deleted: %v", err)
	}
	if !stored.IdleExpiresAt.Equal(extendedIdleExpiry) {
		t.Fatalf("stored idle expiry = %s, want %s", stored.IdleExpiresAt, extendedIdleExpiry)
	}
}

func TestServiceRejectsRefreshCrossingDeadlineAndQueuedCaller(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	clock := &testClock{now: now}
	refreshedClaims := claimsForTest(t, now, now.Add(2*time.Hour))
	refreshEntered := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRefresh) }) }
	var refreshCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Form.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q", request.Form.Get("grant_type"))
		}
		if refreshCalls.Add(1) == 1 {
			close(refreshEntered)
		}
		<-releaseRefresh
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"access_token":"access-new","refresh_token":"refresh-new","refresh_expires_in":1800,"token_type":"Bearer"}`)
	}))
	defer server.Close()
	defer release()
	oauthClient := oauthClientForTest(t, clock, server.URL,
		idTokenVerifierFunc(func(context.Context, string) (*oidc.IDToken, error) { return nil, errors.New("unused") }),
		accessTokenVerifierFunc(func(_ context.Context, raw string) (tenantauth.Claims, error) {
			if raw != "access-new" {
				return tenantauth.Claims{}, errors.New("unexpected access token")
			}
			return refreshedClaims, nil
		}),
	)
	base := newMemoryRepository()
	repository := &signalingRepository{
		memoryRepository:  base,
		secondLoadStarted: make(chan struct{}),
	}
	service := serviceForTest(t, clock, repository, oauthClient)
	rawSessionID := opaqueForTest(52)
	putSessionForTest(
		t, service, base, rawSessionID, now,
		now.Add(30*time.Second), now.Add(2*time.Minute), now.Add(2*time.Minute), now.Add(8*time.Hour),
	)

	firstResult := make(chan error, 1)
	go func() {
		_, err := service.Authenticate(context.Background(), rawSessionID)
		firstResult <- err
	}()
	waitForSignal(t, refreshEntered, "refresh request")
	secondResult := make(chan error, 1)
	go func() {
		_, err := service.Authenticate(context.Background(), rawSessionID)
		secondResult <- err
	}()
	waitForSignal(t, repository.secondLoadStarted, "queued session load")
	clock.Set(now.Add(3 * time.Minute))
	release()

	if err := waitForResult(t, firstResult, "refreshing caller"); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("refreshing caller error = %v", err)
	}
	if err := waitForResult(t, secondResult, "queued caller"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("queued caller error = %v", err)
	}
	if refreshCalls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls.Load())
	}
}

type testRepository interface {
	FlowRepository
	SessionRepository
}

func serviceForTest(t testing.TB, clock Clock, repository testRepository, oauthClient *OAuthClient) *Service {
	t.Helper()
	key := make([]byte, aes256KeyBytes)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	keys, err := NewKeyring(KeyringConfig{
		ActiveKeyID: "test-key",
		Keys:        map[string][]byte{"test-key": key},
		Entropy:     rand.Reader,
	})
	if err != nil {
		t.Fatal(err)
	}
	cookies, err := NewCookiePolicy(CookiePolicyConfig{
		FlowName:    "__Host-noebs_bo_flow",
		SessionName: "__Host-noebs_bo",
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ServiceConfig{
		Flows:            repository,
		Sessions:         repository,
		OAuth:            oauthClient,
		Keys:             keys,
		Cookies:          cookies,
		Clock:            clock,
		Entropy:          rand.Reader,
		FlowTTL:          5 * time.Minute,
		IdleTTL:          30 * time.Minute,
		AbsoluteTTL:      8 * time.Hour,
		RefreshSkew:      time.Minute,
		TouchInterval:    time.Minute,
		ReturnPathPrefix: "/backoffice/",
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func putSessionForTest(
	t testing.TB,
	service *Service,
	repository *memoryRepository,
	rawSessionID string,
	createdAt, accessExpiresAt, refreshExpiresAt, idleExpiresAt, absoluteExpiresAt time.Time,
) {
	t.Helper()
	sessionHash, err := digestOpaque(rawSessionID)
	if err != nil {
		t.Fatal(err)
	}
	csrfSecret := make([]byte, opaqueTokenBytes)
	if _, err := rand.Read(csrfSecret); err != nil {
		t.Fatal(err)
	}
	sealed, err := service.sealTokenMaterial(sessionHash, tokenMaterial{
		Version: tokenMaterialVersion, AccessToken: "access-old", RefreshToken: "refresh-old", IDToken: "id-old", CSRFSecret: csrfSecret,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateSession(context.Background(), SessionRecord{
		SessionHash: sessionHash, Issuer: "https://api.noebs.sd/auth/realms/noebs", Subject: "operator-1", Tokens: sealed,
		AccessExpiresAt: accessExpiresAt, RefreshExpiresAt: refreshExpiresAt,
		IdleExpiresAt: idleExpiresAt, AbsoluteExpiresAt: absoluteExpiresAt,
		LastSeenAt: createdAt, CreatedAt: createdAt, UpdatedAt: createdAt,
	}); err != nil {
		t.Fatal(err)
	}
}

type advanceAfterLoadRepository struct {
	*memoryRepository
	clock     *testClock
	advanceTo time.Time
}

func (r *advanceAfterLoadRepository) LoadSession(ctx context.Context, sessionHash Digest) (SessionRecord, error) {
	session, err := r.memoryRepository.LoadSession(ctx, sessionHash)
	if err == nil {
		r.clock.Set(r.advanceTo)
	}
	return session, err
}

type signalingRepository struct {
	*memoryRepository
	loadCalls         atomic.Int32
	secondLoadStarted chan struct{}
}

type interleavedTouchRepository struct {
	*memoryRepository
	loadCalls          atomic.Int32
	touchCalls         atomic.Int32
	bothLoaded         chan struct{}
	firstTouched       chan struct{}
	secondTouchWaiting chan struct{}
	releaseSecondTouch chan struct{}
}

func (r *interleavedTouchRepository) LoadSession(ctx context.Context, sessionHash Digest) (SessionRecord, error) {
	session, err := r.memoryRepository.LoadSession(ctx, sessionHash)
	if err != nil {
		return SessionRecord{}, err
	}
	if r.loadCalls.Add(1) == 2 {
		close(r.bothLoaded)
	}
	<-r.bothLoaded
	return session, nil
}

func (r *interleavedTouchRepository) TouchSession(
	ctx context.Context,
	sessionHash Digest,
	now, idleExpiresAt, touchBefore time.Time,
) (SessionRecord, error) {
	if r.touchCalls.Add(1) == 1 {
		session, err := r.memoryRepository.TouchSession(ctx, sessionHash, now, idleExpiresAt, touchBefore)
		close(r.firstTouched)
		return session, err
	}
	close(r.secondTouchWaiting)
	<-r.releaseSecondTouch
	return r.memoryRepository.TouchSession(ctx, sessionHash, now, idleExpiresAt, touchBefore)
}

func (r *signalingRepository) LoadSession(ctx context.Context, sessionHash Digest) (SessionRecord, error) {
	if r.loadCalls.Add(1) == 2 {
		close(r.secondLoadStarted)
	}
	return r.memoryRepository.LoadSession(ctx, sessionHash)
}

func waitForSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitForResult(t *testing.T, result <-chan error, name string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
		return nil
	}
}

type memoryRepository struct {
	mu       sync.Mutex
	flows    map[Digest]FlowRecord
	sessions map[Digest]SessionRecord
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		flows:    make(map[Digest]FlowRecord),
		sessions: make(map[Digest]SessionRecord),
	}
}

func (r *memoryRepository) CreateFlow(_ context.Context, flow FlowRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.flows[flow.StateHash]; exists {
		return ErrFlowConflict
	}
	r.flows[flow.StateHash] = flow
	return nil
}

func (r *memoryRepository) ConsumeFlow(_ context.Context, stateHash, browserHash Digest, now time.Time) (FlowRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	flow, exists := r.flows[stateHash]
	if !exists || flow.BrowserHash != browserHash || !flow.ExpiresAt.After(now) {
		return FlowRecord{}, ErrInvalidFlow
	}
	delete(r.flows, stateHash)
	return flow, nil
}

func (r *memoryRepository) CreateSession(_ context.Context, session SessionRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sessions[session.SessionHash]; exists {
		return ErrSessionConflict
	}
	r.sessions[session.SessionHash] = session
	return nil
}

func (r *memoryRepository) LoadSession(_ context.Context, sessionHash Digest) (SessionRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, exists := r.sessions[sessionHash]
	if !exists {
		return SessionRecord{}, ErrSessionNotFound
	}
	return session, nil
}

func (r *memoryRepository) RefreshSession(
	ctx context.Context,
	sessionHash Digest,
	clock Clock,
	refreshSkew time.Duration,
	refresh RefreshSessionFunc,
) (SessionRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, exists := r.sessions[sessionHash]
	if !exists {
		return SessionRecord{}, ErrSessionNotFound
	}
	lockedAt := clock.Now().UTC()
	if sessionDeadlinePassed(session, lockedAt) {
		delete(r.sessions, sessionHash)
		return SessionRecord{}, ErrSessionExpired
	}
	if session.AccessExpiresAt.After(lockedAt.Add(refreshSkew)) {
		return session, nil
	}
	updated, err := refresh(ctx, session)
	completedAt := clock.Now().UTC()
	if sessionDeadlinePassed(session, completedAt) {
		delete(r.sessions, sessionHash)
		return SessionRecord{}, ErrSessionExpired
	}
	if err != nil {
		if errors.Is(err, ErrSessionRevoked) {
			delete(r.sessions, sessionHash)
		}
		return SessionRecord{}, err
	}
	session.Tokens = updated.Tokens
	session.AccessExpiresAt = updated.AccessExpiresAt
	session.RefreshExpiresAt = updated.RefreshExpiresAt
	session.UpdatedAt = completedAt
	r.sessions[sessionHash] = session
	return session, nil
}

func (r *memoryRepository) TouchSession(
	_ context.Context,
	sessionHash Digest,
	now time.Time,
	idleExpiresAt time.Time,
	touchBefore time.Time,
) (SessionRecord, error) {
	if now.IsZero() || idleExpiresAt.IsZero() || touchBefore.After(now) {
		return SessionRecord{}, ErrInvalidInput
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	session, exists := r.sessions[sessionHash]
	if !exists {
		return SessionRecord{}, ErrSessionNotFound
	}
	if sessionDeadlinePassed(session, now) {
		delete(r.sessions, sessionHash)
		return SessionRecord{}, ErrSessionExpired
	}
	if session.LastSeenAt.After(touchBefore) {
		return session, nil
	}
	if !idleExpiresAt.After(now) {
		return SessionRecord{}, ErrInvalidInput
	}
	session.LastSeenAt = now
	session.IdleExpiresAt = minTime(idleExpiresAt, session.AbsoluteExpiresAt)
	session.UpdatedAt = now
	r.sessions[sessionHash] = session
	return session, nil
}

func (r *memoryRepository) DeleteSession(_ context.Context, sessionHash Digest) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sessions[sessionHash]; !exists {
		return false, nil
	}
	delete(r.sessions, sessionHash)
	return true, nil
}
