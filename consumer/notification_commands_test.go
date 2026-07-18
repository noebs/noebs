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

func TestStoreNotificationPushDataUsesNotificationScope(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeNotificationChat})
	service := &Service{Store: storeSvc}

	err := service.StoreNotificationPushData(context.Background(), tenantID, StorePushDataCommand{
		Data: PushData{
			UUID:       "notification-uuid",
			Type:       EBS_NOTIFICATION,
			Title:      "Payment Success",
			Body:       "stored by notification-chat",
			UserMobile: "0912141660",
		},
	})
	if err != nil {
		t.Fatalf("store notification push data: %v", err)
	}

	records, err := storeSvc.GetNotifications(context.Background(), tenantID, "0912141660")
	if err != nil {
		t.Fatalf("get notifications: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].UUID != "notification-uuid" || records[0].TenantID != tenantID {
		t.Fatalf("notification record = %+v", records[0])
	}
}

func TestStoreNotificationPushDataRejectsMissingInputs(t *testing.T) {
	service := &Service{Store: &store.Store{}}

	if err := service.StoreNotificationPushData(context.Background(), "", StorePushDataCommand{Data: PushData{UUID: "uuid"}}); !errors.Is(err, store.ErrMissingTenantID) {
		t.Fatalf("missing tenant error = %v, want %v", err, store.ErrMissingTenantID)
	}
	if err := service.StoreNotificationPushData(context.Background(), "default", StorePushDataCommand{Data: PushData{UUID: "uuid"}}); !errors.Is(err, store.ErrInvalidTenantID) {
		t.Fatalf("reserved tenant error = %v, want %v", err, store.ErrInvalidTenantID)
	}
	if err := service.StoreNotificationPushData(context.Background(), "tenant-a", StorePushDataCommand{}); !errors.Is(err, ErrMissingUUID) {
		t.Fatalf("missing uuid error = %v, want %v", err, ErrMissingUUID)
	}
	if err := service.StoreNotificationPushData(context.Background(), "tenant-a", StorePushDataCommand{Data: PushData{UUID: "uuid"}}); !errors.Is(err, store.ErrMissingPushTarget) {
		t.Fatalf("missing push target error = %v, want %v", err, store.ErrMissingPushTarget)
	}
}

func TestNotificationRecordForEventRequiresTransactionUUID(t *testing.T) {
	if _, err := notificationRecordForEvent(PushData{}, "sender"); !errors.Is(err, ErrMissingUUID) {
		t.Fatalf("notificationRecordForEvent() error = %v, want %v", err, ErrMissingUUID)
	}
}

func TestNotificationTransactionUUIDIsDistinctAndTapAuthorizationUsesParticipants(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{
		store.MigrationScopeEBSAdapter,
		store.MigrationScopeNotificationChat,
	})
	service := &Service{
		Store:       storeSvc,
		NoebsConfig: ebs_fields.NoebsConfig{KafkaTransactionTopic: testKafkaTransactionTopic},
	}
	const transactionUUID = "ed9de23b-734f-4db4-91f0-b6299a7b80a2"
	actorCtx, err := WithTransactionActor(context.Background(), 42)
	if err != nil {
		t.Fatalf("bind actor: %v", err)
	}
	if err := service.recordTransaction(actorCtx, tenantID, ebs_fields.EBSResponse{UUID: transactionUUID, ResponseCode: 0}); err != nil {
		t.Fatalf("record transaction: %v", err)
	}
	notification, err := notificationRecordForEvent(PushData{
		UUID: transactionUUID, UserMobile: "0912141660", Type: EBS_NOTIFICATION,
	}, "sender")
	if err != nil {
		t.Fatalf("build notification event: %v", err)
	}
	if notification.UUID != transactionUUID+":sender" || notification.TransactionUUID != transactionUUID || notification.EBSUUID != transactionUUID {
		t.Fatalf("notification identities = %+v", notification)
	}
	payload, err := json.Marshal(notification)
	if err != nil {
		t.Fatalf("marshal push payload: %v", err)
	}
	if !strings.Contains(string(payload), `"uuid":"`+transactionUUID+`:sender"`) ||
		!strings.Contains(string(payload), `"transaction_uuid":"`+transactionUUID+`"`) ||
		strings.Contains(string(payload), "ebs_uuid") {
		t.Fatalf("push payload identities = %s", payload)
	}
	if err := service.StoreNotificationPushData(context.Background(), tenantID, StorePushDataCommand{Data: notification}); err != nil {
		t.Fatalf("store notification: %v", err)
	}
	records, err := storeSvc.GetNotifications(context.Background(), tenantID, "0912141660")
	if err != nil {
		t.Fatalf("read notifications: %v", err)
	}
	if len(records) != 1 || records[0].UUID != transactionUUID+":sender" || records[0].TransactionUUID != transactionUUID {
		t.Fatalf("stored notification identities = %+v", records)
	}
	if _, err := service.GetTransactionByUUIDForUser(context.Background(), tenantID, 42, records[0].TransactionUUID); err != nil {
		t.Fatalf("participant notification tap: %v", err)
	}
	if _, err := service.GetTransactionByUUIDForUser(context.Background(), tenantID, 84, records[0].TransactionUUID); !errors.Is(err, ErrTransactionNotFound) {
		t.Fatalf("non-participant notification tap error = %v, want %v", err, ErrTransactionNotFound)
	}
}

func TestNotificationRecordRejectsNonCanonicalTransactionUUID(t *testing.T) {
	for _, value := range []string{
		"not-a-uuid",
		"ED9DE23B-734F-4DB4-91F0-B6299A7B80A2",
		" ed9de23b-734f-4db4-91f0-b6299a7b80a2 ",
	} {
		if _, err := notificationRecordForEvent(PushData{UUID: value}, "sender"); !errors.Is(err, store.ErrInvalidTransactionUUID) {
			t.Fatalf("notificationRecordForEvent(%q) error = %v, want %v", value, err, store.ErrInvalidTransactionUUID)
		}
	}
}

func TestSubmitBillerHookUsesNotificationScopeOnly(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeNotificationChat})

	var sawHook bool
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		var payload billerHookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode hook payload: %v", err)
		}
		if payload.PaymentToken != "token-1" || !payload.IsSuccessful || payload.UUID != "ebs-uuid" || payload.ResponseCode != 0 {
			t.Fatalf("hook payload = %+v", payload)
		}
		sawHook = true
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(webhook.Close)

	service := &Service{
		Store:      storeSvc,
		HTTPClient: webhook.Client(),
		NoebsConfig: ebs_fields.NoebsConfig{
			ConsumerBillerHooksURL: webhook.URL,
			IsDebug:                true,
		},
	}
	if err := service.SubmitBillerHook(context.Background(), tenantID, BillerHookCommand{
		EBS: ebs_fields.EBSResponse{
			UUID:            "ebs-uuid",
			ResponseCode:    0,
			ResponseMessage: "Approved",
		},
		IsSuccessful: true,
		Token:        "token-1",
	}); err != nil {
		t.Fatalf("submit biller hook: %v", err)
	}
	if !sawHook {
		t.Fatalf("biller hook endpoint was not called")
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM users LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("notification-chat scope should not create user tables, err=%v", err)
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM cards LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("notification-chat scope should not create card tables, err=%v", err)
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM cache_cards LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("notification-chat scope should not create card cache tables, err=%v", err)
	}
}

func TestSubmitBillerHookRejectsInvalidEndpoint(t *testing.T) {
	service := &Service{
		HTTPClient: testHTTPClient(),
		NoebsConfig: ebs_fields.NoebsConfig{
			ConsumerBillerHooksURL: "http://callbacks.example/hook",
		},
	}
	err := service.SubmitBillerHook(context.Background(), "tenant-a", BillerHookCommand{Token: "token-1"})
	if !errors.Is(err, ErrInvalidBillerHookEndpoint) {
		t.Fatalf("invalid endpoint error = %v, want %v", err, ErrInvalidBillerHookEndpoint)
	}
}

func TestSubmitBillerHookRejectsReservedTenantBeforeHTTP(t *testing.T) {
	service := &Service{
		HTTPClient: testHTTPClient(),
		NoebsConfig: ebs_fields.NoebsConfig{
			ConsumerBillerHooksURL: "https://callbacks.example/hook",
		},
	}
	err := service.SubmitBillerHook(context.Background(), "default", BillerHookCommand{Token: "token-1"})
	if !errors.Is(err, store.ErrInvalidTenantID) {
		t.Fatalf("reserved tenant error = %v, want %v", err, store.ErrInvalidTenantID)
	}
}

func TestSubmitBillerHookInNotificationChatUsesAdminCommand(t *testing.T) {
	var sawCommand bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/notification-chat/biller-hook" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get(gateway.GatewayAdminIdentityHeader) != gateway.GatewayAdminIdentityValue {
			t.Fatalf("admin identity header = %q", r.Header.Get(gateway.GatewayAdminIdentityHeader))
		}
		if r.Header.Get(gateway.GatewayTenantIDHeader) != "tenant-a" {
			t.Fatalf("tenant header = %q", r.Header.Get(gateway.GatewayTenantIDHeader))
		}
		if r.Header.Get("X-Tenant-ID") != "" {
			t.Fatalf("public tenant header forwarded = %q", r.Header.Get("X-Tenant-ID"))
		}
		var cmd BillerHookCommand
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			t.Fatalf("decode command: %v", err)
		}
		if cmd.Token != "token-1" || cmd.EBS.UUID != "ebs-uuid" || !cmd.IsSuccessful {
			t.Fatalf("command = %+v", cmd)
		}
		sawCommand = true
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	service := &Service{
		HTTPClient: server.Client(),
		NoebsConfig: ebs_fields.NoebsConfig{
			ServiceDiscovery: map[string]string{
				notificationServiceDiscoveryKey: server.URL,
			},
		},
	}
	err := service.SubmitBillerHookInNotificationChat(context.Background(), "tenant-a", BillerHookCommand{
		EBS:          ebs_fields.EBSResponse{UUID: "ebs-uuid"},
		IsSuccessful: true,
		Token:        "token-1",
	})
	if err != nil {
		t.Fatalf("submit notification command: %v", err)
	}
	if !sawCommand {
		t.Fatalf("notification command was not sent")
	}
}
