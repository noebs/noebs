package activity

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/testdb"
	basestore "github.com/adonese/noebs/store"
	"github.com/adonese/noebs/wallet/psp"
	"github.com/adonese/noebs/wallet/psp/httpjson"
	walletstore "github.com/adonese/noebs/wallet/store"
	"go.temporal.io/sdk/temporal"
)

func TestRecordInteractionRequiresExplicitProvider(t *testing.T) {
	activities := &PSPActivities{Store: &walletstore.Store{}}

	err := activities.recordInteraction(context.Background(), walletstore.PSPInteraction{
		TenantID:        "tenant-a",
		InteractionType: "deposit_verify",
	})
	if !errors.Is(err, walletstore.ErrMissingProviderCode) {
		t.Fatalf("recordInteraction() error = %v, want %v", err, walletstore.ErrMissingProviderCode)
	}
}

func TestClassifyPSPDispatchErrorOnlyStopsPermanentRequests(t *testing.T) {
	for _, permanent := range []error{psp.ErrPSPPermanent, psp.ErrPSPRequestInvalid, psp.ErrPSPConfigInvalid} {
		err := classifyPSPDispatchError(permanent)
		var applicationError *temporal.ApplicationError
		if !errors.As(err, &applicationError) || applicationError.Type() != PSPDispatchRejectedErrorType || !applicationError.NonRetryable() {
			t.Fatalf("classified %v as %v", permanent, err)
		}
	}
	for _, retryable := range []error{psp.ErrPSPTemporary, psp.ErrPSPResponseInvalid} {
		if err := classifyPSPDispatchError(retryable); !errors.Is(err, retryable) {
			t.Fatalf("classified retryable %v as %v", retryable, err)
		}
	}
}

func TestHTTPJSONConflictRemainsRetryableAtActivityBoundary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()

	mapping := psp.ResponseMapping{
		TransactionID: []string{"id"},
		Status:        []string{"status"},
		Amount:        []string{"amount"},
		Currency:      []string{"currency"},
	}
	provider, err := httpjson.NewProvider(&psp.Config{
		ProviderCode:          "pay",
		APIBaseURL:            server.URL,
		IdempotencyHeaderName: "Idempotency-Key",
		SupportsWithdrawal:    true,
		DepositRequestMethod:  http.MethodPost,
		DepositRequestPath:    "/deposits",
		PayoutRequestMethod:   http.MethodPost,
		PayoutRequestPath:     "/payouts",
		PayoutResponseMapping: mapping,
		StatusRequestMethod:   http.MethodGet,
		StatusRequestPath:     "/status",
		StatusResponseMapping: mapping,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, providerErr := provider.SendPayout(context.Background(), psp.PayoutRequest{
		IdempotencyKey:  "withdrawal-idem",
		ClientReference: "withdrawal-1",
		Amount:          100,
		Currency:        "AED",
		Destination:     map[string]any{},
	})
	classified := classifyPSPDispatchError(providerErr)
	if !errors.Is(classified, psp.ErrPSPTemporary) {
		t.Fatalf("classified conflict = %v, want %v", classified, psp.ErrPSPTemporary)
	}
	var applicationError *temporal.ApplicationError
	if errors.As(classified, &applicationError) && applicationError.Type() == PSPDispatchRejectedErrorType {
		t.Fatalf("classified conflict as %s", PSPDispatchRejectedErrorType)
	}
}

func TestClassifyPSPDispatchPreparationErrorPreservesKnownNoCallOutcome(t *testing.T) {
	err := classifyPSPDispatchPreparationError(errors.New("database unavailable"))
	var applicationError *temporal.ApplicationError
	if !errors.As(err, &applicationError) || applicationError.Type() != PSPDispatchNotAttemptedErrorType || applicationError.NonRetryable() {
		t.Fatalf("preparation error = %v", err)
	}

	err = classifyPSPDispatchPreparationError(psp.ErrPSPConfigInvalid)
	if !errors.As(err, &applicationError) || applicationError.Type() != PSPDispatchRejectedErrorType || !applicationError.NonRetryable() {
		t.Fatalf("invalid config error = %v", err)
	}
}

func TestValidateDispatchIdempotencyKeyRequiresCanonicalValue(t *testing.T) {
	for _, key := range []string{"", " padded", "padded "} {
		if err := validateDispatchIdempotencyKey(key); !errors.Is(err, psp.ErrPSPRequestInvalid) {
			t.Fatalf("key %q error = %v, want %v", key, err, psp.ErrPSPRequestInvalid)
		}
	}
	if err := validateDispatchIdempotencyKey("withdrawal-idem"); err != nil {
		t.Fatalf("valid key: %v", err)
	}
}

func TestValidatePayoutResultBindsProviderSettlementToCommand(t *testing.T) {
	request := psp.PayoutRequest{ClientReference: "withdrawal-1", Amount: 100, Currency: "AED"}
	valid := psp.PayoutResult{ProviderTxID: "provider-1", Amount: 100, Currency: "AED", Status: "pending"}
	if err := validatePayoutResult(request, &valid); err != nil {
		t.Fatalf("valid payout result: %v", err)
	}
	for _, mutate := range []func(*psp.PayoutResult){
		func(result *psp.PayoutResult) { result.ProviderTxID = "" },
		func(result *psp.PayoutResult) { result.Amount++ },
		func(result *psp.PayoutResult) { result.Currency = "USD" },
		func(result *psp.PayoutResult) { result.Status = "unknown" },
	} {
		result := valid
		mutate(&result)
		if err := validatePayoutResult(request, &result); !errors.Is(err, psp.ErrPSPResponseInvalid) {
			t.Fatalf("mismatched payout result %+v: %v", result, err)
		}
	}
}

func TestValidateDepositResultBindsProviderSettlementToCommand(t *testing.T) {
	request := psp.DepositRequest{ClientReference: "deposit-1", Amount: 100, Currency: "AED"}
	valid := psp.DepositResult{
		ClientReference: request.ClientReference, ProviderTxID: "provider-1",
		Amount: request.Amount, Currency: request.Currency, Status: "pending",
	}
	if err := validateDepositResult(request, &valid); err != nil {
		t.Fatalf("valid deposit result: %v", err)
	}
	for _, mutate := range []func(*psp.DepositResult){
		func(result *psp.DepositResult) { result.ClientReference = "other" },
		func(result *psp.DepositResult) { result.ProviderTxID = "" },
		func(result *psp.DepositResult) { result.Amount++ },
		func(result *psp.DepositResult) { result.Currency = "USD" },
		func(result *psp.DepositResult) { result.Status = "unknown" },
	} {
		result := valid
		mutate(&result)
		if err := validateDepositResult(request, &result); !errors.Is(err, psp.ErrPSPResponseInvalid) {
			t.Fatalf("mismatched deposit result %+v: %v", result, err)
		}
	}
}

func TestValidateTransactionStatusResultBindsResolvedSettlement(t *testing.T) {
	params := GetStatusParams{Amount: 100, Currency: "AED"}
	valid := psp.TxStatus{ProviderTxID: "provider-1", Amount: 100, Currency: "AED", Status: "held"}
	if err := validateTransactionStatusResult(params, &valid); err != nil {
		t.Fatalf("valid transaction status: %v", err)
	}
	for _, mutate := range []func(*psp.TxStatus){
		func(result *psp.TxStatus) { result.ProviderTxID = "" },
		func(result *psp.TxStatus) { result.Amount++ },
		func(result *psp.TxStatus) { result.Currency = "USD" },
		func(result *psp.TxStatus) { result.Status = "unknown" },
	} {
		result := valid
		mutate(&result)
		if err := validateTransactionStatusResult(params, &result); !errors.Is(err, psp.ErrPSPResponseInvalid) {
			t.Fatalf("mismatched transaction status %+v: %v", result, err)
		}
	}
}

func TestNewPSPActivitiesUsesLoaderStoreForAuditing(t *testing.T) {
	storeSvc := &walletstore.Store{}
	activities := NewPSPActivities(&psp.Loader{Store: storeSvc}, psp.NewRegistry())
	if activities.Store != storeSvc {
		t.Fatalf("activities.Store = %p, want loader store %p", activities.Store, storeSvc)
	}
}

func TestResolveProviderRequiresAuditStoreBeforeProviderWork(t *testing.T) {
	activities := &PSPActivities{
		Loader:   &psp.Loader{},
		Registry: psp.NewRegistry(),
	}
	_, _, err := activities.resolveProvider(context.Background(), "tenant-a", "provider", "SDG", 1, "deposit", "")
	if !errors.Is(err, ErrMissingStore) {
		t.Fatalf("resolveProvider() error = %v, want %v", err, ErrMissingStore)
	}
}

func TestSendPayoutAuditRetryKeepsOneIdempotentInteraction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	container, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatal(err)
	}
	defer func() { _ = container.Terminate(context.Background()) }()

	const database = "wallet_ledger"
	databaseURL, err := container.CreateDatabaseForRole(ctx, database, "wallet_ledger_migrate")
	if err != nil {
		t.Fatal(err)
	}
	db, err := basestore.OpenFromConfig(databaseURL, basestore.DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = db.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer dropCancel()
		_ = container.DropDatabase(dropCtx, database)
	}()
	if err := basestore.MigrateScope(ctx, db, basestore.MigrationScopeWalletLedger); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO tenants(id, name, created_at)
		VALUES ('tenant-a', 'Tenant A', clock_timestamp())`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO psp_configs(
		tenant_id, provider_code, provider_name, api_base_url, enabled_currencies,
		idempotency_header_name, supports_withdrawal, deposit_response_mapping
	) VALUES ('tenant-a', 'pay', 'Pay', 'https://pay.invalid', ARRAY['AED'], 'Idempotency-Key', TRUE, '{}')`); err != nil {
		t.Fatal(err)
	}

	store := walletstore.New(db)
	unit, err := store.GetCurrencyUnit(ctx, "AED", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	provider := &recordingPayoutProvider{}
	registry := psp.NewRegistry()
	if err := registry.Register("pay", func(*psp.Config) (psp.Provider, error) { return provider, nil }); err != nil {
		t.Fatal(err)
	}
	loader := &psp.Loader{
		Store: store,
		Secrets: psp.SecretResolverFunc(func(context.Context, string, string) (psp.SecretBundle, error) {
			return psp.SecretBundle{}, nil
		}),
	}
	activities := NewPSPActivities(loader, registry)
	params := SendPayoutParams{
		TenantID: "tenant-a", ProviderCode: "pay", CurrencyUnitID: unit.ID,
		Request: psp.PayoutRequest{
			IdempotencyKey: "withdrawal-idem", ClientReference: "withdrawal-1",
			Amount: 100, Currency: "AED", Destination: map[string]any{},
		},
	}
	for _, test := range []struct {
		name string
		id   int64
		want error
	}{
		{name: "missing", id: 0, want: walletstore.ErrMissingCurrencyUnitID},
		{name: "invalid", id: -1, want: walletstore.ErrInvalidCurrencyUnitID},
	} {
		t.Run(test.name+" currency unit", func(t *testing.T) {
			invalid := params
			invalid.CurrencyUnitID = test.id
			if _, err := activities.SendPayout(ctx, invalid); !errors.Is(err, test.want) {
				t.Fatalf("SendPayout() error = %v, want %v", err, test.want)
			}
		})
	}
	provider.mu.Lock()
	providerCallsBeforeValidDispatch := len(provider.requests)
	provider.mu.Unlock()
	if providerCallsBeforeValidDispatch != 0 {
		t.Fatalf("invalid unit reached provider %d times", providerCallsBeforeValidDispatch)
	}
	if _, err := activities.GetTransactionStatus(ctx, GetStatusParams{
		TenantID:        "tenant-a",
		ProviderCode:    "pay",
		IdempotencyKey:  "status-idem",
		ClientReference: "withdrawal-1",
		Amount:          100,
		Currency:        "AED",
		CurrencyUnitID:  unit.ID,
	}); !errors.Is(err, walletstore.ErrMissingDirection) {
		t.Fatalf("GetTransactionStatus() error = %v, want %v", err, walletstore.ErrMissingDirection)
	}
	provider.mu.Lock()
	providerStatusCalls := len(provider.statusRequests)
	provider.mu.Unlock()
	if providerStatusCalls != 0 {
		t.Fatalf("missing direction reached provider %d times", providerStatusCalls)
	}

	activities.Store = &walletstore.Store{}
	if _, err := activities.SendPayout(ctx, params); !errors.Is(err, walletstore.ErrMissingStore) {
		t.Fatalf("first payout error = %v, want audit store failure", err)
	}
	activities.Store = store
	for range 2 {
		if _, err := activities.SendPayout(ctx, params); err != nil {
			t.Fatalf("idempotent payout retry: %v", err)
		}
	}

	provider.mu.Lock()
	requests := append([]psp.PayoutRequest(nil), provider.requests...)
	provider.mu.Unlock()
	if len(requests) != 3 {
		t.Fatalf("provider calls = %d, want three activity executions", len(requests))
	}
	for _, request := range requests {
		if request.IdempotencyKey != params.Request.IdempotencyKey {
			t.Fatalf("retry idempotency key = %q, want %q", request.IdempotencyKey, params.Request.IdempotencyKey)
		}
	}
	var interactionCount int
	if err := db.GetContext(ctx, &interactionCount, `SELECT count(*) FROM psp_interactions
		WHERE tenant_id = 'tenant-a' AND interaction_type = 'payout_send' AND idempotency_key = 'withdrawal-idem'`); err != nil {
		t.Fatal(err)
	}
	if interactionCount != 1 {
		t.Fatalf("dispatch interactions = %d, want one", interactionCount)
	}
}

type recordingPayoutProvider struct {
	mu             sync.Mutex
	requests       []psp.PayoutRequest
	statusRequests []psp.TransactionLookup
}

func (p *recordingPayoutProvider) CreateDeposit(context.Context, psp.DepositRequest) (*psp.DepositResult, error) {
	return nil, psp.ErrPSPPermanent
}

func (p *recordingPayoutProvider) SendPayout(_ context.Context, request psp.PayoutRequest) (*psp.PayoutResult, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	return &psp.PayoutResult{
		ProviderTxID: "provider-1", Amount: request.Amount, Currency: request.Currency,
		Status: "pending", RawResponse: map[string]any{"id": "provider-1"},
	}, nil
}

func (p *recordingPayoutProvider) GetTransactionStatus(_ context.Context, request psp.TransactionLookup) (*psp.TxStatus, error) {
	p.mu.Lock()
	p.statusRequests = append(p.statusRequests, request)
	p.mu.Unlock()
	return nil, psp.ErrPSPPermanent
}

func (p *recordingPayoutProvider) VerifyWebhook([]byte, string) bool { return false }
func (p *recordingPayoutProvider) Code() string                      { return "pay" }
func (p *recordingPayoutProvider) SupportedOperations() []psp.Operation {
	return []psp.Operation{psp.OperationWithdrawal}
}
