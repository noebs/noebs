package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
)

func TestService_RegisterWithCard(t *testing.T) {
	env := newTestEnv(t)

	ctx := context.Background()
	user := seedUser(t, env.Store, env.Tenant, "0900000000", "Seed@Pass1")
	seedCard := ebs_fields.Card{Pan: "23232323", Expiry: "2901"}
	if err := env.Store.AddCards(ctx, env.Tenant, user.ID, []ebs_fields.Card{seedCard}); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	card := ebs_fields.CacheCards{
		Pan:       "23232323",
		Expiry:    "2901",
		Mobile:    "0912141660",
		Password:  "me@Suckit1",
		PublicKey: "pubkey",
		Name:      "Test User",
	}

	payload, _ := json.Marshal(card)
	req := httptest.NewRequest("POST", "/register_with_card", bytes.NewBuffer(payload))
	resp, err := env.Router.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Errorf("expected: %d, got: %d", 200, resp.StatusCode)
	}
}

func TestService_CreateUser(t *testing.T) {
	env := newTestEnv(t)

	card := ebs_fields.User{Mobile: "0912141660", Password: "me@Suckit1"}
	payload, _ := json.Marshal(card)
	req := httptest.NewRequest("POST", "/register", bytes.NewBuffer(payload))
	resp, err := env.Router.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	if resp.StatusCode != 201 {
		t.Errorf("expected: %d, got: %d", 201, resp.StatusCode)
	}
}

func TestService_LoginHandler(t *testing.T) {
	env := newTestEnv(t)
	seedUser(t, env.Store, env.Tenant, "0912141660", "me@Suckit1")

	card := ebs_fields.User{Mobile: "0912141660", Password: "me@Suckit1"}
	payload, _ := json.Marshal(card)
	req := httptest.NewRequest("POST", "/login", bytes.NewBuffer(payload))
	resp, err := env.Router.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	if resp.StatusCode != 200 {
		t.Errorf("expected: %d, got: %d", 200, resp.StatusCode)
	}
	token := resp.Header.Get("Authorization")
	if token == "" {
		t.Fatal("expected Authorization header to be set")
	}
	claims, err := env.Auth.VerifyJWT(token)
	if err != nil {
		t.Fatalf("verify jwt: %v", err)
	}
	if claims.Mobile != "0912141660" {
		t.Fatalf("expected jwt mobile to be %q, got %q", "0912141660", claims.Mobile)
	}
}
