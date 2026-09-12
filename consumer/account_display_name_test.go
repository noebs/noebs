package consumer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/adonese/noebs/store"
)

func TestAccountDisplayNameKeepsActorAndTargetSeparate(t *testing.T) {
	_, st, tenant := newTestDBWithScopes(t, []string{store.MigrationScopeIdentityAuth})
	s := &Service{Store: st}
	ctx := context.Background()
	actorRef := PrincipalProjectionReference{Issuer: "https://identity.example", Subject: "sender"}
	actor, err := s.CreateProfileProjection(ctx, tenant, actorRef, CreateProfileProjectionCommand{Fullname: "Sender account"})
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := s.CreateProfileProjection(ctx, tenant, PrincipalProjectionReference{Issuer: actorRef.Issuer, Subject: "recipient"},
		CreateProfileProjectionCommand{Fullname: "Recipient account", Email: "private@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.ResolveAccountDisplayName(ctx, tenant, actorRef, actor.UserID, recipient.UserID)
	if err != nil || result.DisplayName != recipient.Fullname {
		t.Fatalf("resolved name: %+v, %v", result, err)
	}
	body, err := json.Marshal(result)
	if err != nil || string(body) != `{"display_name":"Recipient account"}` {
		t.Fatalf("narrow response leaked profile data: %s, %v", body, err)
	}
	if _, err := s.ResolveAccountDisplayName(ctx, tenant, actorRef, recipient.UserID, recipient.UserID); !errors.Is(err, ErrAccountDisplayNameActor) {
		t.Fatalf("numeric actor impersonation accepted: %v", err)
	}
	if _, err := s.ResolveAccountDisplayName(ctx, "other-tenant", actorRef, actor.UserID, recipient.UserID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("tenant scope lost: %v", err)
	}
	if _, err := s.ResolveAccountDisplayName(ctx, tenant, actorRef, actor.UserID, recipient.UserID+1000); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown recipient accepted: %v", err)
	}
	for _, id := range []int64{0, -1} {
		if _, err := s.ResolveAccountDisplayName(ctx, tenant, actorRef, actor.UserID, id); !errors.Is(err, store.ErrInvalidUserID) {
			t.Fatalf("invalid recipient reached database: %v", err)
		}
	}
}

func TestAccountDisplayNameStoreRequiresExplicitInputs(t *testing.T) {
	s := &store.Store{}
	if _, err := s.AccountDisplayName(context.Background(), "", 1); !errors.Is(err, store.ErrMissingTenantID) {
		t.Fatalf("missing tenant: %v", err)
	}
	if _, err := s.AccountDisplayName(context.Background(), "tenant", 0); !errors.Is(err, store.ErrInvalidUserID) {
		t.Fatalf("missing recipient: %v", err)
	}
}
