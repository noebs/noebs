package utils

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
)

func TestMaskPANHandlesShortValues(t *testing.T) {
	for _, pan := range []string{"", "1234", "123456789"} {
		if got := MaskPAN(pan); got != pan {
			t.Fatalf("MaskPAN(%q) = %q, want original value", pan, got)
		}
	}
}

func TestMaskPANMasksLongValues(t *testing.T) {
	if got := MaskPAN("1234567891234567"); got != "123456*****4567" {
		t.Fatalf("MaskPAN() = %q", got)
	}
}

func TestSendSMSClosesResponseBody(t *testing.T) {
	var sawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if r.URL.Query().Get("to") != "249912141660" {
			t.Fatalf("sms recipient = %q", r.URL.Query().Get("to"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(server.Close)

	err := SendSMS(&ebs_fields.NoebsConfig{
		SMSGateway: server.URL + "?",
		SMSAPIKey:  "test-key",
		SMSSender:  "NOEBS",
		SMSMessage: "footer",
	}, SMS{
		Mobile:  "0912141660",
		Message: "body",
	})
	if err != nil {
		t.Fatalf("SendSMS() error = %v", err)
	}
	if !sawRequest {
		t.Fatal("SMS server was not called")
	}
}

func TestSendSMSReturnsGatewayError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream failure"))
	}))
	t.Cleanup(server.Close)

	err := SendSMS(&ebs_fields.NoebsConfig{
		SMSGateway: server.URL + "?",
		SMSAPIKey:  "test-key",
		SMSSender:  "NOEBS",
		SMSMessage: "footer",
	}, SMS{
		Mobile:  "0912141660",
		Message: "body",
	})
	if !errors.Is(err, ErrSMSDeliveryFailed) {
		t.Fatalf("SendSMS() error = %v, want %v", err, ErrSMSDeliveryFailed)
	}
}
