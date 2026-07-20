package consumer

import (
	"context"
	"errors"
	"testing"

	"github.com/adonese/noebs/store"
)

func TestUserMiscTenantValidationFailsBeforeDBOrHTTP(t *testing.T) {
	service := &Service{
		Store:      &store.Store{},
		HTTPClient: testHTTPClient(),
	}
	ctx := context.Background()
	cases := []struct {
		name string
		run  func(string) error
	}{
		{"GetTransactionsForUserID", func(tenantID string) error {
			_, err := service.GetTransactionsForUserID(ctx, tenantID, 1)
			return err
		}},
	}
	tenantCases := []struct {
		tenantID string
		wantErr  error
	}{
		{"", store.ErrMissingTenantID},
		{"default", store.ErrInvalidTenantID},
	}
	for _, tc := range cases {
		for _, tenantCase := range tenantCases {
			t.Run(tc.name+"/"+tenantCase.tenantID, func(t *testing.T) {
				err := tc.run(tenantCase.tenantID)
				if !errors.Is(err, tenantCase.wantErr) {
					t.Fatalf("expected %v, got %v", tenantCase.wantErr, err)
				}
			})
		}
	}
}
