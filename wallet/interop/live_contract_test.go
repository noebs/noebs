package interop

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

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
