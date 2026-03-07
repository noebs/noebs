package consumer

import (
	"context"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestService_CardFromNumber_ReturnsPan(t *testing.T) {
	env := newTestEnv(t)

	ctx := context.Background()
	user := seedUser(t, env.Store, env.Tenant, "0912141660", "My$Passw0rd!")
	if err := env.Store.AddCards(ctx, env.Tenant, user.ID, []ebs_fields.Card{{Pan: "99999"}}); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	pan, err := env.Service.CardFromNumber(ctx, env.Tenant, "0912141660")
	if err != nil {
		t.Fatalf("card from number: %v", err)
	}
	if pan != "99999" {
		t.Fatalf("expected pan 99999, got %q", pan)
	}
}

func TestService_CardFromNumber_NotFound(t *testing.T) {
	env := newTestEnv(t)

	_, err := env.Service.CardFromNumber(context.Background(), env.Tenant, "0912141660")
	if !store.ErrNotFound(err) {
		t.Fatalf("expected not found error, got %v", err)
	}
}
