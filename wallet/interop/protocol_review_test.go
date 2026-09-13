package interop

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/transactionauth"
	walletrequest "github.com/adonese/noebs/wallet/request"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

// Generated using getQuoteResponseIlp in the actual official v24.19.5 image,
// sha256:4d3b0ca0f2fd1dd8ea15957de38718de9cb4c64a4ecb919d1b3086c5da5ce788,
// with the literal test-only secret "synthetic-review-fixture-only". This is a
// cross-implementation wire fixture, not an encoding produced by our parser.
const reviewILPPacket = "AYIC7gAAAAAAAABlHWcuYmFua29uZS5tc2lzZG4uMjQ5OTEwMDAwMDAyggLEZXlKaGJXOTFiblFpT25zaVlXMXZkVzUwSWpvaU1TNHdNU0lzSW1OMWNuSmxibU41SWpvaVUwUkhJbjBzSW1WNGNHbHlZWFJwYjI0aU9pSXlNRE13TFRBeExUQXhWREF3T2pBMU9qQXdMakF3TUZvaUxDSndZWGxsWlNJNmV5SnVZVzFsSWpvaVUzbHVkR2hsZEdsaklGSmxZMmx3YVdWdWRDSXNJbkJoY25SNVNXUkpibVp2SWpwN0ltWnpjRWxrSWpvaVltRnVhMjl1WlNJc0luQmhjblI1U1dSVWVYQmxJam9pVFZOSlUwUk9JaXdpY0dGeWRIbEpaR1Z1ZEdsbWFXVnlJam9pTWpRNU9URXdNREF3TURBeUluMTlMQ0p3WVhsbGNpSTZleUp1WVcxbElqb2lVM2x1ZEdobGRHbGpJRk5sYm1SbGNpSXNJbkJoY25SNVNXUkpibVp2SWpwN0ltWnpjRWxrSWpvaWJtOWxZbk1pTENKd1lYSjBlVWxrVkhsd1pTSTZJazFUU1ZORVRpSXNJbkJoY25SNVNXUmxiblJwWm1sbGNpSTZJakkwT1RreE1EQXdNREF3TVNKOWZTd2ljWFZ2ZEdWSlpDSTZJakF3TURBd01EQXdMVEF3TURBdE5EQXdNQzA0TURBd0xUQXdNREF3TURBd01EQXdNaUlzSW5SeVlXNXpZV04wYVc5dVNXUWlPaUl3TURBd01EQXdNQzB3TURBd0xUUXdNREF0T0RBd01DMHdNREF3TURBd01EQXdNREVpTENKMGNtRnVjMkZqZEdsdmJsUjVjR1VpT25zaWFXNXBkR2xoZEc5eUlqb2lVRUZaUlZJaUxDSnBibWwwYVdGMGIzSlVlWEJsSWpvaVEwOU9VMVZOUlZJaUxDSnpZMlZ1WVhKcGJ5STZJbFJTUVU1VFJrVlNJbjE5AA"
const reviewFulfilment = "-SXr7lyyRBVPOqJZRuRn5QBJ6cfH5Fip0hQbYo6EnEY"
const reviewCondition = "RwsAsZur1a1iX4FV6lM6miU9C5jRMPxobCevb9sPWRU"
const reviewNativeQuote = `{"quoteId":"00000000-0000-4000-8000-000000000002","transactionId":"00000000-0000-4000-8000-000000000001","payer":{"partyIdInfo":{"fspId":"noebs","partyIdType":"MSISDN","partyIdentifier":"249910000001"},"name":"Synthetic Sender"},"payee":{"partyIdInfo":{"fspId":"bankone","partyIdType":"MSISDN","partyIdentifier":"249910000002"},"name":"Synthetic Recipient"},"amount":{"amount":"1.01","currency":"SDG"},"amountType":"SEND","transactionType":{"scenario":"TRANSFER","initiator":"PAYER","initiatorType":"CONSUMER"},"expiration":"2030-01-01T00:05:00.000Z"}`

func reviewQuoteFixture(t *testing.T, copiedDisplayName bool) (*walletstore.InteropQuote, SDKState) {
	t.Helper()
	from := Party{IDType: "MSISDN", IDValue: "249910000001", FSPID: "noebs", DisplayName: "Synthetic Sender"}
	to := Party{IDType: "MSISDN", IDValue: "249910000002", FSPID: "bankone"}
	if copiedDisplayName {
		to.DisplayName = "Synthetic Recipient"
	}
	intent := QuoteIntent{From: from, To: Party{IDType: "MSISDN", IDValue: to.IDValue}, Amount: "1.01", Currency: "SDG", AmountType: "SEND", TransactionType: "TRANSFER"}
	response := QuoteResponse{TransferAmount: Money{Amount: "1.01", Currency: "SDG"}, PayeeReceiveAmount: &Money{Amount: "1.01", Currency: "SDG"}, Expiration: "2030-01-01T00:05:00.000Z", ILPPacket: reviewILPPacket, Condition: reviewCondition}
	id := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	wire := map[string]any{
		"homeTransactionId": id.String(), "transferId": id.String(), "quoteId": "00000000-0000-4000-8000-000000000002", "currentState": "WAITING_FOR_QUOTE_ACCEPTANCE",
		"from": from, "to": to,
		"quoteRequest":  Envelope[json.RawMessage]{Headers: map[string]string{"fspiop-source": "noebs", "fspiop-destination": "bankone"}, Body: json.RawMessage(reviewNativeQuote)},
		"quoteResponse": Envelope[QuoteResponse]{Headers: map[string]string{"fspiop-source": "bankone", "fspiop-destination": "noebs"}, Body: response},
		"getPartiesResponse": map[string]any{
			"headers": map[string]string{"fspiop-source": "bankone", "fspiop-destination": "noebs"},
			"body":    map[string]any{"party": map[string]any{"partyIdInfo": map[string]string{"fspId": "bankone", "partyIdType": "MSISDN", "partyIdentifier": to.IDValue}, "name": "Synthetic Recipient"}},
		},
	}
	raw := reviewJSON(t, wire)
	var state SDKState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return &walletstore.InteropQuote{ID: uuid.MustParse("00000000-0000-4000-8000-000000000003"), TransferID: id, Direction: "OUT", Amount: 101, Currency: "SDG", CurrencyUnitID: 1, Request: reviewJSON(t, intent), SDKState: raw, ExpiresAt: sql.NullTime{Time: time.Date(2030, 1, 1, 0, 5, 0, 0, time.UTC), Valid: true}}, state
}

func reviewJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestProtocolReviewOfficialSDKQuoteMapping(t *testing.T) {
	q, state := reviewQuoteFixture(t, false)
	// Upstream _resolvePayee preserves the verified name in the native party
	// response. It does not copy party.name into state.to.displayName.
	if err := ValidateQuote(q, state, "noebs", time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("valid pinned-SDK party/quote response was rejected: %v", err)
	}
}

func TestProtocolReviewQuoteIdentityBinding(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	q, state := reviewQuoteFixture(t, true)
	if err := ValidateQuote(q, state, "noebs", now); err != nil {
		t.Fatalf("control quote: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*SDKState)
	}{
		{"different payer alias", func(s *SDKState) { s.From.IDValue = "249910000099" }},
		{"different payer participant", func(s *SDKState) { s.From.FSPID = "walletone" }},
		{"different DFSP correlation identifier", func(s *SDKState) { s.HomeTransactionID = uuid.NewString() }},
		{"different native quote identifier", func(s *SDKState) { s.QuoteID = uuid.NewString() }},
		{"native quote from another transaction", func(s *SDKState) {
			var fields map[string]any
			if err := json.Unmarshal(s.QuoteRequest.Body, &fields); err != nil {
				t.Fatal(err)
			}
			fields["transactionId"] = uuid.NewString()
			s.QuoteRequest.Body = reviewJSON(t, fields)
		}},
		{"quote callback for another participant", func(s *SDKState) { s.QuoteResponse.Headers["fspiop-destination"] = "walletone" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, state := reviewQuoteFixture(t, true)
			tc.mutate(&state)
			if err := ValidateQuote(q, state, "noebs", now); !errors.Is(err, ErrProtocol) {
				t.Fatalf("uncorrelated quote accepted: %v", err)
			}
		})
	}
}

func TestProtocolReviewIncomingFinalityRequiresHubProvenance(t *testing.T) {
	q, _ := reviewQuoteFixture(t, true)
	prepare, err := PrepareFromQuote(q, "noebs")
	if err != nil {
		t.Fatal(err)
	}
	// Treat the same wire fixture from its receiving participant's perspective.
	q.Direction = "IN"
	original := IncomingPrepare{TransferID: q.TransferID.String()}
	original.Protocol.Source = prepare.PayerFSP
	original.Protocol.Prepare = prepare
	transfer := &walletstore.InteropTransfer{ID: q.TransferID, OriginalPrepare: reviewJSON(t, original), OriginalResponse: []byte(`{"transferState":"RESERVED"}`)}
	for _, terminal := range []string{"COMMITTED", "ABORTED"} {
		for _, source := range []string{"", "noebs", "bankone", "walletone", "switch", "hub", "Hub", "HUB", "Switch"} {
			t.Run(terminal+"/source="+source, func(t *testing.T) {
				// The pinned SDK patch retains authenticated ingress source after
				// the original SDK handler would otherwise discard its headers.
				wire := map[string]any{"transferId": q.TransferID.String(), "prepare": Envelope[Prepare]{Body: prepare}, "finalNotification": Fulfil{TransferState: terminal, CompletedTimestamp: "2030-01-01T00:01:00.000Z"}, "finalNotificationSource": source}
				var state SDKState
				if err := json.Unmarshal(reviewJSON(t, wire), &state); err != nil {
					t.Fatal(err)
				}
				outcome, err := ValidateOutcome(q, transfer, state, "sdk-loopback", "bankone")
				if source == "switch" || source == "hub" || source == "Hub" {
					if err != nil || outcome != terminal {
						t.Fatalf("correlated hub notification returned %q: %v", outcome, err)
					}
				} else if !errors.Is(err, ErrProtocol) || outcome != "" {
					t.Fatalf("terminal notification without hub provenance authorized %q: %v", outcome, err)
				}
			})
		}
	}
}

func TestProtocolReviewOutcomeChecks(t *testing.T) {
	q, _ := reviewQuoteFixture(t, true)
	prepare, err := PrepareFromQuote(q, "noebs")
	if err != nil {
		t.Fatal(err)
	}
	transfer := &walletstore.InteropTransfer{ID: q.TransferID, SubmittedAt: sql.NullTime{Time: time.Now(), Valid: true}}
	for _, tc := range []struct {
		name    string
		mutate  func(*SDKState)
		outcome string
		invalid bool
	}{
		{"native commit with valid fulfilment", func(*SDKState) {}, "COMMITTED", false},
		{"reserved is not final", func(s *SDKState) { s.Fulfil.Body.TransferState = "RESERVED" }, "", false},
		{"convenience completed is not final", func(s *SDKState) { s.CurrentState = "COMPLETED"; s.Fulfil.Body.TransferState = "RESERVED" }, "", false},
		{"wrong fulfilment", func(s *SDKState) { s.Fulfil.Body.Fulfilment = reviewCondition }, "", true},
		{"wrong transfer identifier", func(s *SDKState) { s.TransferID = uuid.NewString() }, "", true},
		{"wrong callback destination", func(s *SDKState) { s.Fulfil.Headers["fspiop-destination"] = "walletone" }, "", true},
		{"wrong prepare amount", func(s *SDKState) { s.Prepare.Body.Amount.Amount = "1.02" }, "", true},
		{"wrong prepare expiry", func(s *SDKState) { s.Prepare.Body.Expiration = "2030-01-01T00:06:00.000Z" }, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := SDKState{TransferID: q.TransferID.String(), Prepare: Envelope[Prepare]{Body: prepare}, Fulfil: Envelope[Fulfil]{Headers: map[string]string{"fspiop-source": "switch", "fspiop-destination": "noebs"}, Body: Fulfil{TransferState: "COMMITTED", Fulfilment: reviewFulfilment}}}
			tc.mutate(&state)
			outcome, err := ValidateOutcome(q, transfer, state, "sdk-hub-query", "noebs")
			if tc.invalid {
				if !errors.Is(err, ErrProtocol) || outcome != "" {
					t.Fatalf("invalid outcome returned %q: %v", outcome, err)
				}
			} else if err != nil || outcome != tc.outcome {
				t.Fatalf("outcome=%q error=%v; want %q", outcome, err, tc.outcome)
			}
		})
	}
}

func TestProtocolReviewStepUpCanonicalBinding(t *testing.T) {
	operation := transactionauth.OperationWalletInterop
	body := []byte(`{"quoteId":"00000000-0000-4000-8000-000000000003","idempotencyKey":"synthetic-transfer-1"}`)
	original, err := walletrequest.ParsePublic(operation, "synthetic", body, walletrequest.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	forwarded, err := walletrequest.ParseCanonical(operation, "synthetic", original.Body)
	if err != nil || forwarded.Digest != original.Digest || forwarded.IdempotencyKey != original.IdempotencyKey {
		t.Fatalf("gateway to wallet canonical binding changed: %v", err)
	}
	for _, tc := range []struct{ tenant, body string }{
		{"other-synthetic", string(body)},
		{"synthetic", `{"quoteId":"00000000-0000-4000-8000-000000000004","idempotencyKey":"synthetic-transfer-1"}`},
		{"synthetic", `{"quoteId":"00000000-0000-4000-8000-000000000003","idempotencyKey":"synthetic-transfer-2"}`},
	} {
		changed, err := walletrequest.ParsePublic(operation, tc.tenant, []byte(tc.body), walletrequest.Defaults{})
		if err != nil || changed.Digest == original.Digest {
			t.Fatalf("tenant, quote or idempotency change did not change authorization binding: %v", err)
		}
	}
	for _, forbidden := range []string{"tenantId", "ownerId", "amountMinor", "recipient"} {
		var fields map[string]any
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Fatal(err)
		}
		fields[forbidden] = "unexpected-client-authority"
		if _, err := walletrequest.ParsePublic(operation, "synthetic", reviewJSON(t, fields), walletrequest.Defaults{}); err == nil {
			t.Errorf("client-supplied %s accepted in transfer authorization", forbidden)
		}
	}
}

func TestProtocolReviewBackendRejectsUnconfiguredPeers(t *testing.T) {
	for _, peer := range []string{"127.0.0.1:40000", "[::1]:40000", "172.30.250.1:40000", "10.42.1.9:40000", "192.0.2.1:40000", "[2001:db8::1]:40000", "malformed"} {
		req := httptest.NewRequest(http.MethodGet, "/parties/MSISDN/249910000001", nil)
		req.RemoteAddr = peer
		response := httptest.NewRecorder()
		(&Worker{}).Handler().ServeHTTP(response, req)
		if response.Code != http.StatusForbidden {
			t.Errorf("peer %s: got %d, want 403", peer, response.Code)
		}
	}
}
