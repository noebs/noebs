package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestGetTransactionsUsesCardVaultMaskedCards(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeEBSAdapter})
	ctx := context.Background()
	if err := storeSvc.CreateTransaction(ctx, tenantID, ebs_fields.EBSResponse{
		UUID:            "tx-1",
		PAN:             "9222081700000000",
		ResponseCode:    0,
		ResponseMessage: "approved",
	}); err != nil {
		t.Fatalf("seed transaction: %v", err)
	}

	var sawCardVault bool
	cardVaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/card-vault/cards/masked" {
			t.Fatalf("card-vault path = %s", r.URL.Path)
		}
		if r.Header.Get(gateway.GatewayTenantIDHeader) != tenantID {
			t.Fatalf("tenant header = %q", r.Header.Get(gateway.GatewayTenantIDHeader))
		}
		if r.Header.Get(gateway.GatewayUserIDHeader) != "42" {
			t.Fatalf("user header = %q", r.Header.Get(gateway.GatewayUserIDHeader))
		}
		var cmd MaskedCardsCommand
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			t.Fatalf("decode masked cards command: %v", err)
		}
		sawCardVault = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(MaskedCardsResult{MaskedPANs: []string{"922208*****0000"}})
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

	transactions, err := service.GetTransactionsForUserID(ctx, tenantID, 42)
	if err != nil {
		t.Fatalf("get transactions: %v", err)
	}
	if !sawCardVault {
		t.Fatalf("card-vault was not called")
	}
	if len(transactions) != 1 || transactions[0].UUID != "tx-1" || transactions[0].PAN != "922208*****0000" {
		t.Fatalf("transactions = %+v", transactions)
	}
	if _, err := db.ExecContext(ctx, "SELECT 1 FROM users LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("ebs-adapter scope should not create user tables, err=%v", err)
	}
	if _, err := db.ExecContext(ctx, "SELECT 1 FROM cards LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("ebs-adapter scope should not create card tables, err=%v", err)
	}
}

func TestGetTransactionsRejectsMissingUserID(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeEBSAdapter})
	service := &Service{Store: storeSvc}

	if _, err := service.GetTransactionsForUserID(context.Background(), tenantID, 0); !errors.Is(err, store.ErrInvalidUserID) {
		t.Fatalf("missing user_id error = %v", err)
	}
}
