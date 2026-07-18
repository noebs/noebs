package consumer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestResolveIdentityUserByMobileUsesIdentityScope(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeIdentityAuth})
	seedUser(t, storeSvc, tenantID, "0912141660", "My$Passw0rd!")
	service := &Service{Store: storeSvc}

	result, err := service.ResolveIdentityUserByMobile(context.Background(), tenantID, IdentityUserByMobileCommand{Mobile: "0912141660"})
	if err != nil {
		t.Fatalf("resolve identity user: %v", err)
	}
	if result.UserID <= 0 || result.Mobile != "0912141660" {
		t.Fatalf("identity result = %+v", result)
	}
}

func TestIssueRecoveryCredentialUsesIdentityScope(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeIdentityAuth})
	user := seedUser(t, storeSvc, tenantID, "0912141660", "My$Passw0rd!")
	if err := storeSvc.SetUserVerified(context.Background(), tenantID, user.ID, true); err != nil {
		t.Fatalf("verify user: %v", err)
	}
	auth := &gateway.JWTAuth{NoebsConfig: ebs_fields.NoebsConfig{JWTKey: "test-secret"}}
	auth.Init()
	service := &Service{Store: storeSvc, Auth: auth}

	result, err := service.IssueRecoveryCredential(context.Background(), tenantID, RecoveryCredentialCommand{UserID: user.ID, Mobile: user.Mobile}, authTestNow)
	if err != nil {
		t.Fatalf("issue recovery credential: %v", err)
	}
	if result.RecoveryCredential == "" || result.ExpiresIn != 600 {
		t.Fatalf("credential = %+v", result)
	}
	if claims, err := auth.VerifyJWT(result.RecoveryCredential); err == nil || claims != nil {
		t.Fatalf("recovery credential was a JWT: claims=%+v err=%v", claims, err)
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM cards LIMIT 1"); err == nil {
		t.Fatalf("identity-auth scope should not create card tables")
	}
}

func newIdentityUserByMobileServer(t *testing.T, tenantID, mobile string, userID int64) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/identity-auth/users/by-mobile" {
			t.Fatalf("identity path = %s", r.URL.Path)
		}
		assertAdminCommandHeaders(t, r, tenantID)
		var cmd IdentityUserByMobileCommand
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
			t.Fatalf("decode identity command: %v", err)
		}
		if cmd.Mobile != mobile {
			t.Fatalf("identity mobile = %q, want %q", cmd.Mobile, mobile)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(IdentityUserByMobileResult{UserID: userID, Mobile: mobile})
	}))
	t.Cleanup(server.Close)
	return server
}
