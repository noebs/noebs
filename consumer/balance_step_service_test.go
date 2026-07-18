package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestBalanceStepUsesCardVaultEBSAndIdentityScopes(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeEBSAdapter})

	var sawCardVault bool
	cardVaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/card-vault/cards/by-mobile-pan" {
			t.Fatalf("card-vault path = %s", r.URL.Path)
		}
		assertInternalCommandHeaders(t, r, tenantID)
		var cmd CardByMobilePANCommand
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			t.Fatalf("decode card-vault command: %v", err)
		}
		if cmd.Mobile != "0912141660" || cmd.PAN != "23232323" {
			t.Fatalf("card-vault command = %+v", cmd)
		}
		sawCardVault = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CardByMobilePANResult{UserID: 42, ExpDate: "2901"})
	}))
	t.Cleanup(cardVaultServer.Close)

	var sawEBS bool
	ebsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+ebs_fields.ConsumerBalanceEndpoint {
			t.Fatalf("EBS path = %s", r.URL.Path)
		}
		body := readBodyForTest(t, r)
		if bytes.Contains(body, []byte(`"mobile"`)) {
			t.Fatalf("EBS request leaked mobile: %s", body)
		}
		if !bytes.Contains(body, []byte(`"PAN":"23232323"`)) || !bytes.Contains(body, []byte(`"expDate":"2901"`)) {
			t.Fatalf("EBS request missing card data: %s", body)
		}
		sawEBS = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ebs_fields.EBSParserFields{
			EBSResponse: ebs_fields.EBSResponse{
				UUID:            "balance-step-uuid",
				ResponseCode:    0,
				ResponseMessage: "Success",
			},
		})
	}))
	t.Cleanup(ebsServer.Close)

	var sawIdentity bool
	identityServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/identity-auth/recovery-credential" {
			t.Fatalf("identity path = %s", r.URL.Path)
		}
		assertInternalCommandHeaders(t, r, tenantID)
		var cmd RecoveryCredentialCommand
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			t.Fatalf("decode recovery credential command: %v", err)
		}
		if cmd.UserID != 42 || cmd.Mobile != "0912141660" {
			t.Fatalf("identity command = %+v", cmd)
		}
		sawIdentity = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RecoveryCredentialResult{RecoveryCredential: "recovery-token", ExpiresIn: 600})
	}))
	t.Cleanup(identityServer.Close)

	service := &Service{
		Store:           storeSvc,
		HTTPClient:      &http.Client{Timeout: 2 * time.Second},
		WorkloadSigners: testEBSWorkloadSigners(t),
		NoebsConfig: ebs_fields.NoebsConfig{
			ConsumerIP:            ebsServer.URL + "/",
			ConsumerID:            "consumer-app",
			KafkaTransactionTopic: testKafkaTransactionTopic,
			ServiceDiscovery: map[string]string{
				cardVaultServiceDiscoveryKey:    cardVaultServer.URL,
				identityAuthServiceDiscoveryKey: identityServer.URL,
			},
		},
	}

	credential, err := service.BalanceStep(noConsumerTransactionContext(), tenantID, BalanceStepRequest{
		ConsumerBalanceFields: ebs_fields.ConsumerBalanceFields{
			ConsumerCardHolderFields: ebs_fields.ConsumerCardHolderFields{
				Pan: "23232323",
			},
		},
		Mobile: "0912141660",
	})
	if err != nil {
		t.Fatalf("balance step: %v", err)
	}
	if credential.RecoveryCredential != "recovery-token" || credential.ExpiresIn != 600 {
		t.Fatalf("credential = %+v", credential)
	}
	if !sawCardVault || !sawEBS || !sawIdentity {
		t.Fatalf("sawCardVault=%v sawEBS=%v sawIdentity=%v", sawCardVault, sawEBS, sawIdentity)
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM users LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("ebs-adapter scope should not create user tables, err=%v", err)
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM cards LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("ebs-adapter scope should not create card tables, err=%v", err)
	}
}
