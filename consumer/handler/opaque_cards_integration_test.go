package handler

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/testdb"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/adaptor/v2"
	"github.com/gofiber/fiber/v2"
)

const opaqueEnrollmentPublicKeyFixture = "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA4Jj+8WL5ANXllkz9lkOKRmXnDzQ+yS/VFKxKttkk4o5duJPPFZzJ0E3/m1F6xqEVPH2aM2IpSKN/SgeBv9NL6y+qgms7GbpnQ8MCilLIFWNGuTeRzDNVIR7yIqQ0jHX3dgrJyiDp02LQnQtMTRhzOYDZnwOnweixwEzAk8yPEeXQyzp867rUsLZ4jIIChRcI06UTFdMQrd7KZReTt5hunjQLH+qJBaMj1yAQGmf9C10MeC3Nnp4oE7m0OuTkTvekHnsaAtyY+TFg/UBvMQOyp9uJG6OwdvV6doI3MmXg16K6WJx1J1xewG6e28Tvt13z5mEljj8dnWQcqmhuASRlZwIDAQAB"

func TestOpaqueCardHTTPEnrollmentRetryAndPublicIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	postgres, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatalf("start postgres: %v", err)
	}
	const databaseName = "card_vault"
	dbURL, err := postgres.CreateDatabaseForRole(ctx, databaseName, "card_vault_migrate")
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	db, err := store.OpenFromConfig(dbURL, store.DriverPostgres)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_ = postgres.DropDatabase(context.Background(), databaseName)
		_ = postgres.Terminate(context.Background())
	})
	const tenantID = "tenant-opaque-http"
	if err := store.MigrateScope(ctx, db, store.MigrationScopeCardVault); err != nil {
		t.Fatalf("migrate card vault: %v", err)
	}
	storeSvc := store.New(db, store.WithDataKey("opaque-card-http-data-key"))
	provisionHandlerTestTenant(t, ctx, storeSvc, tenantID, "Opaque Card HTTP Tenant")
	vaultService := &consumer.Service{Store: storeSvc}
	vaultHandler := &Handler{Service: vaultService}

	vaultApp := fiber.New()
	vaultApp.Use(func(c *fiber.Ctx) error {
		c.Locals("tenant_id", c.Get(gateway.GatewayTenantIDHeader))
		userID, _ := strconv.ParseInt(c.Get(gateway.GatewayUserIDHeader), 10, 64)
		c.Locals("user_id", userID)
		return c.Next()
	})
	RegisterCardVaultInternalRoutes(vaultApp.Group("/internal/card-vault"), vaultHandler)
	RegisterCardVaultAdminInternalRoutes(vaultApp.Group("/internal/card-vault"), vaultHandler)
	vaultHTTP := httptest.NewServer(adaptor.FiberApp(vaultApp))
	t.Cleanup(vaultHTTP.Close)
	for _, path := range []string{
		"/internal/card-vault/cards/resolve-owned-rail",
		"/internal/card-vault/cards/resolve-main-rail",
	} {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, vaultHTTP.URL+path, strings.NewReader(`{}`))
		if err != nil {
			t.Fatalf("build absent resolver request: %v", err)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("call absent resolver %s: %v", path, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("generic resolver %s status = %d, want 404", path, response.StatusCode)
		}
	}

	fixture := newEnrollmentEBSFixture(t)
	t.Cleanup(fixture.Server.Close)
	transport := &enrollmentFaultTransport{
		base:                 http.DefaultTransport,
		cardVaultHost:        mustURLHost(t, vaultHTTP.URL),
		ebsHost:              mustURLHost(t, fixture.Server.URL),
		dropCompleteResponse: true,
	}
	ebsAdapter := &consumer.Service{
		HTTPClient:      &http.Client{Transport: transport, Timeout: 10 * time.Second},
		WorkloadSigners: testEBSAdapterWorkloadSigners(t),
		NoebsConfig: ebs_fields.NoebsConfig{
			ConsumerID:       "fixture-app",
			ConsumerIP:       fixture.Server.URL + "/",
			EBSConsumerKey:   opaqueEnrollmentPublicKeyFixture,
			ServiceDiscovery: map[string]string{"card-vault": vaultHTTP.URL},
		},
	}
	invalidKeyAdapter := *ebsAdapter
	invalidKeyAdapter.NoebsConfig.EBSConsumerKey = "not-a-public-key"
	if _, err := invalidKeyAdapter.CreateOpaqueCardEnrollmentIntent(ctx, tenantID, 101); !errors.Is(err, consumer.ErrInvalidEnrollmentPublicKey) {
		t.Fatalf("invalid enrollment key error = %v, want %v", err, consumer.ErrInvalidEnrollmentPublicKey)
	}
	var intentCount int
	if err := db.GetContext(ctx, &intentCount, db.Rebind(`SELECT COUNT(*) FROM card_enrollment_intents WHERE tenant_id = ?`), tenantID); err != nil {
		t.Fatalf("count intents after invalid key: %v", err)
	}
	if intentCount != 0 {
		t.Fatalf("invalid key created %d enrollment intents", intentCount)
	}

	intent, err := ebsAdapter.CreateOpaqueCardEnrollmentIntent(ctx, tenantID, 101)
	if err != nil {
		t.Fatalf("create public enrollment intent: %v", err)
	}
	if intent.RailKey.PublicKey != opaqueEnrollmentPublicKeyFixture || !strings.HasPrefix(intent.RailKey.KeyID, "sha256:") {
		t.Fatalf("rail key metadata = %+v", intent.RailKey)
	}
	confirm := consumer.ConfirmCardEnrollmentRequest{
		RailUUID:  intent.RailUUID,
		PAN:       "4242424242424242",
		Expiry:    "2912",
		Name:      "Daily",
		IPINBlock: enrollmentIPINBlock(t, intent.RailUUID),
	}
	invalidBlock := confirm
	invalidBlock.IPINBlock = "not-canonical-base64"
	if _, err := ebsAdapter.ConfirmOpaqueCardEnrollment(ctx, tenantID, 101, intent.EnrollmentID, invalidBlock); !errors.Is(err, consumer.ErrInvalidIPINBlock) {
		t.Fatalf("invalid IPIN block error = %v, want %v", err, consumer.ErrInvalidIPINBlock)
	}
	var untouched int
	if err := db.GetContext(ctx, &untouched, db.Rebind(`SELECT COUNT(*) FROM card_enrollment_intents
		WHERE tenant_id = ? AND enrollment_id = ?::uuid AND status = 'pending'
		  AND request_claim IS NULL AND rail_submitted_at IS NULL`), tenantID, intent.EnrollmentID); err != nil {
		t.Fatalf("inspect intent after invalid IPIN block: %v", err)
	}
	if untouched != 1 || fixture.balanceCalls(intent.RailUUID) != 0 {
		t.Fatalf("invalid IPIN block mutated intent or called rail: untouched=%d rail_calls=%d", untouched, fixture.balanceCalls(intent.RailUUID))
	}
	if _, err := ebsAdapter.ConfirmOpaqueCardEnrollment(ctx, tenantID, 101, intent.EnrollmentID, confirm); !errors.Is(err, consumer.ErrCardVaultCommand) {
		t.Fatalf("dropped completion response error = %v, want %v", err, consumer.ErrCardVaultCommand)
	}
	card, err := ebsAdapter.ConfirmOpaqueCardEnrollment(ctx, tenantID, 101, intent.EnrollmentID, confirm)
	if err != nil {
		t.Fatalf("retry completed enrollment: %v", err)
	}
	if card.MaskedPAN != "****4242" || card.CardID == "" || !card.IsMain {
		t.Fatalf("completed card = %+v", card)
	}
	if fixture.balanceCalls(intent.RailUUID) != 1 || fixture.statusCalls.Load() != 0 {
		t.Fatalf("rail calls after completed retry: balance=%d status=%d", fixture.balanceCalls(intent.RailUUID), fixture.statusCalls.Load())
	}
	if transport.completeCalls.Load() != 1 {
		t.Fatalf("complete calls = %d, want 1", transport.completeCalls.Load())
	}
	changed := confirm
	changed.Expiry = "3012"
	if _, err := ebsAdapter.ConfirmOpaqueCardEnrollment(ctx, tenantID, 101, intent.EnrollmentID, changed); !errors.Is(err, store.ErrEnrollmentClaimMismatch) {
		t.Fatalf("changed completed retry error = %v, want %v", err, store.ErrEnrollmentClaimMismatch)
	}

	transport.dropBalanceResponse.Store(true)
	secondIntent, err := ebsAdapter.CreateOpaqueCardEnrollmentIntent(ctx, tenantID, 101)
	if err != nil {
		t.Fatalf("create second intent: %v", err)
	}
	secondConfirm := consumer.ConfirmCardEnrollmentRequest{
		RailUUID:  secondIntent.RailUUID,
		PAN:       "5555555555554242",
		Expiry:    "2912",
		Name:      "Daily",
		IPINBlock: enrollmentIPINBlock(t, secondIntent.RailUUID),
	}
	if _, err := ebsAdapter.ConfirmOpaqueCardEnrollment(ctx, tenantID, 101, secondIntent.EnrollmentID, secondConfirm); !errors.Is(err, consumer.ErrEnrollmentOutcomeUnknown) {
		t.Fatalf("dropped rail response error = %v, want %v", err, consumer.ErrEnrollmentOutcomeUnknown)
	}
	secondCard, err := ebsAdapter.ConfirmOpaqueCardEnrollment(ctx, tenantID, 101, secondIntent.EnrollmentID, secondConfirm)
	if err != nil {
		t.Fatalf("reconcile second enrollment: %v", err)
	}
	if secondCard.CardID == card.CardID || secondCard.MaskedPAN != card.MaskedPAN {
		t.Fatalf("colliding-mask cards = first:%+v second:%+v", card, secondCard)
	}
	if fixture.balanceCalls(secondIntent.RailUUID) != 1 || fixture.statusCalls.Load() != 1 {
		t.Fatalf("rail calls after reconciliation: balance=%d status=%d", fixture.balanceCalls(secondIntent.RailUUID), fixture.statusCalls.Load())
	}

	foreign := enrollHandlerTestCard(t, vaultService, tenantID, 202, "4000000000004242", "Foreign")
	publicApp := fiber.New()
	publicApp.Use(func(c *fiber.Ctx) error {
		c.Locals("tenant_id", tenantID)
		userID, _ := strconv.ParseInt(c.Get("X-Test-User-ID", "101"), 10, 64)
		c.Locals("user_id", userID)
		return c.Next()
	})
	RegisterCardVaultAuthedRoutes(publicApp.Group("/consumer"), vaultHandler)

	listResponse := testFiberRequest(t, publicApp, http.MethodGet, "/consumer/cards", "", 101)
	if listResponse.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", listResponse.StatusCode)
	}
	listBody := readResponseBody(t, listResponse)
	assertNoSensitiveCardJSONKeys(t, listBody)
	var listed struct {
		Cards []ebs_fields.CardSummary `json:"cards"`
	}
	if err := json.Unmarshal(listBody, &listed); err != nil {
		t.Fatalf("decode card list: %v", err)
	}
	if len(listed.Cards) != 2 || listed.Cards[0].CardID != card.CardID || listed.Cards[1].CardID != secondCard.CardID {
		t.Fatalf("listed cards = %+v", listed.Cards)
	}

	foreignPatch := testFiberRequest(t, publicApp, http.MethodPatch, "/consumer/cards/"+foreign.CardID, `{"name":"stolen"}`, 101)
	if foreignPatch.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign patch status = %d, want 404", foreignPatch.StatusCode)
	}
	_ = readResponseBody(t, foreignPatch)
	foreignDelete := testFiberRequest(t, publicApp, http.MethodDelete, "/consumer/cards/"+foreign.CardID, "", 101)
	if foreignDelete.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign delete status = %d, want 404", foreignDelete.StatusCode)
	}
	_ = readResponseBody(t, foreignDelete)
	foreignMain := testFiberRequest(t, publicApp, http.MethodPut, "/consumer/cards/"+foreign.CardID+"/main", "", 101)
	if foreignMain.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign main status = %d, want 404", foreignMain.StatusCode)
	}
	_ = readResponseBody(t, foreignMain)

	legacyBefore := countCards(t, db, tenantID)
	legacy := testFiberRequest(t, publicApp, http.MethodPost, "/consumer/add_card", `[{"pan":"4111111111111111","ipin":"1234"}]`, 101)
	if legacy.StatusCode != http.StatusNotFound {
		t.Fatalf("legacy add status = %d, want 404", legacy.StatusCode)
	}
	_ = readResponseBody(t, legacy)
	if after := countCards(t, db, tenantID); after != legacyBefore {
		t.Fatalf("legacy endpoint mutated cards: before=%d after=%d", legacyBefore, after)
	}

	setMain := testFiberRequest(t, publicApp, http.MethodPut, "/consumer/cards/"+secondCard.CardID+"/main", "", 101)
	if setMain.StatusCode != http.StatusNoContent {
		t.Fatalf("set main status = %d", setMain.StatusCode)
	}
	_ = readResponseBody(t, setMain)
	retire := testFiberRequest(t, publicApp, http.MethodDelete, "/consumer/cards/"+secondCard.CardID, "", 101)
	if retire.StatusCode != http.StatusNoContent {
		t.Fatalf("retire status = %d", retire.StatusCode)
	}
	_ = readResponseBody(t, retire)
	remaining, err := vaultService.ListOpaqueCardsForUserID(ctx, tenantID, 101)
	if err != nil || len(remaining) != 1 || remaining[0].CardID != card.CardID || !remaining[0].IsMain {
		t.Fatalf("remaining cards after main retirement = %+v, %v", remaining, err)
	}
}

type enrollmentEBSFixture struct {
	Server      *httptest.Server
	mu          sync.Mutex
	balances    map[string]int
	statusCalls atomic.Int64
}

func newEnrollmentEBSFixture(t *testing.T) *enrollmentEBSFixture {
	t.Helper()
	fixture := &enrollmentEBSFixture{balances: make(map[string]int)}
	fixture.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode EBS fixture request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/" + ebs_fields.ConsumerBalanceEndpoint:
			railUUID, _ := body["UUID"].(string)
			pan, _ := body["PAN"].(string)
			ipinBlock, _ := body["IPIN"].(string)
			expiry, _ := body["expDate"].(string)
			decodedIPIN, decodeErr := base64.StdEncoding.DecodeString(ipinBlock)
			if _, err := store.NormalizeRailUUID(railUUID); err != nil || len(pan) < 12 || expiry != "2912" ||
				decodeErr != nil || len(decodedIPIN) != 256 || base64.StdEncoding.EncodeToString(decodedIPIN) != ipinBlock {
				t.Errorf("unexpected enrollment rail request: %#v", body)
				_ = json.NewEncoder(w).Encode(map[string]any{"responseCode": ebs_fields.INVALIDCARD, "responseMessage": "Invalid card", "UUID": railUUID})
				return
			}
			fixture.mu.Lock()
			fixture.balances[railUUID]++
			fixture.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"responseCode": 0, "responseMessage": "Success", "UUID": railUUID,
				"PAN": pan, "expDate": expiry,
			})
		case "/" + ebs_fields.ConsumerTransactionStatusEndpoint:
			fixture.statusCalls.Add(1)
			original, _ := body["originalTranUUID"].(string)
			fixture.mu.Lock()
			seen := fixture.balances[original]
			fixture.mu.Unlock()
			if seen != 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{"responseCode": 25, "responseMessage": "Not found"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"responseCode": 0, "responseMessage": "Success", "UUID": body["UUID"],
				"originalTransaction": map[string]any{
					"responseCode": 0, "responseMessage": "Success", "UUID": original,
				},
			})
		default:
			t.Errorf("unexpected EBS fixture path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return fixture
}

func (f *enrollmentEBSFixture) balanceCalls(railUUID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.balances[railUUID]
}

type enrollmentFaultTransport struct {
	base                 http.RoundTripper
	cardVaultHost        string
	ebsHost              string
	dropCompleteResponse bool
	dropBalanceResponse  atomic.Bool
	completeCalls        atomic.Int64
	mu                   sync.Mutex
}

func (t *enrollmentFaultTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(req)
	if err != nil {
		return response, err
	}
	if req.URL.Host == t.cardVaultHost && req.URL.Path == "/internal/card-vault/enrollment-intents/complete" {
		t.completeCalls.Add(1)
		t.mu.Lock()
		drop := t.dropCompleteResponse
		t.dropCompleteResponse = false
		t.mu.Unlock()
		if drop {
			discardResponse(response)
			return nil, errors.New("injected completed-response loss")
		}
	}
	if req.URL.Host == t.ebsHost && req.URL.Path == "/"+ebs_fields.ConsumerBalanceEndpoint && t.dropBalanceResponse.CompareAndSwap(true, false) {
		discardResponse(response)
		return nil, errors.New("injected rail-response loss")
	}
	return response, nil
}

func discardResponse(response *http.Response) {
	if response == nil || response.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
}

func enrollHandlerTestCard(t *testing.T, service *consumer.Service, tenantID string, userID int64, pan, name string) ebs_fields.CardSummary {
	t.Helper()
	now := time.Now().UTC()
	intent, err := service.CreateCardEnrollmentIntentForUserID(context.Background(), tenantID, userID, now)
	if err != nil {
		t.Fatalf("create direct intent: %v", err)
	}
	_, err = service.BeginCardEnrollmentForUserID(context.Background(), tenantID, userID, consumer.BeginCardEnrollmentCommand{
		EnrollmentID: intent.EnrollmentID, PAN: pan, Expiry: "2912", Name: name,
	}, now)
	if err != nil {
		t.Fatalf("begin direct intent: %v", err)
	}
	claim, err := service.ClaimCardEnrollmentRailForUserID(context.Background(), tenantID, userID, consumer.ClaimCardEnrollmentRailCommand{EnrollmentID: intent.EnrollmentID}, now)
	if err != nil || !claim.Granted {
		t.Fatalf("claim direct intent = %+v, %v", claim, err)
	}
	card, err := service.CompleteCardEnrollmentForUserID(context.Background(), tenantID, userID, consumer.CompleteCardEnrollmentCommand{
		EnrollmentID: intent.EnrollmentID, PAN: pan, Expiry: "2912", Name: name,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("complete direct intent: %v", err)
	}
	return card
}

func testFiberRequest(t *testing.T, app *fiber.App, method, path, body string, userID int64) *http.Response {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("X-Test-User-ID", strconv.FormatInt(userID, 10))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := app.Test(request, 30_000)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return response
}

func readResponseBody(t *testing.T, response *http.Response) []byte {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return body
}

func countCards(t *testing.T, db *store.DB, tenantID string) int {
	t.Helper()
	var count int
	if err := db.GetContext(context.Background(), &count, db.Rebind(`SELECT COUNT(*) FROM cards WHERE tenant_id = ?`), tenantID); err != nil {
		t.Fatalf("count cards: %v", err)
	}
	return count
}

func assertNoSensitiveCardJSONKeys(t *testing.T, body []byte) {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode public card JSON: %v", err)
	}
	forbidden := map[string]struct{}{
		"pan": {}, "ipin": {}, "pin": {}, "id": {}, "user_id": {}, "mobile": {},
		"card_index": {}, "pan_fingerprint": {}, "pan_ciphertext": {}, "ciphertext": {},
	}
	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				visit(item)
			}
		case map[string]any:
			for key, item := range typed {
				if _, found := forbidden[strings.ToLower(key)]; found {
					t.Fatalf("public response contains forbidden key %q: %s", key, body)
				}
				visit(item)
			}
		}
	}
	visit(decoded)
}

func mustURLHost(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse URL %q: %v", raw, err)
	}
	return parsed.Host
}

func enrollmentIPINBlock(t *testing.T, railUUID string) string {
	t.Helper()
	der, err := base64.StdEncoding.DecodeString(opaqueEnrollmentPublicKeyFixture)
	if err != nil {
		t.Fatalf("decode enrollment public key fixture: %v", err)
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		t.Fatalf("parse enrollment public key fixture: %v", err)
	}
	publicKey, ok := parsed.(*rsa.PublicKey)
	if !ok || publicKey.N.BitLen() < 2048 {
		t.Fatalf("enrollment public key fixture is not a strong RSA key")
	}
	ciphertext, err := rsa.EncryptPKCS1v15(rand.Reader, publicKey, []byte(railUUID+"1234"))
	if err != nil {
		t.Fatalf("encrypt enrollment IPIN block fixture: %v", err)
	}
	return base64.StdEncoding.EncodeToString(ciphertext)
}
