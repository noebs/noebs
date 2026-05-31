package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
)

func TestConfigLoad(t *testing.T) {
	ensureInit()
	if noebsConfig.DatabaseDriver == "" {
		t.Fatalf("expected database driver to be configured for tests")
	}
}

func requireIntegration(t *testing.T) {
	t.Helper()
	ensureInit()
	if !noebsConfig.IntegrationTests {
		t.Skip("integration_tests disabled in config")
	}
}

func TestWorkingKey(t *testing.T) {
	workingKeyFields := populateWorkingKeyFields()
	payload, err := json.Marshal(workingKeyFields)
	if err != nil {
		t.Fatalf("marshal working key payload: %v", err)
	}
	route := GetMainEngine()

	// well, assuming that the server is running. Eh?
	// Mock data BTW...
	req := httptest.NewRequest(http.MethodGet, "/test", bytes.NewBuffer(payload))

	resp, err := route.Test(req, routeTestTimeout)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Errorf("expected: %d, got: %d", 200, resp.StatusCode)
	}
	// I'm really not sure why this would ever work.
	// suddenly, things starting to make sense.
}

func TestPurchase(t *testing.T) {
	requireIntegration(t)
	// always returns t.Fatal...
	// test a missing field always returns 400.

	route := GetMainEngine()

	t.Run("Test all fields passed", func(t *testing.T) {
		fields := populatePurchaseFields(false)
		now := time.Now()
		iso8601 := now.Format(time.RFC3339)
		fields.TranDateTime = iso8601
		buff, err := json.Marshal(&fields)
		if err != nil {
			t.Fatalf("Error in marshalling %v", err)
		}
		req, _ := http.NewRequest(http.MethodPost, "/purchase", bytes.NewBuffer(buff))
		resp, err := route.Test(req, routeTestTimeout)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}

		got := resp.StatusCode
		want := 500

		if got != want {
			t.Errorf("got '%d', want '%d'", got, want)
		}
	})

	t.Run("Test missing field(s)", func(t *testing.T) {
		fields := populatePurchaseFields(true)
		buff, err := json.Marshal(&fields)
		if err != nil {
			t.Fatalf("Error in marshalling %v", err)
		}
		req, _ := http.NewRequest(http.MethodPost, "/purchase", bytes.NewBuffer(buff))
		resp, err := route.Test(req, routeTestTimeout)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}

		got := resp.StatusCode
		want := 400

		if got != want {
			t.Errorf("got '%d', want '%d'", got, want)
		}
	})
}

func TestEBSHttpClient(t *testing.T) {
	t.Run("Testing wrong content-types", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"responseCode":0,"responseMessage":"Success"}`))
		}))
		t.Cleanup(server.Close)

		payload := getSuccessfulPurchasePayload(t)
		_, _, err := ebs_fields.EBSHttpClient(server.URL, payload)

		if err != ebs_fields.ContentTypeErr {
			t.Fatalf("Returned error is not of the correct type, %v. Wanted %v", err, ebs_fields.ContentTypeErr)
		}
	})

	t.Run("Returns error on failed transaction", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"responseCode":72,"responseMessage":"Failed"}`))
		}))
		t.Cleanup(server.Close)

		payload := getFailedPurchasePayload(t)
		status, res, err := ebs_fields.EBSHttpClient(server.URL, payload)

		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if err.Error() != "Failed" {
			t.Fatalf("unexpected error message: %v", err)
		}
		if status != http.StatusBadGateway {
			t.Fatalf("unexpected status: %d", status)
		}
		if res.ResponseCode != 72 {
			t.Fatalf("unexpected response code: %d", res.ResponseCode)
		}
	})

}

func TestCardTransfer(t *testing.T) {
	requireIntegration(t)
	route := GetMainEngine()
	t.Run("Test all CardTransfer passed", func(t *testing.T) {
		fields := populateCardTransferFields()
		now := time.Now()
		iso8601 := now.Format(time.RFC3339)
		fields.TranDateTime = iso8601

		buff, err := json.Marshal(&fields)
		if err != nil {
			t.Fatalf("Error in marshalling %v", err)
		}
		req, _ := http.NewRequest(http.MethodPost, "/cardTransfer", bytes.NewBuffer(buff))
		resp, err := route.Test(req, routeTestTimeout)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}

		got := resp.StatusCode
		want := 500

		if got != want {
			t.Errorf("got '%d', want '%d'", got, want)
		}
	})

	t.Run("Test missing field(s)", func(t *testing.T) {
		fields := populateCardTransferFields()
		buff, err := json.Marshal(&fields)
		if err != nil {
			t.Fatalf("Error in marshalling %v", err)
		}
		req, _ := http.NewRequest(http.MethodPost, "/cardTransfer", bytes.NewBuffer(buff))
		resp, err := route.Test(req, routeTestTimeout)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}

		got := resp.StatusCode
		want := 400

		if got != want {
			t.Errorf("got '%d', want '%d'", got, want)
		}
	})

}

// TODO: Add Purchase handler tests for fiber if needed.
