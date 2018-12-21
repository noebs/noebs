package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/assert"
	"math/rand"
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

	t.Fatalf("Something went wrong")
}

func TestEBSHttpClient2(t *testing.T) {
	// always return wrong

	t.Fatalf("Something went wrong")
}

func TestCardTransfer(t *testing.T) {
	t.Fatalf("Something went wrong")
}
