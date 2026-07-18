package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestQuickPaymentTokenUUIDAcceptsExactlyOneReference(t *testing.T) {
	encoded := encodePaymentTokenForTest(t, "token-1")

	tests := []struct {
		name       string
		req        ebs_fields.QuickPaymentFields
		uuidQuery  string
		tokenQuery string
		want       string
	}{
		{
			name: "body token",
			req: ebs_fields.QuickPaymentFields{
				EncodedPaymentToken: encoded,
			},
			want: "token-1",
		},
		{
			name:       "query token",
			tokenQuery: encoded,
			want:       "token-1",
		},
		{
			name:      "query uuid",
			uuidQuery: "token-1",
			want:      "token-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := quickPaymentTokenUUID(tt.req, tt.uuidQuery, tt.tokenQuery)
			if err != nil {
				t.Fatalf("quickPaymentTokenUUID() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("quickPaymentTokenUUID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestQuickPaymentTokenUUIDRejectsMissingInvalidOrAmbiguousReferences(t *testing.T) {
	encoded := encodePaymentTokenForTest(t, "token-1")
	emptyUUIDToken := encodePaymentTokenForTest(t, "")

	tests := []struct {
		name       string
		req        ebs_fields.QuickPaymentFields
		uuidQuery  string
		tokenQuery string
		wantErr    error
	}{
		{
			name:    "missing",
			wantErr: ErrMissingUUID,
		},
		{
			name: "invalid body token",
			req: ebs_fields.QuickPaymentFields{
				EncodedPaymentToken: "not-base64",
			},
			wantErr: ErrInvalidPaymentToken,
		},
		{
			name:       "invalid query token",
			tokenQuery: "not-base64",
			wantErr:    ErrInvalidPaymentToken,
		},
		{
			name: "empty token uuid",
			req: ebs_fields.QuickPaymentFields{
				EncodedPaymentToken: emptyUUIDToken,
			},
			wantErr: ErrInvalidPaymentToken,
		},
		{
			name: "body token plus uuid",
			req: ebs_fields.QuickPaymentFields{
				EncodedPaymentToken: encoded,
			},
			uuidQuery: "token-1",
			wantErr:   ErrAmbiguousPaymentToken,
		},
		{
			name:       "query token plus uuid",
			tokenQuery: encoded,
			uuidQuery:  "token-1",
			wantErr:    ErrAmbiguousPaymentToken,
		},
		{
			name: "body token plus query token",
			req: ebs_fields.QuickPaymentFields{
				EncodedPaymentToken: encoded,
			},
			tokenQuery: encoded,
			wantErr:    ErrAmbiguousPaymentToken,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := quickPaymentTokenUUID(tt.req, tt.uuidQuery, tt.tokenQuery)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("quickPaymentTokenUUID() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func encodePaymentTokenForTest(t *testing.T, uuid string) string {
	t.Helper()
	encoded, err := ebs_fields.Encode(&ebs_fields.Token{UUID: uuid, Amount: 10})
	if err != nil {
		t.Fatalf("encode payment token: %v", err)
	}
	return encoded
}

func TestResolveQuickPaymentAmount(t *testing.T) {
	tests := []struct {
		name            string
		storedAmount    int
		requestedAmount int
		want            int
		wantErr         error
	}{
		{name: "fixed amount exact match", storedAmount: 25, requestedAmount: 25, want: 25},
		{name: "fixed amount mismatch", storedAmount: 25, requestedAmount: 10, wantErr: ErrAmountMismatch},
		{name: "open amount uses requested amount", storedAmount: 0, requestedAmount: 25, want: 25},
		{name: "open amount rejects missing request amount", storedAmount: 0, requestedAmount: 0, wantErr: store.ErrInvalidAmount},
		{name: "open amount rejects negative request amount", storedAmount: 0, requestedAmount: -1, wantErr: store.ErrInvalidAmount},
		{name: "stored negative amount is invalid", storedAmount: -1, requestedAmount: 25, wantErr: store.ErrInvalidAmount},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveQuickPaymentAmount(tt.storedAmount, tt.requestedAmount)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("resolveQuickPaymentAmount() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveQuickPaymentAmount() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolveQuickPaymentAmount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestNoebsQuickPaymentRejectsIncompletePayerFieldsBeforeClaim(t *testing.T) {
	called := false
	cardVaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(cardVaultServer.Close)
	service := &Service{
		HTTPClient: cardVaultServer.Client(),
		NoebsConfig: ebs_fields.NoebsConfig{ServiceDiscovery: map[string]string{
			cardVaultServiceDiscoveryKey: cardVaultServer.URL,
		}},
	}

	_, err := service.NoebsQuickPayment(t.Context(), "tenant-a", 42, ebs_fields.QuickPaymentFields{}, "token-1", "")
	if !errors.Is(err, ErrInvalidQuickPaymentRequest) {
		t.Fatalf("NoebsQuickPayment() error = %v, want %v", err, ErrInvalidQuickPaymentRequest)
	}
	if called {
		t.Fatal("incomplete payer request reached card-vault claim")
	}
}

func TestQuickPaymentTerminalStatusFailsClosed(t *testing.T) {
	approved := ebs_fields.EBSParserFields{EBSResponse: ebs_fields.EBSResponse{
		UUID:            "rail-1",
		ResponseCode:    0,
		ResponseMessage: "Approved",
	}}
	rejected := ebs_fields.EBSParserFields{EBSResponse: ebs_fields.EBSResponse{
		UUID:            "rail-1",
		ResponseCode:    72,
		ResponseMessage: "Declined",
	}}
	tests := []struct {
		name string
		res  ebs_fields.EBSParserFields
		err  error
		want string
	}{
		{name: "authoritative success", res: approved, want: ebs_fields.PaymentTokenStatusPaid},
		{name: "success with local record failure", res: approved, err: store.ErrMissingUUID, want: ebs_fields.PaymentTokenStatusPaid},
		{name: "explicit rail rejection", res: rejected, err: &ebs_fields.CallError{Status: http.StatusBadGateway, Response: rejected, Err: errors.New("declined")}, want: ebs_fields.PaymentTokenStatusFailed},
		{name: "empty response", res: ebs_fields.EBSParserFields{}},
		{name: "transport timeout", err: &ebs_fields.CallError{Status: http.StatusGatewayTimeout, Err: errors.New("timeout")}},
		{name: "malformed rejection", res: rejected, err: &ebs_fields.CallError{Status: http.StatusInternalServerError, Response: rejected, Err: errors.New("decode")}},
		{name: "uncorrelated success", res: approved, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			railUUID := "rail-1"
			if tt.name == "uncorrelated success" {
				railUUID = "rail-2"
			}
			if got := quickPaymentTerminalStatus(tt.res, tt.err, railUUID); got != tt.want {
				t.Fatalf("quickPaymentTerminalStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestServiceCommandErrorMapsInvalidAmount(t *testing.T) {
	if err := errorForServiceCommandCode(store.ErrInvalidAmount.Error()); !errors.Is(err, store.ErrInvalidAmount) {
		t.Fatalf("errorForServiceCommandCode(invalid amount) = %v, want %v", err, store.ErrInvalidAmount)
	}
}

func TestQuickPaymentCardVaultClientSendsGatewayIdentity(t *testing.T) {
	var sawResolve bool
	var sawFinalize bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(gateway.GatewayTenantIDHeader) != "tenant-a" {
			t.Fatalf("tenant header = %q", r.Header.Get(gateway.GatewayTenantIDHeader))
		}
		if r.Header.Get(gateway.GatewayUserIDHeader) != "42" {
			t.Fatalf("user header = %q", r.Header.Get(gateway.GatewayUserIDHeader))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/internal/card-vault/quick-pay/resolve":
			sawResolve = true
			var cmd QuickPaymentTokenResolveCommand
			if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
				t.Fatalf("decode resolve command: %v", err)
			}
			if cmd.UUID != "token-1" || cmd.Amount != 10 {
				t.Fatalf("resolve command = %+v", cmd)
			}
			_ = json.NewEncoder(w).Encode(QuickPaymentTokenResolution{UUID: "token-1", RailUUID: "rail-1", ToCard: "9222081700000000", Amount: 10, RecipientUserID: 84})
		case "/internal/card-vault/quick-pay/finalize":
			sawFinalize = true
			var cmd QuickPaymentTokenFinalizationCommand
			if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
				t.Fatalf("decode finalize command: %v", err)
			}
			if cmd.UUID != "token-1" || cmd.RailUUID != "rail-1" || cmd.Status != ebs_fields.PaymentTokenStatusPaid {
				t.Fatalf("finalize command = %+v", cmd)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected card-vault path %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	service := &Service{
		HTTPClient: server.Client(),
		NoebsConfig: ebs_fields.NoebsConfig{
			ServiceDiscovery: map[string]string{
				cardVaultServiceDiscoveryKey: server.URL,
			},
		},
	}

	resolution, err := service.ResolveQuickPaymentTokenFromCardVault(t.Context(), "tenant-a", 42, QuickPaymentTokenResolveCommand{
		UUID:   "token-1",
		Amount: 10,
	})
	if err != nil {
		t.Fatalf("resolve quick-pay token through card-vault client: %v", err)
	}
	if resolution.UUID != "token-1" || resolution.RailUUID != "rail-1" || resolution.ToCard != "9222081700000000" || resolution.Amount != 10 || resolution.RecipientUserID != 84 {
		t.Fatalf("resolution = %+v", resolution)
	}
	if err := service.FinalizeQuickPaymentTokenInCardVault(t.Context(), "tenant-a", 42, QuickPaymentTokenFinalizationCommand{UUID: "token-1", RailUUID: "rail-1", Status: ebs_fields.PaymentTokenStatusPaid}); err != nil {
		t.Fatalf("finalize quick-pay token through card-vault client: %v", err)
	}
	if !sawResolve || !sawFinalize {
		t.Fatalf("sawResolve=%v sawFinalize=%v", sawResolve, sawFinalize)
	}
}

func TestNoebsQuickPaymentLeavesMalformedOutcomeUnfinalized(t *testing.T) {
	var sawResolve bool
	var sawFinalize bool
	cardVaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/card-vault/quick-pay/resolve":
			sawResolve = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(QuickPaymentTokenResolution{
				UUID:            "token-1",
				RailUUID:        "rail-1",
				ToCard:          "9222081700000000",
				Amount:          25,
				RecipientUserID: 84,
			})
		case "/internal/card-vault/quick-pay/finalize":
			sawFinalize = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected card-vault path %s", r.URL.Path)
		}
	}))
	t.Cleanup(cardVaultServer.Close)

	var sawEBS bool
	ebsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawEBS = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ebsServer.Close)

	service := &Service{
		HTTPClient: testHTTPClient(),
		NoebsConfig: ebs_fields.NoebsConfig{
			ConsumerIP:            ebsServer.URL + "/",
			KafkaTransactionTopic: testKafkaTransactionTopic,
			ServiceDiscovery: map[string]string{
				cardVaultServiceDiscoveryKey: cardVaultServer.URL,
			},
		},
	}
	req := ebs_fields.QuickPaymentFields{ConsumerCardTransferFields: ebs_fields.ConsumerCardTransferFields{
		ConsumerCommonFields: ebs_fields.ConsumerCommonFields{TranDateTime: "270526205500"},
		ConsumerCardHolderFields: ebs_fields.ConsumerCardHolderFields{
			Pan:     "9222081700009999",
			Ipin:    "encrypted-ipin",
			ExpDate: "2601",
		},
		AmountFields: ebs_fields.AmountFields{TranAmount: 25},
	}}

	_, err := service.NoebsQuickPayment(transactionActorContext(t, 42), "tenant-a", 42, req, "token-1", "")
	if !errors.Is(err, ErrPaymentOutcomeUnknown) {
		t.Fatalf("NoebsQuickPayment() error = %v, want %v", err, ErrPaymentOutcomeUnknown)
	}
	if !sawResolve || !sawEBS || sawFinalize {
		t.Fatalf("sawResolve=%v sawEBS=%v sawFinalize=%v", sawResolve, sawEBS, sawFinalize)
	}
}

func TestNoebsQuickPaymentSubmitsBillerHookThroughNotificationChat(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeEBSAdapter})

	var sawResolve bool
	var sawFinalize bool
	cardVaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/card-vault/quick-pay/resolve":
			assertCardVaultUserCommandHeaders(t, r, tenantID, 42)
			var cmd QuickPaymentTokenResolveCommand
			if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
				t.Fatalf("decode resolve command: %v", err)
			}
			if cmd.UUID != "token-1" || cmd.Amount != 25 {
				t.Fatalf("resolve command = %+v", cmd)
			}
			sawResolve = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(QuickPaymentTokenResolution{UUID: "token-1", RailUUID: "rail-1", ToCard: "9222081700000000", Amount: 25, RecipientUserID: 84})
		case "/internal/card-vault/quick-pay/finalize":
			assertCardVaultUserCommandHeaders(t, r, tenantID, 42)
			var cmd QuickPaymentTokenFinalizationCommand
			if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
				t.Fatalf("decode finalize command: %v", err)
			}
			if cmd.UUID != "token-1" || cmd.RailUUID != "rail-1" || cmd.Status != ebs_fields.PaymentTokenStatusPaid {
				t.Fatalf("finalize command = %+v", cmd)
			}
			sawFinalize = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected card-vault path %s", r.URL.Path)
		}
	}))
	t.Cleanup(cardVaultServer.Close)

	var sawEBS bool
	ebsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+ebs_fields.ConsumerCardTransferEndpoint {
			t.Fatalf("EBS path = %s", r.URL.Path)
		}
		body := readBodyForTest(t, r)
		if !bytes.Contains(body, []byte(`"toCard":"9222081700000000"`)) {
			t.Fatalf("EBS request missing card-vault destination PAN: %s", body)
		}
		if !bytes.Contains(body, []byte(`"dynamicFees":17`)) {
			t.Fatalf("EBS request missing configured quick-pay dynamic fee: %s", body)
		}
		if !bytes.Contains(body, []byte(`"UUID":"rail-1"`)) || bytes.Contains(body, []byte(`"UUID":"token-1"`)) || bytes.Contains(body, []byte(`"UUID":"quickpay-request-uuid"`)) {
			t.Fatalf("EBS request must use the payment token as its stable identity: %s", body)
		}
		sawEBS = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ebs_fields.EBSParserFields{
			EBSResponse: ebs_fields.EBSResponse{
				UUID:            "rail-1",
				ResponseCode:    0,
				ResponseMessage: "Approved",
				PAN:             "9222081700009999",
				ToCard:          "9222081700000000",
				TranAmount:      25,
			},
		})
	}))
	t.Cleanup(ebsServer.Close)

	var sawNotification bool
	notificationServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/notification-chat/biller-hook" {
			t.Fatalf("notification path = %s", r.URL.Path)
		}
		assertAdminCommandHeaders(t, r, tenantID)
		var cmd BillerHookCommand
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			t.Fatalf("decode biller hook command: %v", err)
		}
		if cmd.Token != "token-1" || cmd.EBS.UUID != "rail-1" || !cmd.IsSuccessful {
			t.Fatalf("biller hook command = %+v", cmd)
		}
		sawNotification = true
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(notificationServer.Close)

	service := &Service{
		Store:      storeSvc,
		HTTPClient: testHTTPClient(),
		NoebsConfig: ebs_fields.NoebsConfig{
			ConsumerIP:            ebsServer.URL + "/",
			KafkaTransactionTopic: testKafkaTransactionTopic,
			EBSDynamicFees: ebs_fields.DynamicFeesFields{
				CardTransferfees: 17,
			},
			ServiceDiscovery: map[string]string{
				cardVaultServiceDiscoveryKey:    cardVaultServer.URL,
				notificationServiceDiscoveryKey: notificationServer.URL,
			},
		},
	}

	res, err := service.NoebsQuickPayment(transactionActorContext(t, 42), tenantID, 42, ebs_fields.QuickPaymentFields{
		ConsumerCardTransferFields: ebs_fields.ConsumerCardTransferFields{
			ConsumerCommonFields: ebs_fields.ConsumerCommonFields{
				UUID:         "quickpay-request-uuid",
				TranDateTime: "270526205500",
			},
			ConsumerCardHolderFields: ebs_fields.ConsumerCardHolderFields{
				Pan:     "9222081700009999",
				Ipin:    "encrypted-ipin",
				ExpDate: "2601",
			},
			AmountFields: ebs_fields.AmountFields{
				TranAmount: 25,
			},
		},
	}, "token-1", "")
	if err != nil {
		t.Fatalf("quick pay: %v", err)
	}
	if res.UUID != "rail-1" {
		t.Fatalf("EBS response = %+v", res)
	}
	if !sawResolve || !sawFinalize || !sawEBS || !sawNotification {
		t.Fatalf("sawResolve=%v sawFinalize=%v sawEBS=%v sawNotification=%v", sawResolve, sawFinalize, sawEBS, sawNotification)
	}
	for _, userID := range []int64{42, 84} {
		history, err := service.GetTransactionsForUserID(t.Context(), tenantID, userID)
		if err != nil {
			t.Fatalf("get quick-pay participant %d history: %v", userID, err)
		}
		if len(history) != 1 || history[0].UUID != "rail-1" {
			t.Fatalf("quick-pay participant %d history = %+v", userID, history)
		}
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM users LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("ebs-adapter scope should not create user tables, err=%v", err)
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM cards LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("ebs-adapter scope should not create card tables, err=%v", err)
	}
}

func assertCardVaultUserCommandHeaders(t *testing.T, r *http.Request, tenantID string, userID int64) {
	t.Helper()
	if r.Header.Get(gateway.GatewayTenantIDHeader) != tenantID {
		t.Fatalf("tenant header = %q", r.Header.Get(gateway.GatewayTenantIDHeader))
	}
	if r.Header.Get(gateway.GatewayUserIDHeader) != fmt.Sprint(userID) {
		t.Fatalf("user header = %q", r.Header.Get(gateway.GatewayUserIDHeader))
	}
}
