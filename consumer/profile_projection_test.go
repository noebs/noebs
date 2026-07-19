package consumer

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/adonese/noebs/store"
)

func TestProfileProjectionServiceCreateAndResolveAreSeparate(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeIdentityAuth})
	service := &Service{Store: storeSvc}
	ctx := context.Background()
	reference := PrincipalProjectionReference{
		Issuer:  "https://identity.example/realms/noebs",
		Subject: "ef8028d7-5e58-422b-a438-3d87914ca12f",
	}
	if _, err := service.ResolveProfileProjection(ctx, tenantID, reference); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("resolve-before-create error = %v, want sql.ErrNoRows", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("resolve-before-create inserted %d rows", count)
	}
	created, err := service.CreateProfileProjection(ctx, tenantID, reference, CreateProfileProjectionCommand{
		Fullname: "Profile Owner",
		Mobile:   "0990000000",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := service.ResolveProfileProjection(ctx, tenantID, reference)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.UserID != created.UserID || resolved.Issuer != reference.Issuer || resolved.Subject != reference.Subject {
		t.Fatalf("resolved = %+v, created = %+v", resolved, created)
	}
}

func TestProfileProjectionServiceRejectsMissingStore(t *testing.T) {
	service := &Service{}
	if _, err := service.ResolveProfileProjection(context.Background(), "tenant", PrincipalProjectionReference{}); !errors.Is(err, ErrMissingStore) {
		t.Fatalf("resolve error = %v, want ErrMissingStore", err)
	}
	if _, err := service.CreateProfileProjection(context.Background(), "tenant", PrincipalProjectionReference{}, CreateProfileProjectionCommand{}); !errors.Is(err, ErrMissingStore) {
		t.Fatalf("create error = %v, want ErrMissingStore", err)
	}
}
