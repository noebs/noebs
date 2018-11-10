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
	workingKeyFields := WorkingKeyFields{
		SystemTraceAuditNumber: rand.Intn(99999), // maybe better define it as const?
		TranDateTime:           time.Now().UTC(),
		TerminalID:             "15000trtrtrtrtrtr005", // wrong terminal ID. Just to reason about this whole thing.
		ClientID:               "gndi",
	}
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
