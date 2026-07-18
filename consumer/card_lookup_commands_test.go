package consumer

import (
	"context"
	"errors"
	"testing"

	"github.com/adonese/noebs/store"
)

func TestLegacyCardResolversFailClosedWithoutMutation(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeCardVault})
	service := &Service{Store: storeSvc}
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "mobile",
			call: func() error {
				_, err := service.ResolveCardByMobile(ctx, tenantID, CardByMobileCommand{Mobile: "0912141660"})
				return err
			},
		},
		{
			name: "mobile and PAN",
			call: func() error {
				_, err := service.ResolveCardByMobilePAN(ctx, tenantID, CardByMobilePANCommand{
					Mobile: "0912141660",
					PAN:    "9222081700000000",
				})
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, store.ErrLegacyCardOperation) {
				t.Fatalf("error = %v, want %v", err, store.ErrLegacyCardOperation)
			}

			var cards int
			if err := db.GetContext(ctx, &cards, "SELECT COUNT(*) FROM cards"); err != nil {
				t.Fatalf("count cards: %v", err)
			}
			if cards != 0 {
				t.Fatalf("legacy lookup mutated %d cards", cards)
			}
		})
	}

	var identityTableExists bool
	if err := db.GetContext(ctx, &identityTableExists, "SELECT to_regclass('users') IS NOT NULL"); err != nil {
		t.Fatalf("inspect identity table: %v", err)
	}
	if identityTableExists {
		t.Fatal("card-vault scope created identity tables")
	}
}
