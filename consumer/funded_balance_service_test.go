package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestBalanceInquiryRequestClaimMatchesSDKVector(t *testing.T) {
	const cardID = "123e4567-e89b-42d3-a456-426614174000"
	const expected = "v1:156f27e07145ee6a12bfbd6ce2111f1c8ba5ba8fe0d9f728a1e07de867dcc07a"
	claim, err := BalanceInquiryRequestClaim(cardID)
	if err != nil {
		t.Fatalf("balance claim: %v", err)
	}
	if claim != expected {
		t.Fatalf("balance claim = %q, want SDK vector %q", claim, expected)
	}
	if _, err := BalanceInquiryRequestClaim(cardID + " "); !errors.Is(err, store.ErrInvalidCardID) {
		t.Fatalf("non-canonical card ID error = %v, want %v", err, store.ErrInvalidCardID)
	}
}

func TestBalanceAmountsExposeOnlyKnownFiniteRailFields(t *testing.T) {
	result, err := balanceAmounts(map[string]any{
		"available": 1250.75,
		"leger":     1200.25,
		"PAN":       "4242420000004242",
		"IPIN":      "encrypted-pin-block",
		"expDate":   "2912",
		"arbitrary": map[string]any{"nested": "rail value"},
	})
	if err != nil {
		t.Fatalf("balance amounts: %v", err)
	}
	if result != (BalanceAmounts{Available: 1250.75, Ledger: 1200.25}) {
		t.Fatalf("balance amounts = %+v", result)
	}
	body, err := json.Marshal(OpaqueBalanceResult{UUID: "operation", Balance: result})
	if err != nil {
		t.Fatalf("marshal public result: %v", err)
	}
	if string(body) != `{"uuid":"operation","balance":{"available":1250.75,"ledger":1200.25}}` {
		t.Fatalf("public balance JSON = %s", body)
	}

	for name, balance := range map[string]map[string]any{
		"missing available": {"leger": 1.0},
		"missing ledger":    {"available": 1.0},
		"string available":  {"available": "4242420000004242", "leger": 1.0},
		"string ledger":     {"available": 1.0, "leger": "2912"},
		"infinite":          {"available": math.Inf(1), "leger": 1.0},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := balanceAmounts(balance); !errors.Is(err, ErrUnsafeBalanceResponse) {
				t.Fatalf("balance error = %v, want %v", err, ErrUnsafeBalanceResponse)
			}
		})
	}
}

func TestOpaqueBalanceCapabilityDefaultsClosedBeforeOutboundCalls(t *testing.T) {
	transport := &countingBalanceTransport{}
	service := &Service{
		Store:      &store.Store{},
		HTTPClient: &http.Client{Transport: transport},
		NoebsConfig: ebs_fields.NoebsConfig{
			ConsumerIP:       "https://ebs.invalid/",
			EBSConsumerKey:   "unused",
			ServiceDiscovery: map[string]string{"card-vault": "https://vault.invalid"},
		},
	}
	_, err := service.OpaqueBalance(context.Background(), "tenant", 101, OpaqueBalanceRequest{}, time.Now())
	if !errors.Is(err, ErrFundedOperationsUnavailable) {
		t.Fatalf("opaque balance error = %v, want %v", err, ErrFundedOperationsUnavailable)
	}
	if calls := transport.calls.Load(); calls != 0 {
		t.Fatalf("disabled operation made %d outbound calls", calls)
	}
}

type countingBalanceTransport struct {
	calls atomic.Int64
}

func (t *countingBalanceTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls.Add(1)
	return nil, errors.New("unexpected outbound call")
}
