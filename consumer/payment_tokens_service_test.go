package consumer

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
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
