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

func TestQuickPaymentCardVaultClientSendsGatewayIdentity(t *testing.T) {
	var sawResolve bool
	var sawMarkPaid bool
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
			_ = json.NewEncoder(w).Encode(QuickPaymentTokenResolution{UUID: "token-1", ToCard: "9222081700000000", Amount: 10})
		case "/internal/card-vault/quick-pay/mark-paid":
			sawMarkPaid = true
			var cmd QuickPaymentTokenPaidCommand
			if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
				t.Fatalf("decode mark-paid command: %v", err)
			}
			if cmd.UUID != "token-1" {
				t.Fatalf("mark-paid command = %+v", cmd)
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
	if resolution.UUID != "token-1" || resolution.ToCard != "9222081700000000" || resolution.Amount != 10 {
		t.Fatalf("resolution = %+v", resolution)
	}
	if err := service.MarkQuickPaymentTokenPaidInCardVault(t.Context(), "tenant-a", 42, QuickPaymentTokenPaidCommand{UUID: "token-1"}); err != nil {
		t.Fatalf("mark quick-pay token through card-vault client: %v", err)
	}
	if !sawResolve || !sawMarkPaid {
		t.Fatalf("sawResolve=%v sawMarkPaid=%v", sawResolve, sawMarkPaid)
	}
}

func TestNoebsQuickPaymentSubmitsBillerHookThroughNotificationChat(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeEBSAdapter})

	var sawResolve bool
	var sawMarkPaid bool
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
			_ = json.NewEncoder(w).Encode(QuickPaymentTokenResolution{UUID: "token-1", ToCard: "9222081700000000", Amount: 25})
		case "/internal/card-vault/quick-pay/mark-paid":
			assertCardVaultUserCommandHeaders(t, r, tenantID, 42)
			var cmd QuickPaymentTokenPaidCommand
			if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
				t.Fatalf("decode mark-paid command: %v", err)
			}
			if cmd.UUID != "token-1" {
				t.Fatalf("mark-paid command = %+v", cmd)
			}
			sawMarkPaid = true
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
		sawEBS = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ebs_fields.EBSParserFields{
			EBSResponse: ebs_fields.EBSResponse{
				UUID:            "quickpay-ebs-uuid",
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
		if cmd.Token != "token-1" || cmd.EBS.UUID != "quickpay-ebs-uuid" || !cmd.IsSuccessful {
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
			ServiceDiscovery: map[string]string{
				cardVaultServiceDiscoveryKey:    cardVaultServer.URL,
				notificationServiceDiscoveryKey: notificationServer.URL,
			},
		},
	}

	res, err := service.NoebsQuickPayment(context.Background(), tenantID, 42, ebs_fields.QuickPaymentFields{
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
	if res.UUID != "quickpay-ebs-uuid" {
		t.Fatalf("EBS response = %+v", res)
	}
	if !sawResolve || !sawMarkPaid || !sawEBS || !sawNotification {
		t.Fatalf("sawResolve=%v sawMarkPaid=%v sawEBS=%v sawNotification=%v", sawResolve, sawMarkPaid, sawEBS, sawNotification)
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
