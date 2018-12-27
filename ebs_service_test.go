package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/assert"
	"math/rand"
	"morsal/noebs/validations"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWorkingKey(t *testing.T) {
	var workingKeyFields validations.WorkingKeyFields
	workingKeyFields.ClientID = "noebs"
	workingKeyFields.TerminalID = "12345678"
	workingKeyFields.TranDateTime = time.Now().UTC()
	workingKeyFields.SystemTraceAuditNumber = rand.Intn(99999)

	payload, err := json.Marshal(workingKeyFields)
	if err != nil {
		t.Fatal()
	}
	w := httptest.NewRecorder()
	route := GetMainEngine()

	fmt.Println(w.Body.String(), "Why is it.")

	// well, assuming that the server is running. Eh?
	// Mock data BTW...
	req := httptest.NewRequest("POST", "/test", bytes.NewBuffer(payload))

	route.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	// I'm really not sure why this would ever work.
	// suddenly, things starting to make sense.
}

func TestPurchase(t *testing.T) {
	// always returns t.Fatal...
	// test a missing field always returns 400.

	route := GetMainEngine()

	t.Run("Test all fields passed", func(t *testing.T) {
		fields := populatePurchaseFields(false)
		buff, err := json.Marshal(&fields)
		if err != nil {
			t.Fatalf("Error in marshalling %v", err)
		}
		req, _ := http.NewRequest(http.MethodPost, "/purchase", bytes.NewBuffer(buff))
		w := httptest.NewRecorder()

		route.ServeHTTP(w, req)

		got := w.Code
		want := 500

		if got != want {
			t.Errorf("got '%s', want '%s'", got, want)
			t.Errorf(w.Body.String())
		}
	})

	t.Run("Test missing field(s)", func(t *testing.T) {
		fields := populatePurchaseFields(true)
		buff, err := json.Marshal(&fields)
		if err != nil {
			t.Fatalf("Error in marshalling %v", err)
		}
		req, _ := http.NewRequest(http.MethodPost, "/purchase", bytes.NewBuffer(buff))
		w := httptest.NewRecorder()

		route.ServeHTTP(w, req)

		got := w.Code
		want := 400

		if got != want {
			t.Errorf("got '%s', want '%s'", got, want)
		}
	})
}

func TestEBSHttpClient2(t *testing.T) {
	// always return wrong
	// i need to mock up EBS server (which is really challenging!

	t.Fatalf("Something went wrong")
}

func TestCardTransfer(t *testing.T) {

	route := GetMainEngine()

	t.Run("Test all CardTransfer passed", func(t *testing.T) {
		fields := populateCardTransferFields()
		buff, err := json.Marshal(&fields)
		if err != nil {
			t.Fatalf("Error in marshalling %v", err)
		}
		req, _ := http.NewRequest(http.MethodPost, "/cardTransfer", bytes.NewBuffer(buff))
		w := httptest.NewRecorder()

		route.ServeHTTP(w, req)

		got := w.Code
		want := 500

		if got != want {
			t.Errorf("got '%s', want '%s'", got, want)
			t.Errorf(w.Body.String())
		}
	})

	t.Run("Test missing field(s)", func(t *testing.T) {
		fields := populateCardTransferFields()
		buff, err := json.Marshal(&fields)
		if err != nil {
			t.Fatalf("Error in marshalling %v", err)
		}
		req, _ := http.NewRequest(http.MethodPost, "/cardTransfer", bytes.NewBuffer(buff))
		w := httptest.NewRecorder()

		route.ServeHTTP(w, req)

		got := w.Code
		want := 400

		if got != want {
			t.Errorf("got '%s', want '%s'", got, want)
		}
	})

	t.Fatalf("Something went wrong")
}
