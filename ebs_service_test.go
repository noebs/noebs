package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/assert"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWorkingKey(t *testing.T) {
	var workingKeyFields WorkingKeyFields
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

	t.Fatalf("Something went wrong")
}

func TestCardTransfer(t *testing.T) {
	t.Fatalf("Something went wrong")
}


func populatePurchaseFields(missing bool) PurchaseFields{
	// this should be a generic function for all fields
	// it should also respects each struct types
	// lets test populating purchase fields
	var fields PurchaseFields
	fields.TerminalID = "09123456"
	fields.TranDateTime = time.Now().UTC()
	fields.SystemTraceAuditNumber = rand.Int()
	fields.ClientID = "noebs"
	fields.Expdate = "2203"
	fields.Pan = "1234567891234567"
	fields.Pin = "1234"
	fields.TranAmount = 232
	fields.TranCurrencyCode = "SDG"
	if missing{
		fields.TerminalID = ""
		return fields
	}
	return fields
}