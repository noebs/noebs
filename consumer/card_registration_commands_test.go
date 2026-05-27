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
		var cmd CompletedRegistrationIdentityCommand
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			t.Fatalf("decode identity command: %v", err)
		}
		if cmd.Mobile != "0912345678" || cmd.Password != "local-password" || cmd.PAN != "9222081700000000" || cmd.ExpDate != "2601" {
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
		if cmd.Transaction == nil || cmd.Transaction.UUID != "complete-registration-uuid" {
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
			ServiceDiscovery: map[string]string{
				cardVaultServiceDiscoveryKey:      cardVaultServer.URL,
				identityAuthServiceDiscoveryKey:   identityServer.URL,
				adminReportingServiceDiscoveryKey: adminReportingServer.URL,
			},
		},
	}
	res, err := service.CompleteRegistration(context.Background(), tenantID, ebs_fields.ConsumerCompleteRegistrationFields{
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
	if !sawEBS || !sawIdentity || !sawCardVault || !sawAdminReporting {
		t.Fatalf("sawEBS=%v sawIdentity=%v sawCardVault=%v sawAdminReporting=%v", sawEBS, sawIdentity, sawCardVault, sawAdminReporting)
	}
}

func TestCreateCompletedRegistrationIdentityUsesIdentityScope(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeIdentityAuth})
	service := &Service{Store: storeSvc}
	result, err := service.CreateCompletedRegistrationIdentity(context.Background(), tenantID, CompletedRegistrationIdentityCommand{
		Mobile:   "0912345678",
		Password: "local-password",
		PAN:      "9222081700000000",
		ExpDate:  "2601",
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
	if !user.IsVerified || !user.IsPasswordOTP || user.MainCard != "9222081700000000" || user.ExpDate != "2601" {
		t.Fatalf("user = %+v", user)
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM cards LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("identity scope should not create card tables, err=%v", err)
	}
}

func TestStoreCompletedRegistrationCardUsesCardVaultScope(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeCardVault})
	service := &Service{Store: storeSvc}
	if err := service.StoreCompletedRegistrationCard(context.Background(), tenantID, CompletedRegistrationCardCommand{
		Mobile:  "0912345678",
		UserID:  42,
		PAN:     "9222081700000000",
		ExpDate: "2601",
	}); err != nil {
		t.Fatalf("store completed registration card: %v", err)
	}
	cards, err := storeSvc.ListCardsByUserID(context.Background(), tenantID, 42)
	if err != nil {
		t.Fatalf("list cards: %v", err)
	}
	if len(cards) != 1 || cards[0].Mobile != "0912345678" || cards[0].Pan != "9222081700000000" || !cards[0].IsMain {
		t.Fatalf("cards = %+v", cards)
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM users LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("card-vault scope should not create user tables, err=%v", err)
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
}

func assertAdminCommandHeaders(t *testing.T, r *http.Request, tenantID string) {
	t.Helper()
	if r.Header.Get(internalTenantIDHeader) != tenantID {
		t.Fatalf("tenant header = %q", r.Header.Get(internalTenantIDHeader))
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
