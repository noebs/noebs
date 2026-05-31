package merchant

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestCallEBSRejectsReservedTenantBeforeHTTP(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	ctx := context.Background()

	_, err := service.callEBSJSON(ctx, "default", "/ebs", struct{}{})
	if !errors.Is(err, store.ErrInvalidTenantID) {
		t.Fatalf("callEBSJSON() error = %v, want %v", err, store.ErrInvalidTenantID)
	}

	_, err = service.callEBSRaw(ctx, "default", "/ebs", []byte("{}"))
	if !errors.Is(err, store.ErrInvalidTenantID) {
		t.Fatalf("callEBSRaw() error = %v, want %v", err, store.ErrInvalidTenantID)
	}
}

func TestCallEBSUsesConfiguredHTTPClient(t *testing.T) {
	var called bool
	service := &Service{
		Store: &store.Store{},
		NoebsConfig: ebs_fields.NoebsConfig{
			MerchantIP:            "https://merchant-ebs.example",
			KafkaTransactionTopic: "test-ebs-transactions",
		},
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				called = true
				if req.URL.String() != "https://merchant-ebs.example/purchase" {
					t.Fatalf("request URL = %q, want configured target", req.URL.String())
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"responseCode":0,"responseMessage":"Success","UUID":"merchant-ebs"}`)),
					Request:    req,
				}, nil
			}),
		},
	}

	_, err := service.callEBSJSON(context.Background(), "tenant-a", "/purchase", struct{}{})
	if err == nil || !strings.Contains(err.Error(), "nil db") {
		t.Fatalf("callEBSJSON() error = %v, want nil db after configured HTTP client call", err)
	}
	if !called {
		t.Fatalf("configured HTTP client was not used")
	}
}

func TestCallEBSRequiresConfiguredHTTPClient(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	_, err := service.callEBSJSON(context.Background(), "tenant-a", "/purchase", struct{}{})
	if !errors.Is(err, ErrMissingHTTPClient) {
		t.Fatalf("callEBSJSON() error = %v, want %v", err, ErrMissingHTTPClient)
	}
}

func TestRecordTransactionRejectsReservedTenantBeforeDB(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	err := service.recordTransaction(context.Background(), "default", ebs_fields.EBSResponse{})
	if !errors.Is(err, store.ErrInvalidTenantID) {
		t.Fatalf("recordTransaction() error = %v, want %v", err, store.ErrInvalidTenantID)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
