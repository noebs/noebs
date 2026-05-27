package consumer

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestResolveCardByMobileUsesCardVaultScope(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeCardVault})
	service := &Service{
		Store:      storeSvc,
		HTTPClient: testHTTPClient(),
	}
	if err := storeSvc.AddCards(context.Background(), tenantID, 42, []ebs_fields.Card{{Pan: "9222081700000000", Expiry: "2601", Mobile: "0912141660"}}); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	result, err := service.ResolveCardByMobile(context.Background(), tenantID, CardByMobileCommand{Mobile: "0912141660"})
	if err != nil {
		t.Fatalf("resolve card by mobile: %v", err)
	}
	if result.PAN != "9222081700000000" || result.ExpDate != "2601" {
		t.Fatalf("card result = %+v", result)
	}
}

func testHTTPClient() *http.Client {
	return &http.Client{Timeout: 2 * time.Second}
}
