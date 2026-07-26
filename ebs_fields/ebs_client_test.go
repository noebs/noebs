package ebs_fields

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEBSHTTPClientVerifiesTLS(t *testing.T) {
	if ebsTransport.TLSClientConfig == nil {
		t.Fatalf("expected TLSClientConfig to be set")
	}
	if ebsTransport.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("expected InsecureSkipVerify=false by default")
	}
	if ebsTransport.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.Fatalf("expected MinVersion >= TLS1.2, got %v", ebsTransport.TLSClientConfig.MinVersion)
	}
}

func TestEBSHTTPClientRejectsNumericTranDateTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"UUID":"uuid-1","tranDateTime":20260531120000,"responseCode":0,"responseMessage":"Approved","pubKeyValue":"public-key","pan":"9222081700000000","expDate":"2601"}`))
	}))
	t.Cleanup(server.Close)

	code, res, err := EBSHttpClient(context.Background(), server.URL, []byte(`{}`))
	if err == nil {
		t.Fatalf("EBSHttpClient() error = nil, status=%d response=%+v; want strict decode error", code, res)
	}
	if code != http.StatusInternalServerError {
		t.Fatalf("EBSHttpClient() status = %d, want %d", code, http.StatusInternalServerError)
	}
}

func TestEBSHTTPClientRejectsMalformedTranDateTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"UUID":"uuid-1","tranDateTime":{},"responseCode":0,"responseMessage":"Approved"}`))
	}))
	t.Cleanup(server.Close)

	code, res, err := EBSHttpClient(context.Background(), server.URL, []byte(`{}`))
	if err == nil {
		t.Fatalf("EBSHttpClient() error = nil, status=%d response=%+v; want strict decode error", code, res)
	}
	if code != http.StatusInternalServerError {
		t.Fatalf("EBSHttpClient() status = %d, want %d", code, http.StatusInternalServerError)
	}
}

func TestEBSHTTPClientRejectsNonzeroResponseCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"responseCode":53,"responseMessage":"Success text is not authoritative"}`))
	}))
	t.Cleanup(server.Close)

	code, _, err := EBSHttpClient(context.Background(), server.URL, []byte(`{}`))
	if err == nil || err.Error() != "Success text is not authoritative" {
		t.Fatalf("EBSHttpClient() error = %v, want provider rejection", err)
	}
	if code != http.StatusBadGateway {
		t.Fatalf("EBSHttpClient() status = %d, want %d", code, http.StatusBadGateway)
	}
}

func TestEBSHTTPClientWithClientUsesProvidedClient(t *testing.T) {
	var called bool
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "request-context")
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			called = true
			if req.Context().Value(contextKey{}) != "request-context" {
				t.Fatal("request did not preserve the caller context")
			}
			if req.URL.String() != "https://ebs.example/purchase" {
				t.Fatalf("request URL = %q, want configured target", req.URL.String())
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"responseCode":0,"responseMessage":"Success","UUID":"uuid-1"}`)),
				Request:    req,
			}, nil
		}),
	}

	code, res, err := EBSHttpClientWithClient(ctx, client, "https://ebs.example/purchase", []byte(`{"amount":100}`))
	if err != nil {
		t.Fatalf("EBSHttpClientWithClient() error = %v", err)
	}
	if code != http.StatusOK || res.UUID != "uuid-1" {
		t.Fatalf("EBSHttpClientWithClient() = status %d response %+v, want success uuid", code, res)
	}
	if !called {
		t.Fatalf("provided client was not used")
	}
}

func TestEBSHTTPClientPreservesCancellationCause(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			cancel()
			<-req.Context().Done()
			return nil, req.Context().Err()
		}),
	}

	code, _, err := EBSHttpClientWithClient(ctx, client, "https://ebs.example/purchase", []byte(`{}`))
	if code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", code, http.StatusGatewayTimeout)
	}
	if !errors.Is(err, EbsGatewayConnectivityErr) {
		t.Fatalf("error = %v, want EBS connectivity category", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation cause", err)
	}
}

func TestEBSHTTPClientPreservesResponseReadCause(t *testing.T) {
	readErr := errors.New("response read failed")
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       errorReadCloser{err: readErr},
				Request:    req,
			}, nil
		}),
	}

	code, _, err := EBSHttpClientWithClient(context.Background(), client, "https://ebs.example/purchase", []byte(`{}`))
	if code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", code, http.StatusInternalServerError)
	}
	if !errors.Is(err, EbsGatewayConnectivityErr) {
		t.Fatalf("error = %v, want EBS connectivity category", err)
	}
	if !errors.Is(err, readErr) {
		t.Fatalf("error = %v, want response read cause", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type errorReadCloser struct {
	err error
}

func (r errorReadCloser) Read([]byte) (int, error) {
	return 0, r.err
}

func (errorReadCloser) Close() error {
	return nil
}
