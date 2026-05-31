package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestCheckUserUsesIdentityAndCardVaultScopes(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeIdentityAuth})
	seedUser(t, storeSvc, tenantID, "0912141660", "My$Passw0rd!")

	var sawCardVault bool
	cardVaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/card-vault/cards/masked-by-mobile" {
			t.Fatalf("card-vault path = %s", r.URL.Path)
		}
		assertAdminCommandHeaders(t, r, tenantID)
		var cmd MaskedCardByMobileCommand
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			t.Fatalf("decode masked card command: %v", err)
		}
		if cmd.Mobile != "0912141660" {
			t.Fatalf("card-vault mobile = %q", cmd.Mobile)
		}
		sawCardVault = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(MaskedCardByMobileResult{MaskedPAN: "922208*****0000"})
	}))
	t.Cleanup(cardVaultServer.Close)

	service := &Service{
		Store:      storeSvc,
		HTTPClient: cardVaultServer.Client(),
		NoebsConfig: ebs_fields.NoebsConfig{
			ServiceDiscovery: map[string]string{
				cardVaultServiceDiscoveryKey: cardVaultServer.URL,
			},
		},
	}

	result, err := service.CheckUser(context.Background(), tenantID, []string{"0912141660", "0999999999"})
	if err != nil {
		t.Fatalf("check user: %v", err)
	}
	if !sawCardVault {
		t.Fatalf("card-vault was not called")
	}
	if len(result) != 2 {
		t.Fatalf("result length = %d, want 2: %+v", len(result), result)
	}
	if result[0] != (CheckUserResult{Phone: "0912141660", IsUser: true, Pan: "922208*****0000"}) {
		t.Fatalf("existing user result = %+v", result[0])
	}
	if result[1] != (CheckUserResult{Phone: "0999999999", IsUser: false}) {
		t.Fatalf("missing user result = %+v", result[1])
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM cards LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("identity-auth scope should not create card tables, err=%v", err)
	}
}

func TestCheckUserSkipsUsersWithoutCardVaultCard(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeIdentityAuth})
	seedUser(t, storeSvc, tenantID, "0912141660", "My$Passw0rd!")

	cardVaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertAdminCommandHeaders(t, r, tenantID)
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(serviceCommandErrorPayload{Code: ErrReceiverHasNoCard.Error()})
	}))
	t.Cleanup(cardVaultServer.Close)

	service := &Service{
		Store:      storeSvc,
		HTTPClient: cardVaultServer.Client(),
		NoebsConfig: ebs_fields.NoebsConfig{
			ServiceDiscovery: map[string]string{
				cardVaultServiceDiscoveryKey: cardVaultServer.URL,
			},
		},
	}

	result, err := service.CheckUser(context.Background(), tenantID, []string{"0912141660"})
	if err != nil {
		t.Fatalf("check user: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("result = %+v, want empty", result)
	}
}

func TestCheckUserRequiresCardVaultClient(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	if _, err := service.CheckUser(context.Background(), "tenant-a", []string{"0912141660"}); !errors.Is(err, ErrMissingHTTPClient) {
		t.Fatalf("missing http client error = %v", err)
	}
}

func TestCheckUserPropagatesIdentityLookupErrors(t *testing.T) {
	service := &Service{Store: &store.Store{}, HTTPClient: http.DefaultClient}
	_, err := service.CheckUser(context.Background(), "tenant-a", []string{"0912141660"})
	if err == nil {
		t.Fatal("CheckUser() error = nil, want identity lookup error")
	}
	if !strings.Contains(err.Error(), "nil db") {
		t.Fatalf("CheckUser() error = %v, want identity lookup error", err)
	}
}
