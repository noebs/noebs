package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestMobileTransferResolvesReceiverThroughCardVault(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeEBSAdapter})
	var sawCardVault bool
	cardVaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/card-vault/cards/by-mobile" {
			t.Fatalf("card-vault path = %s", r.URL.Path)
		}
		assertAdminCommandHeaders(t, r, tenantID)
		var cmd CardByMobileCommand
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			t.Fatalf("decode card-vault command: %v", err)
		}
		if cmd.Mobile != "0912141660" {
			t.Fatalf("card-vault mobile = %q", cmd.Mobile)
		}
		sawCardVault = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CardByMobileResult{PAN: "9222081700000000", ExpDate: "2601"})
	}))
	t.Cleanup(cardVaultServer.Close)

	var sawEBS bool
	ebsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+ebs_fields.ConsumerCardTransferEndpoint {
			t.Fatalf("EBS path = %s", r.URL.Path)
		}
		body := readBodyForTest(t, r)
		if !bytes.Contains(body, []byte(`"toCard":"9222081700000000"`)) {
			t.Fatalf("EBS request did not contain card-vault PAN: %s", body)
		}
		if !bytes.Contains(body, []byte(`"PAN":"9222081700009999"`)) {
			t.Fatalf("EBS request did not contain sender PAN: %s", body)
		}
		sawEBS = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ebs_fields.EBSParserFields{
			EBSResponse: ebs_fields.EBSResponse{
				ResponseCode:    0,
				ResponseMessage: "Approved",
				PAN:             "9222081700009999",
				ToCard:          "9222081700000000",
				AccountCurrency: "SDG",
			},
		})
	}))
	t.Cleanup(ebsServer.Close)

	service := &Service{
		Store:      storeSvc,
		HTTPClient: testHTTPClient(),
		NoebsConfig: ebs_fields.NoebsConfig{
			ConsumerIP: ebsServer.URL + "/",
			ServiceDiscovery: map[string]string{
				cardVaultServiceDiscoveryKey: cardVaultServer.URL,
			},
		},
	}
	res, err := service.MobileTransfer(context.Background(), tenantID, ebs_fields.ConsumerMobileTransferFields{
		ConsumerCommonFields: ebs_fields.ConsumerCommonFields{
			ApplicationId: "consumer-app",
			TranDateTime:  "270526205500",
			UUID:          "transfer-uuid",
			DeviceID:      "sender-device",
		},
		ConsumerCardHolderFields: ebs_fields.ConsumerCardHolderFields{
			Pan:     "9222081700009999",
			Ipin:    "encrypted-ipin",
			ExpDate: "2601",
		},
		AmountFields: ebs_fields.AmountFields{
			TranAmount:       50,
			TranCurrencyCode: "SDG",
		},
		Mobile: "0912141660",
	})
	if err != nil {
		t.Fatalf("mobile transfer: %v", err)
	}
	if res.ResponseCode != 0 {
		t.Fatalf("EBS response = %+v", res)
	}
	if !sawCardVault || !sawEBS {
		t.Fatalf("sawCardVault=%v sawEBS=%v", sawCardVault, sawEBS)
	}
}
