package consumer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestService_CardFromNumber_ReturnsPan(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeCardVault})
	service := &Service{
		Store:      storeSvc,
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	}

	ctx := context.Background()
	if err := storeSvc.AddCards(ctx, tenantID, 42, []ebs_fields.Card{{Pan: "99999", Mobile: "0912141660"}}); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	pan, err := service.CardFromNumber(ctx, tenantID, "0912141660")
	if err != nil {
		t.Fatalf("card from number: %v", err)
	}
	if pan != "99999" {
		t.Fatalf("expected pan 99999, got %q", pan)
	}
}

func TestService_CardFromNumber_NotFound(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeCardVault})
	service := &Service{
		Store:      storeSvc,
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	}

	_, err := service.CardFromNumber(context.Background(), tenantID, "0912141660")
	if err == nil {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestService_GetUserCardsUsesCardVaultMobileMapping(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeCardVault})
	service := &Service{
		Store:      storeSvc,
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	}
	if err := storeSvc.AddCards(context.Background(), tenantID, 42, []ebs_fields.Card{{Pan: "99999", Expiry: "2601", Mobile: "0912141660"}}); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	user, err := service.GetUserCards(context.Background(), tenantID, "0912141660")
	if err != nil {
		t.Fatalf("get user cards: %v", err)
	}
	if user.Mobile != "0912141660" || user.MainCard != "99999" || user.ExpDate != "2601" || len(user.Cards) != 1 {
		t.Fatalf("user cards = %+v", user)
	}
}

func TestResolveIdentityUserByMobileUsesIdentityScope(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeIdentityAuth})
	seedUser(t, storeSvc, tenantID, "0912141660", "My$Passw0rd!")
	service := &Service{Store: storeSvc}

	result, err := service.ResolveIdentityUserByMobile(context.Background(), tenantID, IdentityUserByMobileCommand{Mobile: "0912141660"})
	if err != nil {
		t.Fatalf("resolve identity user: %v", err)
	}
	if result.UserID <= 0 || result.Mobile != "0912141660" {
		t.Fatalf("identity result = %+v", result)
	}
}

func newIdentityUserByMobileServer(t *testing.T, tenantID, mobile string, userID int64) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/identity-auth/users/by-mobile" {
			t.Fatalf("identity path = %s", r.URL.Path)
		}
		assertAdminCommandHeaders(t, r, tenantID)
		var cmd IdentityUserByMobileCommand
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			t.Fatalf("decode identity command: %v", err)
		}
		if cmd.Mobile != mobile {
			t.Fatalf("identity mobile = %q, want %q", cmd.Mobile, mobile)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(IdentityUserByMobileResult{UserID: userID, Mobile: mobile})
	}))
	t.Cleanup(server.Close)
	return server
}
