package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
		assertAdminCommandHeaders(t, r, tenantID)
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
		if r.URL.Path != "/internal/identity-auth/recovery-jwt" {
			t.Fatalf("identity path = %s", r.URL.Path)
		}
		assertAdminCommandHeaders(t, r, tenantID)
		var cmd RecoveryJWTCommand
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			t.Fatalf("decode recovery jwt command: %v", err)
		}
		if cmd.UserID != 42 || cmd.Mobile != "0912141660" {
			t.Fatalf("identity command = %+v", cmd)
		}
		sawIdentity = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RecoveryJWTResult{Token: "recovery-token"})
	}))
	t.Cleanup(identityServer.Close)

	var sawAdminReporting bool
	adminReportingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/admin-reporting/transactions" {
			t.Fatalf("admin-reporting path = %s", r.URL.Path)
		}
		assertAdminCommandHeaders(t, r, tenantID)
		var cmd transactionProjectionCommand
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			t.Fatalf("decode admin-reporting command: %v", err)
		}
		if cmd.Transaction == nil || cmd.Transaction.UUID != "balance-step-uuid" {
			t.Fatalf("admin-reporting command = %+v", cmd)
		}
		sawAdminReporting = true
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(adminReportingServer.Close)

	service := &Service{
		Store:      storeSvc,
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
		NoebsConfig: ebs_fields.NoebsConfig{
			ConsumerIP: ebsServer.URL + "/",
			ConsumerID: "consumer-app",
			ServiceDiscovery: map[string]string{
				cardVaultServiceDiscoveryKey:      cardVaultServer.URL,
				identityAuthServiceDiscoveryKey:   identityServer.URL,
				adminReportingServiceDiscoveryKey: adminReportingServer.URL,
			},
		},
	}

	token, err := service.BalanceStep(context.Background(), tenantID, BalanceStepRequest{
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
	if token != "recovery-token" {
		t.Fatalf("token = %q", token)
	}
	if !sawCardVault || !sawEBS || !sawIdentity || !sawAdminReporting {
		t.Fatalf("sawCardVault=%v sawEBS=%v sawIdentity=%v sawAdminReporting=%v", sawCardVault, sawEBS, sawIdentity, sawAdminReporting)
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM users LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("ebs-adapter scope should not create user tables, err=%v", err)
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM cards LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("ebs-adapter scope should not create card tables, err=%v", err)
	}
}

func TestBalanceStepSurfacesProjectionErrors(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeEBSAdapter})

	cardVaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertAdminCommandHeaders(t, r, tenantID)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CardByMobilePANResult{UserID: 42, ExpDate: "2901"})
	}))
	t.Cleanup(cardVaultServer.Close)

	ebsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	identityServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("identity-auth should not be called after projection failure")
	}))
	t.Cleanup(identityServer.Close)

	adminReportingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertAdminCommandHeaders(t, r, tenantID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(serviceCommandErrorPayload{Code: ErrAdminReportingCommand.Error()})
	}))
	t.Cleanup(adminReportingServer.Close)

	service := &Service{
		Store:      storeSvc,
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
		NoebsConfig: ebs_fields.NoebsConfig{
			ConsumerIP: ebsServer.URL + "/",
			ConsumerID: "consumer-app",
			ServiceDiscovery: map[string]string{
				cardVaultServiceDiscoveryKey:      cardVaultServer.URL,
				identityAuthServiceDiscoveryKey:   identityServer.URL,
				adminReportingServiceDiscoveryKey: adminReportingServer.URL,
			},
		},
	}

	_, err := service.BalanceStep(context.Background(), tenantID, BalanceStepRequest{
		ConsumerBalanceFields: ebs_fields.ConsumerBalanceFields{
			ConsumerCardHolderFields: ebs_fields.ConsumerCardHolderFields{
				Pan: "23232323",
			},
		},
		Mobile: "0912141660",
	})
	if !errors.Is(err, ErrAdminReportingCommand) {
		t.Fatalf("balance step err = %v, want %v", err, ErrAdminReportingCommand)
	}
	if errors.Is(err, ErrTransactionFailed) {
		t.Fatalf("projection failure was flattened to transaction failure: %v", err)
	}
}
