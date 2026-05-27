package activity

import (
	"context"
	"errors"
	"testing"

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
