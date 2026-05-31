package consumer

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
	service := &Service{}
	ctx := context.Background()

	_, err := service.callEBSJSON(ctx, "default", "http://127.0.0.1:1", "/ebs", struct{}{})
	if !errors.Is(err, store.ErrInvalidTenantID) {
		t.Fatalf("callEBSJSON() error = %v, want %v", err, store.ErrInvalidTenantID)
	}

	_, err = service.callEBSRaw(ctx, "default", "http://127.0.0.1:1", "/ebs", []byte("{}"))
	if !errors.Is(err, store.ErrInvalidTenantID) {
		t.Fatalf("callEBSRaw() error = %v, want %v", err, store.ErrInvalidTenantID)
	}
}

func TestCallEBSUsesConfiguredHTTPClient(t *testing.T) {
	var called bool
	service := &Service{
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				called = true
				if req.URL.String() != "https://ebs.example/purchase" {
					t.Fatalf("request URL = %q, want configured target", req.URL.String())
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"responseCode":0,"responseMessage":"Success","UUID":"consumer-ebs"}`)),
					Request:    req,
				}, nil
			}),
		},
	}

	res, err := service.callEBSJSONWithoutTransactionRecord(context.Background(), "tenant-a", "https://ebs.example", "/purchase", struct{}{})
	if err != nil {
		t.Fatalf("callEBSJSONWithoutTransactionRecord() error = %v", err)
	}
	if res.UUID != "consumer-ebs" {
		t.Fatalf("response UUID = %q, want consumer-ebs", res.UUID)
	}
	if !called {
		t.Fatalf("configured HTTP client was not used")
	}
}

func TestCallEBSRequiresConfiguredHTTPClient(t *testing.T) {
	service := &Service{}
	_, err := service.callEBSJSONWithoutTransactionRecord(context.Background(), "tenant-a", "https://ebs.example", "/purchase", struct{}{})
	if !errors.Is(err, ErrMissingHTTPClient) {
		t.Fatalf("callEBSJSONWithoutTransactionRecord() error = %v, want %v", err, ErrMissingHTTPClient)
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
