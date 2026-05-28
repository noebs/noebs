package httpclient

import (
	"net/http"
	"testing"
)

func TestNewDoesNotUseEnvironmentProxy(t *testing.T) {
	client := New()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatalf("transport.Proxy must be nil; outbound service clients must not read proxy environment variables")
	}
}
