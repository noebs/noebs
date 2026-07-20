package transactionauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var testNow = time.Date(2026, time.July, 20, 4, 0, 0, 0, time.UTC)

func TestServiceAuthorizesAndClaimsExactlyOnce(t *testing.T) {
	service, repository, oauth, clock := serviceFixture(t)
	binding := testBinding()

	initiated, err := service.Begin(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := service.StartBrowser(context.Background(), initiated.BrowserStartToken)
	if err != nil {
		t.Fatal(err)
	}
	if challenge.AuthorizationURL == "" || challenge.BrowserBinding == "" {
		t.Fatalf("challenge = %+v", challenge)
	}
	if oauth.authorizationCalls != 1 {
		t.Fatalf("authorization calls = %d, want 1", oauth.authorizationCalls)
	}
	oauth.identity = VerifiedIdentity{
		Issuer:             binding.Issuer,
		Subject:            binding.Subject,
		ACR:                "urn:noebs:acr:google-totp",
		AuthenticationTime: clock.Now().Add(-time.Second),
	}
	state := repository.onlyStateToken(t, oauth.state)
	completed, err := service.Complete(context.Background(), state, challenge.BrowserBinding, "code")
	if err != nil {
		t.Fatal(err)
	}
	if completed.ExpiresAt != clock.Now().Add(2*time.Minute) {
		t.Fatalf("authorization expiry = %s", completed.ExpiresAt)
	}
	if err := service.Claim(context.Background(), initiated.IntentToken, binding); err != nil {
		t.Fatal(err)
	}
	if err := service.Claim(context.Background(), initiated.IntentToken, binding); !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("replayed claim error = %v", err)
	}
}

func TestClaimDoesNotConsumeOnBindingMismatch(t *testing.T) {
	mutations := map[string]func(*Binding){
		"tenant":      func(binding *Binding) { binding.TenantID = "other" },
		"issuer":      func(binding *Binding) { binding.Issuer = "https://other.example/realms/noebs" },
		"subject":     func(binding *Binding) { binding.Subject = "other-subject" },
		"operation":   func(binding *Binding) { binding.Operation = OperationWalletWithdrawal },
		"digest":      func(binding *Binding) { binding.RequestDigest = digestString("other-request") },
		"idempotency": func(binding *Binding) { binding.IdempotencyKey = "other-key" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			service, repository, oauth, clock := serviceFixture(t)
			binding := testBinding()
			initiated, challenge := completeBrowserStart(t, service, repository, oauth, binding)
			oauth.identity = VerifiedIdentity{
				Issuer:             binding.Issuer,
				Subject:            binding.Subject,
				ACR:                "urn:noebs:acr:google-totp",
				AuthenticationTime: clock.Now(),
			}
			if _, err := service.Complete(context.Background(), oauth.state, challenge.BrowserBinding, "code"); err != nil {
				t.Fatal(err)
			}
			wrong := binding
			mutate(&wrong)
			if err := service.Claim(context.Background(), initiated.IntentToken, wrong); !errors.Is(err, ErrAuthorizationDenied) {
				t.Fatalf("wrong binding error = %v", err)
			}
			if err := service.Claim(context.Background(), initiated.IntentToken, binding); err != nil {
				t.Fatalf("correct claim after mismatch: %v", err)
			}
		})
	}
}

func TestConcurrentClaimHasOneWinner(t *testing.T) {
	service, repository, oauth, clock := serviceFixture(t)
	binding := testBinding()
	initiated, challenge := completeBrowserStart(t, service, repository, oauth, binding)
	oauth.identity = VerifiedIdentity{
		Issuer:             binding.Issuer,
		Subject:            binding.Subject,
		ACR:                "urn:noebs:acr:google-totp",
		AuthenticationTime: clock.Now(),
	}
	if _, err := service.Complete(context.Background(), oauth.state, challenge.BrowserBinding, "code"); err != nil {
		t.Fatal(err)
	}

	var winners atomic.Int64
	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if service.Claim(context.Background(), initiated.IntentToken, binding) == nil {
				winners.Add(1)
			}
		}()
	}
	wait.Wait()
	if winners.Load() != 1 {
		t.Fatalf("claim winners = %d, want 1", winners.Load())
	}
}

func TestFlowAndAuthorizationExpiryFailClosed(t *testing.T) {
	t.Run("browser start", func(t *testing.T) {
		service, _, _, clock := serviceFixture(t)
		initiated, err := service.Begin(context.Background(), testBinding())
		if err != nil {
			t.Fatal(err)
		}
		clock.Advance(10 * time.Minute)
		if _, err := service.StartBrowser(context.Background(), initiated.BrowserStartToken); !errors.Is(err, ErrInvalidBrowserStart) {
			t.Fatalf("expired browser start error = %v", err)
		}
	})

	t.Run("callback", func(t *testing.T) {
		service, repository, oauth, clock := serviceFixture(t)
		_, challenge := completeBrowserStart(t, service, repository, oauth, testBinding())
		clock.Advance(5 * time.Minute)
		if _, err := service.Complete(context.Background(), oauth.state, challenge.BrowserBinding, "code"); !errors.Is(err, ErrInvalidFlow) {
			t.Fatalf("expired callback error = %v", err)
		}
	})

	t.Run("claim", func(t *testing.T) {
		service, repository, oauth, clock := serviceFixture(t)
		binding := testBinding()
		initiated, challenge := completeBrowserStart(t, service, repository, oauth, binding)
		oauth.identity = VerifiedIdentity{
			Issuer:             binding.Issuer,
			Subject:            binding.Subject,
			ACR:                "urn:noebs:acr:google-totp",
			AuthenticationTime: clock.Now(),
		}
		if _, err := service.Complete(context.Background(), oauth.state, challenge.BrowserBinding, "code"); err != nil {
			t.Fatal(err)
		}
		clock.Advance(2 * time.Minute)
		if err := service.Claim(context.Background(), initiated.IntentToken, binding); !errors.Is(err, ErrAuthorizationDenied) {
			t.Fatalf("expired claim error = %v", err)
		}
	})
}

func TestFailedCallbackConsumesFlowWithoutAuthorizingIntent(t *testing.T) {
	service, repository, oauth, _ := serviceFixture(t)
	binding := testBinding()
	initiated, challenge := completeBrowserStart(t, service, repository, oauth, binding)
	oauth.exchangeErr = ErrOAuthExchange
	if _, err := service.Complete(context.Background(), oauth.state, challenge.BrowserBinding, "bad-code"); !errors.Is(err, ErrOAuthExchange) {
		t.Fatalf("failed callback error = %v", err)
	}
	if _, err := service.Complete(context.Background(), oauth.state, challenge.BrowserBinding, "code"); !errors.Is(err, ErrInvalidFlow) {
		t.Fatalf("callback replay error = %v", err)
	}
	if err := service.Claim(context.Background(), initiated.IntentToken, binding); !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("claim after failed callback error = %v", err)
	}
}

func TestCallbackIdentityMustMatchInitiator(t *testing.T) {
	validIdentity := VerifiedIdentity{
		Issuer:             testBinding().Issuer,
		Subject:            testBinding().Subject,
		ACR:                "urn:noebs:acr:google-totp",
		AuthenticationTime: testNow,
	}
	for name, mutate := range map[string]func(*VerifiedIdentity){
		"issuer":  func(identity *VerifiedIdentity) { identity.Issuer = "https://other.example/realms/noebs" },
		"subject": func(identity *VerifiedIdentity) { identity.Subject = "other-subject" },
	} {
		t.Run(name, func(t *testing.T) {
			service, repository, oauth, _ := serviceFixture(t)
			_, challenge := completeBrowserStart(t, service, repository, oauth, testBinding())
			identity := validIdentity
			mutate(&identity)
			oauth.identity = identity
			if _, err := service.Complete(context.Background(), oauth.state, challenge.BrowserBinding, "code"); !errors.Is(err, ErrAuthorizationDenied) {
				t.Fatalf("identity mismatch error = %v", err)
			}
		})
	}
}

func TestLateBrowserStartCannotOutliveIntent(t *testing.T) {
	service, _, _, clock := serviceFixture(t)
	initiated, err := service.Begin(context.Background(), testBinding())
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(9 * time.Minute)
	challenge, err := service.StartBrowser(context.Background(), initiated.BrowserStartToken)
	if err != nil {
		t.Fatal(err)
	}
	if want := testNow.Add(10 * time.Minute); !challenge.ExpiresAt.Equal(want) {
		t.Fatalf("challenge expiry = %s, want parent expiry %s", challenge.ExpiresAt, want)
	}
}

func TestBindingValidationUsesTypedErrors(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Binding)
		want error
	}{
		{name: "tenant", edit: func(binding *Binding) { binding.TenantID = "" }, want: ErrMissingTenantID},
		{name: "invalid tenant", edit: func(binding *Binding) { binding.TenantID = "INVALID" }, want: ErrInvalidTenantID},
		{name: "issuer", edit: func(binding *Binding) { binding.Issuer = "" }, want: ErrMissingIssuer},
		{name: "invalid issuer", edit: func(binding *Binding) { binding.Issuer = "http://identity.example" }, want: ErrInvalidIssuer},
		{name: "subject", edit: func(binding *Binding) { binding.Subject = "" }, want: ErrMissingSubject},
		{name: "invalid subject", edit: func(binding *Binding) { binding.Subject = " subject " }, want: ErrInvalidSubject},
		{name: "operation", edit: func(binding *Binding) { binding.Operation = "" }, want: ErrInvalidOperation},
		{name: "digest", edit: func(binding *Binding) { binding.RequestDigest = Digest{} }, want: ErrMissingRequestDigest},
		{name: "idempotency", edit: func(binding *Binding) { binding.IdempotencyKey = "" }, want: ErrMissingIdempotencyKey},
		{name: "invalid idempotency", edit: func(binding *Binding) { binding.IdempotencyKey = " key " }, want: ErrInvalidIdempotencyKey},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binding := testBinding()
			test.edit(&binding)
			if err := binding.Validate(); !errors.Is(err, test.want) {
				t.Fatalf("Validate() error = %v, want %v", err, test.want)
			}
		})
	}
}

func completeBrowserStart(
	t *testing.T,
	service *Service,
	repository *memoryRepository,
	oauth *testOAuth,
	binding Binding,
) (Initiated, BrowserChallenge) {
	t.Helper()
	initiated, err := service.Begin(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := service.StartBrowser(context.Background(), initiated.BrowserStartToken)
	if err != nil {
		t.Fatal(err)
	}
	oauth.state = repository.onlyStateToken(t, oauth.state)
	return initiated, challenge
}

func serviceFixture(t *testing.T) (*Service, *memoryRepository, *testOAuth, *testClock) {
	t.Helper()
	repository := newMemoryRepository()
	oauth := &testOAuth{}
	clock := &testClock{now: testNow}
	entropy := &counterReader{}
	keys, err := NewKeyring(KeyringConfig{
		ActiveKeyID: "test-key",
		Keys:        map[string][]byte{"test-key": make([]byte, 32)},
		Entropy:     entropy,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ServiceConfig{
		Repository:       repository,
		OAuth:            oauth,
		Keys:             keys,
		Clock:            clock,
		Entropy:          entropy,
		RequiredACR:      "urn:noebs:acr:google-totp",
		BrowserStartTTL:  10 * time.Minute,
		FlowTTL:          5 * time.Minute,
		AuthorizationTTL: 2 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, repository, oauth, clock
}

func testBinding() Binding {
	return Binding{
		TenantID:       "alpha",
		Issuer:         "https://identity.example/realms/noebs",
		Subject:        "01J2TESTSUBJECT",
		Operation:      OperationWalletP2P,
		RequestDigest:  digestString("canonical-request"),
		IdempotencyKey: "transfer-123",
	}
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

type counterReader struct {
	mu    sync.Mutex
	value byte
}

func (r *counterReader) Read(target []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := range target {
		r.value++
		target[index] = r.value
	}
	return len(target), nil
}

type testOAuth struct {
	state              string
	authorizationCalls int
	identity           VerifiedIdentity
	exchangeErr        error
}

func (o *testOAuth) AuthorizationURL(state, nonce, verifier string) (string, error) {
	o.state = state
	o.authorizationCalls++
	return "https://identity.example/authorize?state=" + state, nil
}

func (o *testOAuth) Exchange(context.Context, string, string, Digest) (VerifiedIdentity, error) {
	if o.exchangeErr != nil {
		return VerifiedIdentity{}, o.exchangeErr
	}
	return o.identity, nil
}

type memoryRepository struct {
	mu          sync.Mutex
	intents     map[Digest]IntentRecord
	starts      map[Digest]Digest
	flows       map[Digest]FlowRecord
	stateTokens map[Digest]string
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		intents:     map[Digest]IntentRecord{},
		starts:      map[Digest]Digest{},
		flows:       map[Digest]FlowRecord{},
		stateTokens: map[Digest]string{},
	}
}

func (r *memoryRepository) CreateIntent(_ context.Context, intent IntentRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.intents[intent.IntentHash]; exists {
		return ErrIntentConflict
	}
	r.intents[intent.IntentHash] = intent
	r.starts[intent.BrowserStartHash] = intent.IntentHash
	return nil
}

func (r *memoryRepository) StartFlow(
	_ context.Context,
	startHash Digest,
	flow NewFlowRecord,
	now time.Time,
) (FlowRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	intentHash, ok := r.starts[startHash]
	if !ok {
		return FlowRecord{}, ErrInvalidBrowserStart
	}
	intent := r.intents[intentHash]
	if !intent.ExpiresAt.After(now) {
		return FlowRecord{}, ErrInvalidBrowserStart
	}
	delete(r.starts, startHash)
	intent.BrowserStartHash = Digest{}
	r.intents[intentHash] = intent
	if flow.ExpiresAt.After(intent.ExpiresAt) {
		flow.ExpiresAt = intent.ExpiresAt
	}
	stored := FlowRecord{NewFlowRecord: flow, IntentHash: intentHash, Binding: intent.Binding}
	r.flows[flow.StateHash] = stored
	return stored, nil
}

func (r *memoryRepository) ConsumeFlow(
	_ context.Context,
	stateHash Digest,
	browserHash Digest,
	now time.Time,
) (FlowRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	flow, ok := r.flows[stateHash]
	if !ok || flow.BrowserHash != browserHash || !flow.ExpiresAt.After(now) {
		return FlowRecord{}, ErrInvalidFlow
	}
	delete(r.flows, stateHash)
	intent, ok := r.intents[flow.IntentHash]
	if !ok || !intent.ExpiresAt.After(now) || !intent.AuthorizedAt.IsZero() {
		return FlowRecord{}, ErrInvalidFlow
	}
	flow.Binding = intent.Binding
	return flow, nil
}

func (r *memoryRepository) AuthorizeIntent(
	_ context.Context,
	intentHash Digest,
	authorizedAt time.Time,
	authenticationTime time.Time,
	expiresAt time.Time,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	intent, ok := r.intents[intentHash]
	if !ok || !intent.AuthorizedAt.IsZero() || !intent.ExpiresAt.After(authorizedAt) {
		return ErrAuthorizationDenied
	}
	intent.AuthorizedAt = authorizedAt
	intent.AuthenticationTime = authenticationTime
	intent.ExpiresAt = expiresAt
	r.intents[intentHash] = intent
	return nil
}

func (r *memoryRepository) ClaimIntent(_ context.Context, intentHash Digest, binding Binding, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	intent, ok := r.intents[intentHash]
	if !ok || intent.Binding != binding || intent.AuthorizedAt.IsZero() || !intent.ExpiresAt.After(now) {
		return ErrIntentNotFound
	}
	delete(r.intents, intentHash)
	return nil
}

func (r *memoryRepository) DeleteExpired(_ context.Context, before time.Time) (int64, error) {
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

func (r *memoryRepository) onlyStateToken(t *testing.T, token string) string {
	t.Helper()
	stateHash, err := digestOpaque(token)
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.flows[stateHash]; !ok {
		t.Fatalf("state %q not stored", token)
	}
	return token
}

var _ Repository = (*memoryRepository)(nil)
var _ CodeExchanger = (*testOAuth)(nil)
var _ io.Reader = (*counterReader)(nil)

func ExampleOperation() {
	fmt.Println(OperationWalletP2P)
	fmt.Println(OperationWalletWithdrawal)
	// Output:
	// wallet.p2p
	// wallet.withdrawal
}
