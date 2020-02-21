package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestEnv(t *testing.T) {
	// Test that we can read environmental variables correctly.
	key := "MYKEY"
	val := "MYVA"
	if err := os.Setenv(key, val); err != nil {
		t.Errorf("An error occured: %s", err.Error())
	}

	if got := os.Getenv(key); got != val {
		t.Errorf("environmental variable is incorrect. Wanted %s, Got: %s", key, got)
	}
}

func TestWorkingKey(t *testing.T) {
	var workingKeyFields ebs_fields.WorkingKeyFields
	workingKeyFields.ClientID = "noebs"
	workingKeyFields.TerminalID = "12345678"
	workingKeyFields.TranDateTime = time.Now().UTC().String()
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
		now := time.Now()
		iso8601 := now.Format(time.RFC3339)
		fields.TranDateTime = iso8601
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
			t.Errorf("got '%d', want '%d'", got, want)
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
			t.Errorf("got '%d', want '%d'", got, want)
		}
	})
}

func TestEBSHttpClient(t *testing.T) {
	// always return wrong
	// i need to mock up EBS server (which is really challenging!

	//t.Fatalf("Something went wrong")
	t.Run("Testing wrong content-types", func(t *testing.T) {
		url := "https://example.com"
		payload := getSuccessfulPurchasePayload(ebs_fields.PurchaseFields{})
		fmt.Println(string(payload))
		_, _, err := ebs_fields.EBSHttpClient(url, payload)

		if err != ebs_fields.ContentTypeErr {
			t.Fatalf("Returned error is not of the correct type, %v. Wanted %v", err, ebs_fields.ContentTypeErr)
		}
	})

	t.Run("Check the return error type is EBSFailedTransactionErr", func(t *testing.T) {
		url := "https://212.0.129.118/terminal_api/transactions/purchase/"
		payload := getFailedPurchasePayload(t, ebs_fields.PurchaseFields{})

		fmt.Println(string(payload))
		_, _, err := ebs_fields.EBSHttpClient(url, payload)

		fmt.Print(reflect.TypeOf(err))

		if err != ebs_fields.EbsFailedTransaction {

			t.Fatalf("Returned error is not of the correct type. Got: (%s). Wanted: (%s)", reflect.TypeOf(err), ebs_fields.ContentTypeErr)
		}
	})

}

func TestCardTransfer(t *testing.T) {
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
		w := httptest.NewRecorder()

		route.ServeHTTP(w, req)

		got := w.Code
		want := 500

		if got != want {
			t.Errorf("got '%d', want '%d'", got, want)
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
			t.Errorf("got '%d', want '%d'", got, want)
		}
	})

}

func TestPurchase1(t *testing.T) {
	type args struct {
		c *gin.Context
	}
	tests := []struct {
		name string
		args args
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
		})
	}
}
