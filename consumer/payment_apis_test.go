package consumer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
)

func TestService_RegisterWithCard(t *testing.T) {

	card := ebs_fields.CacheCards{Pan: "23232323", Mobile: "0912141660", Password: "12345678"}

	payload, _ := json.Marshal(card)
	w := httptest.NewRecorder()
	route := testSetupRouter()

	fmt.Println(w.Body.String(), "Why is it.")

	// well, assuming that the server is running. Eh?
	// Mock data BTW...
	req := httptest.NewRequest("POST", "/register_with_card", bytes.NewBuffer(payload))

	route.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected: %d, got: %d", 200, w.Code)
	}
}

func TestService_CreateUser(t *testing.T) {

	card := ebs_fields.User{Mobile: "0912141660", Password: "me@Suckit1"}

	payload, _ := json.Marshal(card)
	w := httptest.NewRecorder()
	route := testSetupRouter()

	// well, assuming that the server is running. Eh?
	// Mock data BTW...
	req := httptest.NewRequest("POST", "/register", bytes.NewBuffer(payload))

	route.ServeHTTP(w, req)

	if w.Code != 201 {
		t.Errorf("expected: %d, got: %d", 201, w.Code)
	}
}

func TestService_LoginHandler(t *testing.T) {

	card := ebs_fields.User{Mobile: "0912141660", Password: "me@Suckit1"}

	payload, _ := json.Marshal(card)
	w := httptest.NewRecorder()
	route := testSetupRouter()

	// well, assuming that the server is running. Eh?
	// Mock data BTW...
	req := httptest.NewRequest("POST", "/login", bytes.NewBuffer(payload))

	route.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected: %d, got: %d", 200, w.Code)
	}
}
