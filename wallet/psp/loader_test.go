package psp

import (
	"errors"
	"testing"

	walletstore "github.com/adonese/noebs/wallet/store"
)

func TestLoaderValidatesTenantAndProviderBeforeStore(t *testing.T) {
	loader := &Loader{Store: &walletstore.Store{}}
	cases := []struct {
		name         string
		tenantID     string
		providerCode string
		wantErr      error
	}{
		{"missing_tenant", "", "provider", walletstore.ErrMissingTenantID},
		{"invalid_tenant", "default", "provider", walletstore.ErrInvalidTenantID},
		{"missing_provider", "tenant-a", "", walletstore.ErrMissingProviderCode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loader.Load(t.Context(), tc.tenantID, tc.providerCode)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Load() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
