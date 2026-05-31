package activity

import (
	"context"
	"errors"
	"testing"

	"github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
)

func TestRecordInteractionRequiresExplicitProvider(t *testing.T) {
	activities := &PSPActivities{Store: &walletstore.Store{}}

	err := activities.recordInteraction(context.Background(), walletstore.PSPInteraction{
		TenantID:        "tenant-a",
		InteractionType: "deposit_verify",
	})
	if !errors.Is(err, walletstore.ErrMissingProviderCode) {
		t.Fatalf("recordInteraction() error = %v, want %v", err, walletstore.ErrMissingProviderCode)
	}
}

func TestNewPSPActivitiesUsesLoaderStoreForAuditing(t *testing.T) {
	storeSvc := &walletstore.Store{}
	activities := NewPSPActivities(&psp.Loader{Store: storeSvc}, psp.NewRegistry())
	if activities.Store != storeSvc {
		t.Fatalf("activities.Store = %p, want loader store %p", activities.Store, storeSvc)
	}
}

func TestResolveProviderRequiresAuditStoreBeforeProviderWork(t *testing.T) {
	activities := &PSPActivities{
		Loader:   &psp.Loader{},
		Registry: psp.NewRegistry(),
	}
	_, _, err := activities.resolveProvider(context.Background(), "tenant-a", "provider", "SDG", "deposit", "")
	if !errors.Is(err, ErrMissingStore) {
		t.Fatalf("resolveProvider() error = %v, want %v", err, ErrMissingStore)
	}
}
