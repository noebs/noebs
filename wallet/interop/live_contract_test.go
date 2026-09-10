package interop

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"os"
	"testing"
	"time"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

func TestSDKCanonicalAmountsPreserveMinorUnits(t *testing.T) {
	for amount, expected := range map[int64]string{0: "0", 1: "0.01", 10: "0.1", 100: "1", 120: "1.2", 123: "1.23", 1000: "10", math.MaxInt64: "92233720368547758.07"} {
		wire := ProtocolDecimal(amount)
		if wire != expected {
			t.Fatalf("%d: got %q, want %q", amount, wire, expected)
		}
		if parsed, err := ParseDecimal(wire); err != nil || parsed != amount {
			t.Fatalf("amount changed: %d -> %q -> %d, %v", amount, wire, parsed, err)
		}
	}
}

func TestIncomingQuoteExpiryPolicyAndReplay(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 100123456, time.UTC)
	expiry, err := incomingQuoteExpiry("", "", nil, now)
	if err != nil || expiry.Format(protocolTimeFormat) != "2030-01-01T00:02:00.100Z" {
		t.Fatalf("optional expiry: %s, %v", expiry, err)
	}
	existing := &walletstore.InteropQuote{ExpiresAt: sql.NullTime{Time: expiry, Valid: true}}
	replayed, err := incomingQuoteExpiry("", "", existing, now.Add(time.Hour))
	if err != nil || !replayed.Equal(expiry) {
		t.Fatalf("retry extended original expired agreement: %s, %v", replayed, err)
	}
	for _, tc := range []struct{ request, native string }{
		{"", "2030-01-01T00:02:00.100Z"}, {"2030-01-01T00:02:00.100Z", ""}, {"invalid", "invalid"},
	} {
		if _, err := incomingQuoteExpiry(tc.request, tc.native, nil, now); !errors.Is(err, ErrProtocol) {
			t.Fatalf("invalid or altered native expiry accepted: %+v, %v", tc, err)
		}
	}
	if _, err := incomingQuoteExpiry("2030-01-01T00:00:01.000Z", "2030-01-01T00:00:01.000Z", nil, now); !errors.Is(err, walletstore.ErrInteropQuoteExpired) {
		t.Fatalf("new expired quote accepted: %v", err)
	}
	for nanos, expected := range map[int]string{0: "2030-01-01T00:00:00.000Z", 100000000: "2030-01-01T00:00:00.100Z", 123456789: "2030-01-01T00:00:00.123Z"} {
		if value := time.Date(2030, 1, 1, 0, 0, 0, nanos, time.UTC).Format(protocolTimeFormat); value != expected {
			t.Fatalf("SDK timestamp precision: %s", value)
		}
	}
}

// Captured from the actual pinned SDK and native bankone quote, using only
// synthetic identities. In particular, Axios serialized Content-Length as a
// JSON number and the SDK required homeTransactionId on its HTTP request.
func TestLivePinnedSDKQuoteEnvelope(t *testing.T) {
	raw, err := os.ReadFile("testdata/sdk-v24.19.5-live-quote.json")
	if err != nil {
		t.Fatal(err)
	}
	var state SDKState
	if err = json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	intent := QuoteIntent{From: state.From, To: Party{IDType: "MSISDN", IDValue: "249900000001"}, Amount: "1.23", Currency: "SDG", AmountType: "SEND", TransactionType: "TRANSFER"}
	request, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	quote := &walletstore.InteropQuote{TransferID: uuid.MustParse(state.TransferID), Amount: 123, Currency: "SDG", Request: request, SDKState: raw}
	expiry, err := time.Parse(time.RFC3339Nano, state.QuoteResponse.Body.Expiration)
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateQuote(quote, state, "noebs", expiry.Add(-30*time.Second)); err != nil {
		t.Fatal(err)
	}
	state.HomeTransactionID = ""
	if err = ValidateQuote(quote, state, "noebs", expiry.Add(-30*time.Second)); err == nil {
		t.Fatal("missing DFSP correlation ID accepted")
	}
}
