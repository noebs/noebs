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

func TestService_RegisterWithCardUsesEBSIdentityAndCardVaultScopes(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeEBSAdapter})

	var sawEBS bool
	ebsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+ebs_fields.ConsumerBalanceEndpoint {
			t.Fatalf("EBS path = %s", r.URL.Path)
		}
		body := readBodyForTest(t, r)
		for _, forbidden := range [][]byte{[]byte(`"password"`), []byte(`"mobile"`), []byte(`"user_pubkey"`), []byte(`"public_key"`)} {
			if bytes.Contains(body, forbidden) {
				t.Fatalf("EBS request leaked identity data %s: %s", forbidden, body)
			}
		}
		if !bytes.Contains(body, []byte(`"PAN":"23232323"`)) || !bytes.Contains(body, []byte(`"expDate":"2901"`)) {
			t.Fatalf("EBS request missing card data: %s", body)
		}
		sawEBS = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ebs_fields.EBSParserFields{
			EBSResponse: ebs_fields.EBSResponse{
				UUID:            "register-with-card-balance",
				ResponseCode:    0,
				ResponseMessage: "Success",
			},
		})
	}))
	t.Cleanup(ebsServer.Close)

	var sawIdentity bool
	identityServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/identity-auth/register-with-card/users" {
			t.Fatalf("identity path = %s", r.URL.Path)
		}
		assertAdminCommandHeaders(t, r, tenantID)
		body := readBodyForTest(t, r)
		for _, forbidden := range [][]byte{[]byte(`"pan"`), []byte(`"expDate"`), []byte(`"exp_date"`)} {
			if bytes.Contains(body, forbidden) {
				t.Fatalf("identity command leaked card data %s: %s", forbidden, body)
			}
		}
		var cmd RegisterWithCardIdentityCommand
		if err := json.Unmarshal(body, &cmd); err != nil {
			t.Fatalf("decode identity command: %v", err)
		}
		if cmd.Mobile != "0912141660" || cmd.Password != "me@Suckit1" || cmd.PublicKey != "pubkey" || cmd.Fullname != "Test User" {
			t.Fatalf("identity command = %+v", cmd)
		}
		sawIdentity = true
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RegisterWithCardIdentityResult{UserID: 42})
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
		if cmd.Mobile != "0912141660" || cmd.UserID != 42 || cmd.PAN != "23232323" || cmd.ExpDate != "2901" {
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
			ConsumerID:            "consumer-app",
			EBSConsumerKey:        "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA4Jj+8WL5ANXllkz9lkOKRmXnDzQ+yS/VFKxKttkk4o5duJPPFZzJ0E3/m1F6xqEVPH2aM2IpSKN/SgeBv9NL6y+qgms7GbpnQ8MCilLIFWNGuTeRzDNVIR7yIqQ0jHX3dgrJyiDp02LQnQtMTRhzOYDZnwOnweixwEzAk8yPEeXQyzp867rUsLZ4jIIChRcI06UTFdMQrd7KZReTt5hunjQLH+qJBaMj1yAQGmf9C10MeC3Nnp4oE7m0OuTkTvekHnsaAtyY+TFg/UBvMQOyp9uJG6OwdvV6doI3MmXg16K6WJx1J1xewG6e28Tvt13z5mEljj8dnWQcqmhuASRlZwIDAQAB",
			BillInquiryIPIN:       "0000",
			KafkaTransactionTopic: testKafkaTransactionTopic,
			ServiceDiscovery: map[string]string{
				cardVaultServiceDiscoveryKey:    cardVaultServer.URL,
				identityAuthServiceDiscoveryKey: identityServer.URL,
			},
		},
	}

	err := service.RegisterWithCard(context.Background(), tenantID, ebs_fields.CacheCards{
		Pan:       "23232323",
		Expiry:    "2901",
		Mobile:    "0912141660",
		Password:  "me@Suckit1",
		PublicKey: "pubkey",
		Name:      "Test User",
	})
	if err != nil {
		t.Fatalf("register with card: %v", err)
	}
	if !sawEBS || !sawIdentity || !sawCardVault {
		t.Fatalf("sawEBS=%v sawIdentity=%v sawCardVault=%v", sawEBS, sawIdentity, sawCardVault)
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM users LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("ebs-adapter scope should not create user tables, err=%v", err)
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM cards LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("ebs-adapter scope should not create card tables, err=%v", err)
	}
}

func TestService_CreateUser(t *testing.T) {
	env := newTestEnv(t)

	user, err := env.Service.CreateUser(context.Background(), env.Tenant, ebs_fields.User{
		Mobile:   "0912141660",
		Username: "0912141660",
		Password: "me@Suckit1",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if user.ID == 0 {
		t.Fatalf("expected created user id to be set")
	}
}

func TestService_LoginHandler(t *testing.T) {
	env := newTestEnv(t)
	seedUser(t, env.Store, env.Tenant, "0912141660", "me@Suckit1")

	token, _, err := env.Service.Login(context.Background(), env.Tenant, "0912141660", "me@Suckit1", authTestSource, authTestNow)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if token == "" {
		t.Fatal("expected token to be set")
	}
	claims, err := env.Auth.VerifyJWT(token)
	if err != nil {
		t.Fatalf("verify jwt: %v", err)
	}
	if claims.Mobile != "0912141660" {
		t.Fatalf("expected jwt mobile to be %q, got %q", "0912141660", claims.Mobile)
	}
}
