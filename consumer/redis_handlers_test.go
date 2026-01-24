package consumer

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
)

func TestService_CardFromNumber_ReturnsPan(t *testing.T) {
	env := newTestEnv(t)
	env.Router.Get("/card_from_number", env.Service.CardFromNumber)

	ctx := context.Background()
	user := seedUser(t, env.Store, env.Tenant, "0912141660", "My$Passw0rd!")
	if err := env.Store.AddCards(ctx, env.Tenant, user.ID, []ebs_fields.Card{{Pan: "99999"}}); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	req := httptest.NewRequest("GET", "/card_from_number?mobile_number=0912141660", nil)
	resp, err := env.Router.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var payload map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["result"] != "99999" {
		t.Fatalf("expected result 99999, got %q", payload["result"])
	}
}

func TestService_CardFromNumber_NotFound(t *testing.T) {
	env := newTestEnv(t)
	env.Router.Get("/card_from_number", env.Service.CardFromNumber)

	req := httptest.NewRequest("GET", "/card_from_number?mobile_number=0912141660", nil)
	resp, err := env.Router.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("expected status 400, got %d", resp.StatusCode)
	}
}
