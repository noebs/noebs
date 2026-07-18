package consumer

import (
	"bytes"
	"encoding/json"
	"errors"
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
		assertInternalCommandHeaders(t, r, tenantID)
		var cmd CardByMobileCommand
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			t.Fatalf("decode card-vault command: %v", err)
		}
		if cmd.Mobile != "0912141660" {
			t.Fatalf("card-vault mobile = %q", cmd.Mobile)
		}
		sawCardVault = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CardByMobileResult{UserID: 84, PAN: "9222081700000000", ExpDate: "2601"})
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
				UUID:            "0f8fad5b-d9cb-469f-a165-70867728950e",
				ResponseCode:    0,
				ResponseMessage: "Approved",
				PAN:             "9222081700009999",
				ToCard:          "9222081700000000",
				AccountCurrency: "SDG",
			},
		})
	}))
	t.Cleanup(ebsServer.Close)

	var notifications []StorePushDataCommand
	notificationServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/notification-chat/push-data" {
			t.Fatalf("notification path = %s", r.URL.Path)
		}
		assertInternalCommandHeaders(t, r, tenantID)
		var cmd StorePushDataCommand
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			t.Fatalf("decode notification command: %v", err)
		}
		notifications = append(notifications, cmd)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(notificationServer.Close)

	service := &Service{
		Store:           storeSvc,
		HTTPClient:      testHTTPClient(),
		WorkloadSigners: testEBSWorkloadSigners(t),
		NoebsConfig: ebs_fields.NoebsConfig{
			ConsumerIP:            ebsServer.URL + "/",
			KafkaTransactionTopic: testKafkaTransactionTopic,
			ServiceDiscovery: map[string]string{
				cardVaultServiceDiscoveryKey:    cardVaultServer.URL,
				notificationServiceDiscoveryKey: notificationServer.URL,
			},
		},
	}
	res, err := service.MobileTransfer(transactionActorContext(t, 42), tenantID, ebs_fields.ConsumerMobileTransferFields{
		ConsumerCommonFields: ebs_fields.ConsumerCommonFields{
			ApplicationId: "consumer-app",
			TranDateTime:  "270526205500",
			UUID:          "0f8fad5b-d9cb-469f-a165-70867728950e",
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
	if len(notifications) != 2 {
		t.Fatalf("notification commands = %d, want 2", len(notifications))
	}
	if got, want := notifications[0].Data.UUID, "0f8fad5b-d9cb-469f-a165-70867728950e:receiver"; got != want {
		t.Fatalf("receiver notification uuid = %q, want %q", got, want)
	}
	if got, want := notifications[0].Data.UserMobile, "0912141660"; got != want {
		t.Fatalf("receiver notification user_mobile = %q, want %q", got, want)
	}
	if got, want := notifications[1].Data.UUID, "0f8fad5b-d9cb-469f-a165-70867728950e:sender"; got != want {
		t.Fatalf("sender notification uuid = %q, want %q", got, want)
	}
	if got, want := notifications[1].Data.To, "sender-device"; got != want {
		t.Fatalf("sender notification to = %q, want %q", got, want)
	}
	for _, userID := range []int64{42, 84} {
		history, err := service.GetTransactionsForUserID(t.Context(), tenantID, userID)
		if err != nil {
			t.Fatalf("get participant %d history: %v", userID, err)
		}
		if len(history) != 1 || history[0].UUID != "0f8fad5b-d9cb-469f-a165-70867728950e" {
			t.Fatalf("participant %d history = %+v", userID, history)
		}
	}
}

func TestMobileTransferFailureRetainsActorAndRecipientParticipants(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeEBSAdapter})
	cardVaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CardByMobileResult{UserID: 84, PAN: "9222081700000000", ExpDate: "2601"})
	}))
	t.Cleanup(cardVaultServer.Close)
	ebsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ebs_fields.EBSParserFields{EBSResponse: ebs_fields.EBSResponse{
			UUID: "7c9e6679-7425-40de-944b-e07fc1f90ae7", ResponseCode: 51, ResponseMessage: "Declined",
		}})
	}))
	t.Cleanup(ebsServer.Close)
	notificationServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(notificationServer.Close)

	service := &Service{
		Store:           storeSvc,
		HTTPClient:      testHTTPClient(),
		WorkloadSigners: testEBSWorkloadSigners(t),
		NoebsConfig: ebs_fields.NoebsConfig{
			ConsumerIP:            ebsServer.URL + "/",
			KafkaTransactionTopic: testKafkaTransactionTopic,
			ServiceDiscovery: map[string]string{
				cardVaultServiceDiscoveryKey:    cardVaultServer.URL,
				notificationServiceDiscoveryKey: notificationServer.URL,
			},
		},
	}
	_, err := service.MobileTransfer(transactionActorContext(t, 42), tenantID, ebs_fields.ConsumerMobileTransferFields{
		ConsumerCommonFields:     ebs_fields.ConsumerCommonFields{UUID: "7c9e6679-7425-40de-944b-e07fc1f90ae7", TranDateTime: "270526205500", DeviceID: "sender-device"},
		ConsumerCardHolderFields: ebs_fields.ConsumerCardHolderFields{Pan: "9222081700009999", Ipin: "encrypted-ipin", ExpDate: "2601"},
		AmountFields:             ebs_fields.AmountFields{TranAmount: 50, TranCurrencyCode: "SDG"},
		Mobile:                   "0912141660",
	})
	if err == nil {
		t.Fatal("declined mobile transfer error = nil")
	}
	var callErr *ebs_fields.CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("declined mobile transfer error = %v, want EBS call error", err)
	}
	for _, userID := range []int64{42, 84} {
		history, historyErr := service.GetTransactionsForUserID(t.Context(), tenantID, userID)
		if historyErr != nil {
			t.Fatalf("get failed-transfer participant %d history: %v", userID, historyErr)
		}
		if len(history) != 1 || history[0].UUID != "7c9e6679-7425-40de-944b-e07fc1f90ae7" || history[0].ResponseCode != 51 {
			t.Fatalf("failed-transfer participant %d history = %+v", userID, history)
		}
	}
}
