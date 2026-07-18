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

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestCompleteRegistrationCallsEBSThenIdentityAndCardVaultCommands(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeEBSAdapter})
	var sawEBS bool
	ebsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+ebs_fields.ConsumerCompleteRegistration {
			t.Fatalf("EBS path = %s", r.URL.Path)
		}
		body := readBodyForTest(t, r)
		if bytes.Contains(body, []byte(`"password"`)) {
			t.Fatalf("EBS request leaked noebs password: %s", body)
		}
		if bytes.Contains(body, []byte(`"mobile"`)) {
			t.Fatalf("EBS request leaked mobile: %s", body)
		}
		if !bytes.Contains(body, []byte(`"userPassword"`)) {
			t.Fatalf("EBS request missing EBS userPassword: %s", body)
		}
		sawEBS = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ebs_fields.EBSParserFields{
			EBSResponse: ebs_fields.EBSResponse{
				UUID:            "complete-registration-uuid",
				ResponseCode:    0,
				ResponseMessage: "Success",
				PAN:             "9222081700000000",
				ExpDate:         "2601",
			},
		})
	}))
	t.Cleanup(ebsServer.Close)

	var sawIdentity bool
	identityServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/identity-auth/card-registration/users" {
			t.Fatalf("identity path = %s", r.URL.Path)
		}
		assertAdminCommandHeaders(t, r, tenantID)
		body := readBodyForTest(t, r)
		if bytes.Contains(body, []byte(`"pan"`)) || bytes.Contains(body, []byte(`"expDate"`)) {
			t.Fatalf("identity command leaked card data: %s", body)
		}
		var cmd CompletedRegistrationIdentityCommand
		if err := json.Unmarshal(body, &cmd); err != nil {
			t.Fatalf("decode identity command: %v", err)
		}
		if cmd.Mobile != "0912345678" || cmd.Password != "local-password" {
			t.Fatalf("identity command = %+v", cmd)
		}
		sawIdentity = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CompletedRegistrationIdentityResult{UserID: 42})
	}))
	t.Cleanup(identityServer.Close)

	var sawCardVault bool
	cardVaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/card-vault/card-registration/cards" {
			t.Fatalf("card-vault path = %s", r.URL.Path)
		}
		assertAdminCommandHeaders(t, r, tenantID)
		var cmd CompletedRegistrationCardCommand
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			t.Fatalf("decode card-vault command: %v", err)
		}
		if cmd.Mobile != "0912345678" || cmd.UserID != 42 || cmd.PAN != "9222081700000000" || cmd.ExpDate != "2601" {
			t.Fatalf("card-vault command = %+v", cmd)
		}
		sawCardVault = true
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(cardVaultServer.Close)

	service := &Service{
		Store:      storeSvc,
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
		NoebsConfig: ebs_fields.NoebsConfig{
			ConsumerIP:            ebsServer.URL + "/",
			KafkaTransactionTopic: testKafkaTransactionTopic,
			ServiceDiscovery: map[string]string{
				cardVaultServiceDiscoveryKey:    cardVaultServer.URL,
				identityAuthServiceDiscoveryKey: identityServer.URL,
			},
		},
	}
	res, err := service.CompleteRegistration(noConsumerTransactionContext(), tenantID, ebs_fields.ConsumerCompleteRegistrationFields{
		OTP:              "123456",
		IPIN:             "1111",
		OriginalTranUUID: "original-uuid",
		Password:         "ebs-password",
		NoebsPassword:    "local-password",
		Mobile:           "0912345678",
	})
	if err != nil {
		t.Fatalf("complete registration: %v", err)
	}
	if res.PAN != "922208*****0000" {
		t.Fatalf("masked PAN = %q", res.PAN)
	}
	if !sawEBS || !sawIdentity || !sawCardVault {
		t.Fatalf("sawEBS=%v sawIdentity=%v sawCardVault=%v", sawEBS, sawIdentity, sawCardVault)
	}
}

func TestCreateCompletedRegistrationIdentityUsesIdentityScope(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeIdentityAuth})
	service := &Service{Store: storeSvc}
	result, err := service.CreateCompletedRegistrationIdentity(context.Background(), tenantID, CompletedRegistrationIdentityCommand{
		Mobile:   "0912345678",
		Password: "local-password",
	})
	if err != nil {
		t.Fatalf("create completed registration identity: %v", err)
	}
	if result.UserID <= 0 {
		t.Fatalf("user ID = %d", result.UserID)
	}
	user, err := storeSvc.GetUserByMobile(context.Background(), tenantID, "0912345678")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if !user.IsVerified || !user.IsPasswordOTP || user.MainCard != "" || user.ExpDate != "" {
		t.Fatalf("user = %+v", user)
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM cards LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("identity scope should not create card tables, err=%v", err)
	}
}

func TestRegisterWithCardIdentityUsesIdentityScope(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeIdentityAuth})
	smsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(smsServer.Close)
	service := &Service{
		Store: storeSvc,
		NoebsConfig: ebs_fields.NoebsConfig{
			SMSGateway: smsServer.URL + "?",
			SMSAPIKey:  "test-key",
			SMSSender:  "noebs",
		},
	}

	result, err := service.RegisterWithCardIdentity(context.Background(), tenantID, RegisterWithCardIdentityCommand{
		Mobile:    "0912345678",
		Password:  "local-password",
		PublicKey: "pubkey",
		Fullname:  "Test User",
	})
	if err != nil {
		t.Fatalf("register with card identity: %v", err)
	}
	if result.UserID <= 0 {
		t.Fatalf("user ID = %d", result.UserID)
	}
	user, err := storeSvc.GetUserByMobile(context.Background(), tenantID, "0912345678")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if user.Fullname != "Test User" || user.PublicKey != "pubkey" || user.MainCard != "" || user.ExpDate != "" {
		t.Fatalf("user = %+v", user)
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM cards LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("identity scope should not create card tables, err=%v", err)
	}
}

func TestLegacyCompletedRegistrationCardFailsClosedInCardVault(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeCardVault})
	service := &Service{Store: storeSvc}
	ctx := context.Background()
	err := service.StoreCompletedRegistrationCard(ctx, tenantID, CompletedRegistrationCardCommand{
		Mobile:  "0912345678",
		UserID:  42,
		PAN:     "9222081700000000",
		ExpDate: "2601",
	})
	if !errors.Is(err, store.ErrLegacyCardOperation) {
		t.Fatalf("error = %v, want %v", err, store.ErrLegacyCardOperation)
	}

	var cards int
	if err := db.GetContext(ctx, &cards, "SELECT COUNT(*) FROM cards"); err != nil {
		t.Fatalf("count cards: %v", err)
	}
	if cards != 0 {
		t.Fatalf("legacy registration mutated %d cards", cards)
	}
	var cacheCardsAbsent bool
	if err := db.GetContext(ctx, &cacheCardsAbsent, "SELECT to_regclass('cache_cards') IS NULL"); err != nil {
		t.Fatalf("inspect legacy cache table: %v", err)
	}
	if !cacheCardsAbsent {
		t.Fatal("legacy cache_cards table remains available")
	}
	var identityTableExists bool
	if err := db.GetContext(ctx, &identityTableExists, "SELECT to_regclass('users') IS NOT NULL"); err != nil {
		t.Fatalf("inspect identity table: %v", err)
	}
	if identityTableExists {
		t.Fatal("card-vault scope created identity tables")
	}
}

func TestCompletedRegistrationCommandsRejectMissingInputs(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	_, err := service.CreateCompletedRegistrationIdentity(context.Background(), "", CompletedRegistrationIdentityCommand{})
	if !errors.Is(err, store.ErrMissingTenantID) {
		t.Fatalf("identity missing tenant error = %v", err)
	}
	if err := service.StoreCompletedRegistrationCard(context.Background(), "tenant-a", CompletedRegistrationCardCommand{}); !errors.Is(err, store.ErrInvalidUserID) {
		t.Fatalf("card-vault missing user error = %v", err)
	}
	if err := service.StoreCompletedRegistrationCard(context.Background(), "tenant-a", CompletedRegistrationCardCommand{UserID: 42}); !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("card-vault missing mobile error = %v", err)
	}
	if _, err := service.RegisterWithCardIdentity(context.Background(), "tenant-a", RegisterWithCardIdentityCommand{}); !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("register with card identity missing mobile error = %v", err)
	}
	if _, err := service.RegisterWithCardIdentity(context.Background(), "tenant-a", RegisterWithCardIdentityCommand{Mobile: "0912345678"}); !errors.Is(err, ErrMissingPassword) {
		t.Fatalf("register with card identity missing password error = %v", err)
	}
	if _, err := service.RegisterWithCardIdentity(context.Background(), "tenant-a", RegisterWithCardIdentityCommand{Mobile: "0912345678", Password: "local-password"}); !errors.Is(err, ErrMissingPublicKey) {
		t.Fatalf("register with card identity missing public key error = %v", err)
	}
}

func assertAdminCommandHeaders(t *testing.T, r *http.Request, tenantID string) {
	t.Helper()
	if r.Header.Get(gateway.GatewayTenantIDHeader) != tenantID {
		t.Fatalf("tenant header = %q", r.Header.Get(gateway.GatewayTenantIDHeader))
	}
	if r.Header.Get("X-Tenant-ID") != "" {
		t.Fatalf("public tenant header forwarded = %q", r.Header.Get("X-Tenant-ID"))
	}
	if r.Header.Get(gateway.GatewayAdminIdentityHeader) != gateway.GatewayAdminIdentityValue {
		t.Fatalf("admin identity header = %q", r.Header.Get(gateway.GatewayAdminIdentityHeader))
	}
	if r.Header.Get(gateway.GatewayAdminRoleHeader) != gateway.GatewayAdminRoleValue {
		t.Fatalf("admin role header = %q", r.Header.Get(gateway.GatewayAdminRoleHeader))
	}
}

func readBodyForTest(t *testing.T, r *http.Request) []byte {
	t.Helper()
	body := new(bytes.Buffer)
	if _, err := body.ReadFrom(r.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return body.Bytes()
}
