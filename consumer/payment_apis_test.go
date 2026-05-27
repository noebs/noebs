package consumer

import (
	"context"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
)

func TestService_RegisterWithCard(t *testing.T) {
	env := newTestEnv(t)

	ctx := context.Background()
	user := seedUser(t, env.Store, env.Tenant, "0900000000", "Seed@Pass1")
	seedCard := ebs_fields.Card{Pan: "23232323", Expiry: "2901", Mobile: user.Mobile}
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

	otp, err := env.Service.RegisterWithCard(ctx, env.Tenant, card)
	if err != nil {
		t.Fatalf("register with card: %v", err)
	}
	if otp == "" {
		t.Fatalf("expected otp to be non-empty")
	}
}

func TestService_CreateUser(t *testing.T) {
	env := newTestEnv(t)

	user, err := env.Service.CreateUser(context.Background(), env.Tenant, ebs_fields.User{
		Mobile:   "0912141660",
		Username: "0912141660",
		Password: "me@Suckit1",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if user.ID == 0 {
		t.Fatalf("expected created user id to be set")
	}
}

func TestService_LoginHandler(t *testing.T) {
	env := newTestEnv(t)
	seedUser(t, env.Store, env.Tenant, "0912141660", "me@Suckit1")

	token, _, err := env.Service.Login(context.Background(), env.Tenant, "0912141660", "me@Suckit1")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if token == "" {
		t.Fatal("expected token to be set")
	}
	claims, err := env.Auth.VerifyJWT(token)
	if err != nil {
		t.Fatalf("verify jwt: %v", err)
	}
	if claims.Mobile != "0912141660" {
		t.Fatalf("expected jwt mobile to be %q, got %q", "0912141660", claims.Mobile)
	}
}
