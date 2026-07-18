package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestService_isValidCard(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeEBSAdapter})

	var sawEBS bool
	ebsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+ebs_fields.ConsumerBalanceEndpoint {
			t.Fatalf("EBS path = %s", r.URL.Path)
		}
		sawEBS = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ebs_fields.EBSParserFields{
			EBSResponse: ebs_fields.EBSResponse{
				UUID:            "valid-card-balance",
				ResponseCode:    0,
				ResponseMessage: "Success",
			},
		})
	}))
	t.Cleanup(ebsServer.Close)

	service := &Service{
		Store:      storeSvc,
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
		NoebsConfig: ebs_fields.NoebsConfig{
			ConsumerIP:            ebsServer.URL + "/",
			ConsumerID:            "consumer-app",
			EBSConsumerKey:        "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA4Jj+8WL5ANXllkz9lkOKRmXnDzQ+yS/VFKxKttkk4o5duJPPFZzJ0E3/m1F6xqEVPH2aM2IpSKN/SgeBv9NL6y+qgms7GbpnQ8MCilLIFWNGuTeRzDNVIR7yIqQ0jHX3dgrJyiDp02LQnQtMTRhzOYDZnwOnweixwEzAk8yPEeXQyzp867rUsLZ4jIIChRcI06UTFdMQrd7KZReTt5hunjQLH+qJBaMj1yAQGmf9C10MeC3Nnp4oE7m0OuTkTvekHnsaAtyY+TFg/UBvMQOyp9uJG6OwdvV6doI3MmXg16K6WJx1J1xewG6e28Tvt13z5mEljj8dnWQcqmhuASRlZwIDAQAB",
			BillInquiryIPIN:       "0000",
			KafkaTransactionTopic: testKafkaTransactionTopic,
		},
	}
	type args struct {
		card ebs_fields.CacheCards
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{"valid", args{ebs_fields.CacheCards{Pan: "99999", Expiry: "2901"}}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := service.isValidCard(noConsumerTransactionContext(), tenantID, tt.args.card)
			if (err != nil) != tt.wantErr {
				t.Errorf("Service.isValidCard() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Service.isValidCard() = %v, want %v", got, tt.want)
			}
		})
	}
	if !sawEBS {
		t.Fatalf("sawEBS=%v", sawEBS)
	}
}

func TestGetIpinPubKeyReturnsTypedEBSCallError(t *testing.T) {
	ebsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+ebs_fields.QRPublicKey {
			t.Fatalf("EBS path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ebs_fields.EBSParserFields{
			EBSResponse: ebs_fields.EBSResponse{
				ResponseCode:    72,
				ResponseMessage: "Failed",
			},
		})
	}))
	t.Cleanup(ebsServer.Close)

	service := &Service{
		Store:      &store.Store{},
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
		NoebsConfig: ebs_fields.NoebsConfig{
			IPINIp:                ebsServer.URL + "/",
			EBSIPINUsername:       "ipin-user",
			KafkaTransactionTopic: testKafkaTransactionTopic,
		},
	}

	err := service.GetIpinPubKey(noConsumerTransactionContext(), "tenant-a")
	var callErr *ebs_fields.CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("GetIpinPubKey() error = %v, want CallError", err)
	}
	if callErr.Status != http.StatusBadGateway {
		t.Fatalf("CallError status = %d, want %d", callErr.Status, http.StatusBadGateway)
	}
	if callErr.Response.ResponseMessage != "Failed" {
		t.Fatalf("CallError response message = %q, want Failed", callErr.Response.ResponseMessage)
	}
	if !errors.Is(err, store.ErrMissingUUID) {
		t.Fatalf("GetIpinPubKey() error = %v, want missing UUID record error", err)
	}
}

func TestService_Notifications(t *testing.T) {

	env := newTestEnv(t)

	user := seedUser(t, env.Store, env.Tenant, "0129751986", "Me@Passw0rd!")
	seed := PushData{UUID: "uuid-1", Body: "test me", UserMobile: user.Mobile, Phone: user.Mobile}
	if err := env.Store.CreatePushData(context.Background(), env.Tenant, (*ebs_fields.PushDataRecord)(&seed)); err != nil {
		t.Fatalf("seed notification: %v", err)
	}

	var data []PushData
	records, err := env.Service.Notifications(context.Background(), env.Tenant, user.Mobile)
	if err != nil {
		t.Fatalf("notifications: %v", err)
	}
	for _, rec := range records {
		data = append(data, PushData(rec))
	}
	if len(data) == 0 {
		t.Errorf("no response")
	}
	if data[0].Body != "test me" {
		t.Error("wrong data")
	}
}

func TestService_NotificationsUseNotificationScopeOnly(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeNotificationChat})
	service := &Service{Store: storeSvc}
	mobile := "0129751986"
	seed := PushData{UUID: "uuid-notification-only", Body: "notification scope", UserMobile: mobile}
	if err := storeSvc.CreatePushData(context.Background(), tenantID, (*ebs_fields.PushDataRecord)(&seed)); err != nil {
		t.Fatalf("seed notification: %v", err)
	}

	records, err := service.Notifications(context.Background(), tenantID, mobile)
	if err != nil {
		t.Fatalf("notifications: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records len = %d, want 1", len(records))
	}
	if records[0].Body != "notification scope" {
		t.Fatalf("body = %q", records[0].Body)
	}
}

func TestService_NotificationsMatchExplicitPhone(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeNotificationChat})
	service := &Service{Store: storeSvc}
	mobile := "0129751986"
	seed := PushData{UUID: "uuid-phone-notification", Body: "phone scope", Phone: mobile}
	if err := storeSvc.CreatePushData(context.Background(), tenantID, (*ebs_fields.PushDataRecord)(&seed)); err != nil {
		t.Fatalf("seed notification: %v", err)
	}

	records, err := service.Notifications(context.Background(), tenantID, mobile)
	if err != nil {
		t.Fatalf("notifications: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records len = %d, want 1", len(records))
	}
	if records[0].Body != "phone scope" {
		t.Fatalf("body = %q", records[0].Body)
	}
}
