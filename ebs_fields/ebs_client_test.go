package ebs_fields

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConfigureEBSHTTPClient_SecureByDefault(t *testing.T) {
	old := ebsTransport.TLSClientConfig
	t.Cleanup(func() { ebsTransport.TLSClientConfig = old })

	ConfigureEBSHTTPClient(NoebsConfig{})
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

func TestConfigureEBSHTTPClient_AllowsInsecureWhenExplicit(t *testing.T) {
	old := ebsTransport.TLSClientConfig
	t.Cleanup(func() { ebsTransport.TLSClientConfig = old })

	ConfigureEBSHTTPClient(NoebsConfig{EBSInsecureSkipVerify: true})
	if ebsTransport.TLSClientConfig == nil {
		t.Fatalf("expected TLSClientConfig to be set")
	}
	if !ebsTransport.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("expected InsecureSkipVerify=true when explicitly configured")
	}
}

func TestEBSHTTPClientIPINFallbackParsesNumericTranDateTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"UUID":"uuid-1","tranDateTime":20260531120000,"responseCode":0,"responseMessage":"Approved","pubKeyValue":"public-key","pan":"9222081700000000","expDate":"2601"}`))
	}))
	t.Cleanup(server.Close)

	code, res, err := EBSHttpClient(server.URL, []byte(`{}`))
	if err != nil {
		t.Fatalf("EBSHttpClient() error = %v", err)
	}
	if code != http.StatusOK {
		t.Fatalf("EBSHttpClient() status = %d, want %d", code, http.StatusOK)
	}
	if res.TranDateTime != "20260531120000" || res.UUID != "uuid-1" || res.PubKeyValue != "public-key" {
		t.Fatalf("fallback response = %+v", res)
	}
}

func TestEBSHTTPClientIPINFallbackRejectsMalformedTranDateTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"UUID":"uuid-1","tranDateTime":{},"responseCode":0,"responseMessage":"Approved"}`))
	}))
	t.Cleanup(server.Close)

	code, res, err := EBSHttpClient(server.URL, []byte(`{}`))
	if err == nil {
		t.Fatalf("EBSHttpClient() error = nil, status=%d response=%+v; want fallback decode error", code, res)
	}
	if code != http.StatusInternalServerError {
		t.Fatalf("EBSHttpClient() status = %d, want %d", code, http.StatusInternalServerError)
	}
}

func TestEBSHTTPClientIPINFallbackReturnsGatewayFailureMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"UUID":"uuid-1","tranDateTime":20260531120000,"responseCode":53,"responseMessage":"Invalid PIN"}`))
	}))
	t.Cleanup(server.Close)

	code, _, err := EBSHttpClient(server.URL, []byte(`{}`))
	if err == nil || err.Error() != "Invalid PIN" {
		t.Fatalf("EBSHttpClient() error = %v, want Invalid PIN", err)
	}
	if code != http.StatusBadGateway {
		t.Fatalf("EBSHttpClient() status = %d, want %d", code, http.StatusBadGateway)
	}
}
