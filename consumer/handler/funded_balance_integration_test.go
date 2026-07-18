package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestOpaqueBalanceHTTPAtMostOnceAndOwnershipContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	postgres, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatalf("start postgres: %v", err)
	}
	databaseName := fmt.Sprintf("funded_balance_%d", time.Now().UnixNano())
	dbURL, err := postgres.CreateDatabase(ctx, databaseName)
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

	const tenantID = "tenant-funded-balance"
	for _, scope := range []string{store.MigrationScopeCardVault, store.MigrationScopeEBSAdapter} {
		if err := store.MigrateScope(ctx, db, tenantID, scope); err != nil {
			t.Fatalf("migrate %s: %v", scope, err)
		}
	}
	storeSvc := store.New(db, store.WithDataKey("funded-balance-test-data-key"))
	if err := storeSvc.EnsureTenant(ctx, tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}
	vaultService := &consumer.Service{Store: storeSvc}
	first := enrollHandlerTestCard(t, vaultService, tenantID, 101, "4242420000004242", "Daily")
	second := enrollHandlerTestCard(t, vaultService, tenantID, 101, "4242421111114242", "Backup")
	foreign := enrollHandlerTestCard(t, vaultService, tenantID, 202, "4242429999994242", "Foreign")
	if first.MaskedPAN != second.MaskedPAN || second.MaskedPAN != foreign.MaskedPAN {
		t.Fatalf("fixture cards do not collide: %q %q %q", first.MaskedPAN, second.MaskedPAN, foreign.MaskedPAN)
	}

	var vaultClaims atomic.Int64
	vaultApp := fiber.New()
	vaultApp.Use(func(c *fiber.Ctx) error {
		if c.Path() == "/internal/card-vault/funded-operations/claim" {
			vaultClaims.Add(1)
			if c.Get(gateway.GatewayAdminIdentityHeader) != "" ||
				c.Get(gateway.GatewayAdminRoleHeader) != "" {
				return c.SendStatus(http.StatusUnauthorized)
			}
		}
		c.Locals("tenant_id", c.Get(gateway.GatewayTenantIDHeader))
		return c.Next()
	})
	RegisterCardVaultAdminInternalRoutes(vaultApp.Group("/internal/card-vault"), &Handler{Service: vaultService})
	vaultHTTP := httptest.NewServer(adaptor.FiberApp(vaultApp))
	t.Cleanup(vaultHTTP.Close)

	fixture := newFundedBalanceFixture(t)
	t.Cleanup(fixture.Server.Close)
	transport := &fundedBalanceFaultTransport{
		base:      http.DefaultTransport,
		ebsHost:   mustURLHost(t, fixture.Server.URL),
		vaultHost: mustURLHost(t, vaultHTTP.URL),
	}
	ebsService := &consumer.Service{
		Store:           storeSvc,
		HTTPClient:      &http.Client{Transport: transport, Timeout: 10 * time.Second},
		WorkloadSigners: testEBSAdapterWorkloadSigners(t),
		NoebsConfig: ebs_fields.NoebsConfig{
			ConsumerID:            "fixture-app",
			ConsumerIP:            fixture.Server.URL + "/",
			EBSConsumerKey:        opaqueEnrollmentPublicKeyFixture,
			KafkaTransactionTopic: "fixture-transactions",
			OpaqueBalanceEnabled:  true,
			ServiceDiscovery:      map[string]string{"card-vault": vaultHTTP.URL},
		},
	}
	publicApp := fiber.New()
	publicApp.Use(func(c *fiber.Ctx) error {
		c.Locals("tenant_id", tenantID)
		userID, _ := strconv.ParseInt(c.Get("X-Test-User-ID", "101"), 10, 64)
		c.Locals("user_id", userID)
		return c.Next()
	})
	RegisterEBSAdapterAuthedRoutes(publicApp.Group("/consumer"), &Handler{Service: ebsService})
	publicHTTP := httptest.NewServer(adaptor.FiberApp(publicApp))
	t.Cleanup(publicHTTP.Close)
	client := &http.Client{Timeout: 15 * time.Second}

	success := fundedBalanceRequest(t, "123e4567-e89b-42d3-a456-426614174101", first.CardID)
	status, body, err := postFundedBalance(client, publicHTTP.URL, success, 101)
	if err != nil || status != http.StatusOK {
		t.Fatalf("first balance status=%d body=%s err=%v", status, body, err)
	}
	assertOpaqueBalanceResult(t, body, success.UUID)
	firstBody := append([]byte(nil), body...)
	status, body, err = postFundedBalance(client, publicHTTP.URL, success, 101)
	if err != nil || status != http.StatusOK {
		t.Fatalf("exact retry status=%d body=%s err=%v", status, body, err)
	}
	assertOpaqueBalanceResult(t, body, success.UUID)
	if !bytes.Equal(firstBody, body) || fixture.balanceCalls(success.UUID) != 1 || fixture.statusCalls(success.UUID) != 1 {
		t.Fatalf("exact retry changed result or resubmitted: first=%s retry=%s balance=%d status=%d",
			firstBody, body, fixture.balanceCalls(success.UUID), fixture.statusCalls(success.UUID))
	}

	ambiguous := fundedBalanceRequest(t, "123e4567-e89b-42d3-a456-426614174102", first.CardID)
	transport.dropNextBalance.Store(true)
	status, body, err = postFundedBalance(client, publicHTTP.URL, ambiguous, 101)
	if err != nil || status != http.StatusBadGateway || responseCode(body) != consumer.ErrFundedOutcomeUnknown.Error() {
		t.Fatalf("ambiguous first call status=%d body=%s err=%v", status, body, err)
	}
	status, body, err = postFundedBalance(client, publicHTTP.URL, ambiguous, 101)
	if err != nil || status != http.StatusOK {
		t.Fatalf("ambiguous retry status=%d body=%s err=%v", status, body, err)
	}
	assertOpaqueBalanceResult(t, body, ambiguous.UUID)
	if fixture.balanceCalls(ambiguous.UUID) != 1 || fixture.statusCalls(ambiguous.UUID) != 1 {
		t.Fatalf("ambiguous retry calls: balance=%d status=%d", fixture.balanceCalls(ambiguous.UUID), fixture.statusCalls(ambiguous.UUID))
	}

	concurrent := fundedBalanceRequest(t, "123e4567-e89b-42d3-a456-426614174103", first.CardID)
	concurrentBody, err := json.Marshal(concurrent)
	if err != nil {
		t.Fatalf("marshal concurrent request: %v", err)
	}
	const callers = 12
	start := make(chan struct{})
	results := make(chan fundedHTTPResult, callers)
	for range callers {
		go func() {
			<-start
			status, body, err := postFundedBalanceJSON(client, publicHTTP.URL, concurrentBody, 101)
			results <- fundedHTTPResult{status: status, body: body, err: err}
		}()
	}
	close(start)
	for range callers {
		result := <-results
		if result.err != nil || (result.status != http.StatusOK && result.status != http.StatusBadGateway) {
			t.Fatalf("concurrent result status=%d body=%s err=%v", result.status, result.body, result.err)
		}
	}
	if fixture.balanceCalls(concurrent.UUID) != 1 {
		t.Fatalf("concurrent duplicate submitted %d balance calls", fixture.balanceCalls(concurrent.UUID))
	}
	status, body, err = postFundedBalance(client, publicHTTP.URL, concurrent, 101)
	if err != nil || status != http.StatusOK {
		t.Fatalf("concurrent reconciliation status=%d body=%s err=%v", status, body, err)
	}
	assertOpaqueBalanceResult(t, body, concurrent.UUID)

	mismatch := fundedBalanceRequest(t, "123e4567-e89b-42d3-a456-426614174104", first.CardID)
	status, body, err = postFundedBalance(client, publicHTTP.URL, mismatch, 101)
	if err != nil || status != http.StatusOK {
		t.Fatalf("mismatch seed status=%d body=%s err=%v", status, body, err)
	}
	mismatch.CardAuthorization.CardID = second.CardID
	mismatch.RequestClaim, err = consumer.BalanceInquiryRequestClaim(second.CardID)
	if err != nil {
		t.Fatalf("second-card claim: %v", err)
	}
	status, body, err = postFundedBalance(client, publicHTTP.URL, mismatch, 101)
	if err != nil || status != http.StatusConflict || responseCode(body) != store.ErrFundedClaimMismatch.Error() {
		t.Fatalf("mismatched replay status=%d body=%s err=%v", status, body, err)
	}
	if fixture.balanceCalls(mismatch.UUID) != 1 {
		t.Fatalf("mismatched replay submitted %d balance calls", fixture.balanceCalls(mismatch.UUID))
	}

	foreignRequest := fundedBalanceRequest(t, "123e4567-e89b-42d3-a456-426614174105", foreign.CardID)
	status, body, err = postFundedBalance(client, publicHTTP.URL, foreignRequest, 101)
	if err != nil || status != http.StatusNotFound || responseCode(body) != store.ErrCardNotFound.Error() {
		t.Fatalf("foreign card status=%d body=%s err=%v", status, body, err)
	}
	if fixture.balanceCalls(foreignRequest.UUID) != 0 {
		t.Fatalf("foreign card reached EBS")
	}

	unsafe := fundedBalanceRequest(t, "123e4567-e89b-42d3-a456-426614174106", first.CardID)
	fixture.markMalformed(unsafe.UUID)
	status, body, err = postFundedBalance(client, publicHTTP.URL, unsafe, 101)
	if err != nil || status != http.StatusBadGateway || responseCode(body) != consumer.ErrUnsafeBalanceResponse.Error() {
		t.Fatalf("malformed balance status=%d body=%s err=%v", status, body, err)
	}
	status, body, err = postFundedBalance(client, publicHTTP.URL, unsafe, 101)
	if err != nil || status != http.StatusBadGateway || responseCode(body) != consumer.ErrUnsafeBalanceResponse.Error() {
		t.Fatalf("malformed reconciliation status=%d body=%s err=%v", status, body, err)
	}
	if fixture.balanceCalls(unsafe.UUID) != 1 || fixture.statusCalls(unsafe.UUID) != 1 {
		t.Fatalf("malformed retry calls: balance=%d status=%d", fixture.balanceCalls(unsafe.UUID), fixture.statusCalls(unsafe.UUID))
	}

	stranded := fundedBalanceRequest(t, "123e4567-e89b-42d3-a456-426614174112", first.CardID)
	transport.dropNextClaim.Store(true)
	status, body, err = postFundedBalance(client, publicHTTP.URL, stranded, 101)
	if err != nil || status != http.StatusBadGateway || responseCode(body) != consumer.ErrFundedOutcomeUnknown.Error() {
		t.Fatalf("lost internal grant status=%d body=%s err=%v", status, body, err)
	}
	status, body, err = postFundedBalance(client, publicHTTP.URL, stranded, 101)
	if err != nil || status != http.StatusBadGateway || responseCode(body) != consumer.ErrFundedOutcomeUnknown.Error() {
		t.Fatalf("lost-grant retry status=%d body=%s err=%v", status, body, err)
	}
	if fixture.balanceCalls(stranded.UUID) != 0 || fixture.statusCalls(stranded.UUID) != 1 {
		t.Fatalf("lost grant was unsafely resubmitted: balance=%d status=%d", fixture.balanceCalls(stranded.UUID), fixture.statusCalls(stranded.UUID))
	}

	beforeVault, beforeRail := vaultClaims.Load(), fixture.totalBalanceCalls()
	invalidRequests := []struct {
		name   string
		uuid   string
		status int
		mutate func(*consumer.OpaqueBalanceRequest)
	}{
		{"spaced UUID", "123e4567-e89b-42d3-a456-426614174107", http.StatusBadRequest, func(value *consumer.OpaqueBalanceRequest) { value.UUID += " " }},
		{"spaced card ID", "123e4567-e89b-42d3-a456-426614174108", http.StatusBadRequest, func(value *consumer.OpaqueBalanceRequest) { value.CardAuthorization.CardID += " " }},
		{"different rail UUID", "123e4567-e89b-42d3-a456-426614174109", http.StatusBadRequest, func(value *consumer.OpaqueBalanceRequest) {
			value.CardAuthorization.RailUUID = "123e4567-e89b-42d3-a456-426614174199"
		}},
		{"forged claim", "123e4567-e89b-42d3-a456-426614174110", http.StatusConflict, func(value *consumer.OpaqueBalanceRequest) { value.RequestClaim = "v1:" + strings.Repeat("a", 64) }},
		{"invalid IPIN block", "123e4567-e89b-42d3-a456-426614174111", http.StatusBadRequest, func(value *consumer.OpaqueBalanceRequest) { value.CardAuthorization.IPINBlock = "not-canonical-base64" }},
	}
	for _, test := range invalidRequests {
		invalid := fundedBalanceRequest(t, test.uuid, first.CardID)
		test.mutate(&invalid)
		status, body, err = postFundedBalance(client, publicHTTP.URL, invalid, 101)
		if err != nil || status != test.status {
			t.Fatalf("%s status=%d body=%s err=%v", test.name, status, body, err)
		}
	}
	extraJSON, err := json.Marshal(success)
	if err != nil {
		t.Fatalf("marshal strict request: %v", err)
	}
	extraJSON = append(extraJSON[:len(extraJSON)-1], []byte(`,"PAN":"4242420000004242"}`)...)
	status, body, err = postFundedBalanceJSON(client, publicHTTP.URL, extraJSON, 101)
	if err != nil || status != http.StatusBadRequest || responseCode(body) != "bad_request" {
		t.Fatalf("unknown public field status=%d body=%s err=%v", status, body, err)
	}
	if vaultClaims.Load() != beforeVault || fixture.totalBalanceCalls() != beforeRail {
		t.Fatalf("invalid requests crossed a boundary: vault %d->%d rail %d->%d", beforeVault, vaultClaims.Load(), beforeRail, fixture.totalBalanceCalls())
	}

	assertFundedPersistenceHasNoSecrets(t, db, tenantID, "4242420000004242", success.CardAuthorization.IPINBlock)
}

type fundedBalanceFixture struct {
	Server    *httptest.Server
	mu        sync.Mutex
	accepted  map[string]fundedRailRequest
	balances  map[string]int
	statuses  map[string]int
	malformed map[string]bool
}

type fundedRailRequest struct {
	UUID         string
	PAN          string
	IPIN         string
	Expiry       string
	TranDateTime string
}

func newFundedBalanceFixture(t *testing.T) *fundedBalanceFixture {
	t.Helper()
	fixture := &fundedBalanceFixture{
		accepted:  make(map[string]fundedRailRequest),
		balances:  make(map[string]int),
		statuses:  make(map[string]int),
		malformed: make(map[string]bool),
	}
	fixture.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode EBS request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/" + ebs_fields.ConsumerBalanceEndpoint:
			request := fundedRailRequest{
				UUID:         stringField(body, "UUID"),
				PAN:          stringField(body, "PAN"),
				IPIN:         stringField(body, "IPIN"),
				Expiry:       stringField(body, "expDate"),
				TranDateTime: stringField(body, "tranDateTime"),
			}
			decodedIPIN, decodeErr := base64.StdEncoding.DecodeString(request.IPIN)
			if _, err := store.NormalizeRailUUID(request.UUID); err != nil || len(request.PAN) != 16 || request.Expiry != "2912" ||
				len(request.TranDateTime) != 12 || body["applicationId"] != "fixture-app" || decodeErr != nil || len(decodedIPIN) != 256 {
				t.Errorf("invalid funded rail request: %#v", body)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			fixture.mu.Lock()
			fixture.balances[request.UUID]++
			fixture.accepted[request.UUID] = request
			malformed := fixture.malformed[request.UUID]
			fixture.mu.Unlock()
			fixture.writeBalance(w, request, malformed)
		case "/" + ebs_fields.ConsumerTransactionStatusEndpoint:
			original := stringField(body, "originalTranUUID")
			fixture.mu.Lock()
			fixture.statuses[original]++
			request, found := fixture.accepted[original]
			malformed := fixture.malformed[original]
			fixture.mu.Unlock()
			if !found {
				_ = json.NewEncoder(w).Encode(map[string]any{"responseCode": 25, "responseMessage": "Not found", "UUID": body["UUID"]})
				return
			}
			balance := fixture.balanceMap(request, malformed)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"responseCode": 0, "responseMessage": "Success", "UUID": body["UUID"], "balance": balance,
				"originalTransaction": map[string]any{
					"responseCode": 0, "responseMessage": "Success", "UUID": original,
					"PAN": request.PAN, "expDate": request.Expiry, "workingKey": "fixture-secret", "tranDateTime": request.TranDateTime,
				},
			})
		default:
			t.Errorf("unexpected EBS path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return fixture
}

func (f *fundedBalanceFixture) writeBalance(w http.ResponseWriter, request fundedRailRequest, malformed bool) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"responseCode": 0, "responseMessage": "Success", "UUID": request.UUID,
		"PAN": request.PAN, "expDate": request.Expiry, "workingKey": "fixture-secret", "tranDateTime": request.TranDateTime,
		"balance": f.balanceMap(request, malformed),
	})
}

func (f *fundedBalanceFixture) balanceMap(request fundedRailRequest, malformed bool) map[string]any {
	if malformed {
		return map[string]any{"available": request.PAN, "leger": 1200.25}
	}
	return map[string]any{
		"available": 1250.75,
		"leger":     1200.25,
		"PAN":       request.PAN,
		"IPIN":      "fixture-ipin-secret",
		"expDate":   request.Expiry,
		"unrelated": map[string]any{"rail": "private"},
	}
}

func (f *fundedBalanceFixture) markMalformed(uuid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.malformed[uuid] = true
}

func (f *fundedBalanceFixture) balanceCalls(uuid string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.balances[uuid]
}

func (f *fundedBalanceFixture) statusCalls(uuid string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statuses[uuid]
}

func (f *fundedBalanceFixture) totalBalanceCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, calls := range f.balances {
		total += calls
	}
	return total
}

type fundedBalanceFaultTransport struct {
	base            http.RoundTripper
	ebsHost         string
	vaultHost       string
	dropNextBalance atomic.Bool
	dropNextClaim   atomic.Bool
}

func (t *fundedBalanceFaultTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(req)
	if err != nil {
		return response, err
	}
	if req.URL.Host == t.ebsHost && req.URL.Path == "/"+ebs_fields.ConsumerBalanceEndpoint && t.dropNextBalance.CompareAndSwap(true, false) {
		discardResponse(response)
		return nil, errors.New("injected funded-balance response loss")
	}
	if req.URL.Host == t.vaultHost && req.URL.Path == "/internal/card-vault/funded-operations/claim" && t.dropNextClaim.CompareAndSwap(true, false) {
		discardResponse(response)
		return nil, errors.New("injected funded-operation grant loss")
	}
	return response, nil
}

type fundedHTTPResult struct {
	status int
	body   []byte
	err    error
}

func fundedBalanceRequest(t *testing.T, uuid, cardID string) consumer.OpaqueBalanceRequest {
	t.Helper()
	claim, err := consumer.BalanceInquiryRequestClaim(cardID)
	if err != nil {
		t.Fatalf("balance request claim: %v", err)
	}
	return consumer.OpaqueBalanceRequest{
		UUID:         uuid,
		RequestClaim: claim,
		CardAuthorization: consumer.CardAuthorization{
			CardID: cardID, RailUUID: uuid, IPINBlock: enrollmentIPINBlock(t, uuid),
		},
	}
}

func postFundedBalance(client *http.Client, baseURL string, request consumer.OpaqueBalanceRequest, userID int64) (int, []byte, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return 0, nil, err
	}
	return postFundedBalanceJSON(client, baseURL, body, userID)
}

func postFundedBalanceJSON(client *http.Client, baseURL string, body []byte, userID int64) (int, []byte, error) {
	request, err := http.NewRequest(http.MethodPost, baseURL+"/consumer/balance", bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Test-User-ID", strconv.FormatInt(userID, 10))
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	return response.StatusCode, responseBody, err
}

func assertOpaqueBalanceResult(t *testing.T, body []byte, uuid string) {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("decode opaque balance result: %v body=%s", err, body)
	}
	if len(value) != 2 || value["uuid"] != uuid {
		t.Fatalf("opaque balance envelope = %#v", value)
	}
	balance, ok := value["balance"].(map[string]any)
	if !ok || len(balance) != 2 || balance["available"] != 1250.75 || balance["ledger"] != 1200.25 {
		t.Fatalf("opaque balance result = %#v", value)
	}
	for _, secret := range []string{"4242420000004242", "2912", "fixture-ipin-secret", "fixture-secret", "unrelated", "rail"} {
		if bytes.Contains(body, []byte(secret)) {
			t.Fatalf("public balance contains rail-only value %q: %s", secret, body)
		}
	}
}

func responseCode(body []byte) string {
	var payload struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &payload)
	return payload.Code
}

func stringField(body map[string]any, key string) string {
	value, _ := body[key].(string)
	return value
}

func assertFundedPersistenceHasNoSecrets(t *testing.T, db *store.DB, tenantID, pan, ipinBlock string) {
	t.Helper()
	var claims, transactions, events string
	if err := db.GetContext(context.Background(), &claims, db.Rebind(`SELECT COALESCE(string_agg(row_to_json(c)::text, ''), '')
		FROM card_funded_operation_claims c WHERE tenant_id = ?`), tenantID); err != nil {
		t.Fatalf("read funded claims: %v", err)
	}
	if err := db.GetContext(context.Background(), &transactions, db.Rebind(`SELECT COALESCE(string_agg(payload::text, ''), '')
		FROM transactions WHERE tenant_id = ?`), tenantID); err != nil {
		t.Fatalf("read funded transactions: %v", err)
	}
	if err := db.GetContext(context.Background(), &events, db.Rebind(`SELECT COALESCE(string_agg(payload::text, ''), '')
		FROM transaction_events WHERE tenant_id = ?`), tenantID); err != nil {
		t.Fatalf("read funded events: %v", err)
	}
	for name, durable := range map[string]string{"claims": claims, "transactions": transactions, "events": events} {
		for _, secret := range []string{pan, "2912", ipinBlock, "fixture-secret", "fixture-ipin-secret"} {
			if strings.Contains(durable, secret) {
				t.Fatalf("%s persisted rail secret %q", name, secret)
			}
		}
	}
	var unmasked int
	if err := db.GetContext(context.Background(), &unmasked, db.Rebind(`SELECT COUNT(*) FROM transactions
		WHERE tenant_id = ? AND pan = ?`), tenantID, pan); err != nil {
		t.Fatalf("inspect transaction PAN: %v", err)
	}
	if unmasked != 0 {
		t.Fatalf("persisted %d unmasked funded transaction PANs", unmasked)
	}
}
