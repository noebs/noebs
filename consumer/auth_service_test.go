package consumer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/adonese/noebs/utils"
	"github.com/golang-jwt/jwt/v5"
)

type refreshAuthStub struct {
	claims          *gateway.TokenClaims
	verifyErr       error
	generated       bool
	generatedTenant string
}

func (a *refreshAuthStub) VerifyJWT(string) (*gateway.TokenClaims, error) {
	return a.claims, a.verifyErr
}

func (a *refreshAuthStub) GenerateJWT(_ int64, _ string, tenantID string) (string, error) {
	a.generated = true
	a.generatedTenant = tenantID
	return "new-token", nil
}

type oauthRoundTripFunc func(*http.Request) (*http.Response, error)

func (f oauthRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

const (
	refreshProofPublicKey = `MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAteM6IQBAUK4Lsb42zgr13YRHoBWyiQHuifjHvxxI7QHnOlQGRYU0xqgplV+Gumers6c3vH5xtlPsy6lHFJ7VQnTPHlZIcRefy7rKsVC+D1cjA6H3W6jWAdKDslxEb8sMfnatWI1PO0MNDz4Nh7KHS3V51nDqlx7I+TggtKZU8zq/epeVb+pqCKQphGd36J9KqZzaobDKxY6ObrLQDncKtF74UerJjmQxFd52VM/XDwOjmWS7shpQZx2HaLzFq6IOpTnKE+nySZqoXZVDB5j6llctinSs9E+HAOmN2r32B6zthYvMIO8gQjSZNyRp0E/GKhlPgfF8r55upszm7qIUZQIDAQAB`
	refreshProofSignature = "Xq4J7E2b7QK7mqn7YFnbnd+g1IHCTlvn8d154/CDs0rO+idvJ/e4gEpjOOpfr69EaDILnAgBudZRnAMHhGRIPEm2vCLUREinWwl5pDE0Gbee9h2OjSS26cEPE1fC626PwvizcwTHGPmguw1jYSNy74B128jsdG/RX1xAbbDBYKbJIjG3yXxzZZG/N6rGIQksJdDhgzzsgIESTrXh2JfX6iyEeArWoFJTsDm6T8tXd5/phQRlocQ18OGcnCBMM66CWC0DJhdUQfB7q/tenPYk3SMld7MS7pcWGZ92bMHXPYMzXhVgJnvUZZjkMr16Dn1YFoKvv4irUcy4Fol5z3Rhaw=="
	refreshProofMessage   = "RAMI"
)

func oauthHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestVerifyUserSignatureRequiresValidProof(t *testing.T) {
	if err := verifyUserSignature(refreshProofPublicKey, refreshProofSignature, refreshProofMessage); err != nil {
		t.Fatalf("verifyUserSignature(valid) error = %v", err)
	}

	cases := []struct {
		name      string
		publicKey string
		signature string
		message   string
	}{
		{name: "missing signature", publicKey: refreshProofPublicKey, message: refreshProofMessage},
		{name: "missing message", publicKey: refreshProofPublicKey, signature: refreshProofSignature},
		{name: "malformed public key", publicKey: "not-a-key", signature: refreshProofSignature, message: refreshProofMessage},
		{name: "invalid signature", publicKey: refreshProofPublicKey, signature: "invalid", message: refreshProofMessage},
		{name: "message mismatch", publicKey: refreshProofPublicKey, signature: refreshProofSignature, message: "DIFFERENT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyUserSignature(tc.publicKey, tc.signature, tc.message)
			if !errors.Is(err, ErrInvalidSignature) {
				t.Fatalf("verifyUserSignature() error = %v, want %v", err, ErrInvalidSignature)
			}
		})
	}
}

func TestServiceGoogleAuthRequiresHTTPClient(t *testing.T) {
	service := &Service{
		Store:       &store.Store{},
		NoebsConfig: ebs_fields.NoebsConfig{GoogleClientID: "client-id"},
	}

	_, _, _, err := service.GoogleAuth(context.Background(), "tenant-a", "code", "", "")
	if !errors.Is(err, ErrMissingHTTPClient) {
		t.Fatalf("GoogleAuth() error = %v, want %v", err, ErrMissingHTTPClient)
	}
}

func TestServiceGoogleAuthUsesConfiguredHTTPClient(t *testing.T) {
	requests := []string{}
	client := &http.Client{Transport: oauthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.URL.String())
		switch req.URL.String() {
		case googleTokenURL:
			return oauthHTTPResponse(http.StatusOK, `{"access_token":"token"}`), nil
		case googleUserURL:
			return oauthHTTPResponse(http.StatusOK, `{"email":"user@example.com"}`), nil
		default:
			return nil, fmt.Errorf("unexpected oauth request %s", req.URL.String())
		}
	})}
	service := &Service{
		Store:       &store.Store{},
		NoebsConfig: ebs_fields.NoebsConfig{GoogleClientID: "client-id"},
		HTTPClient:  client,
	}

	_, _, _, err := service.GoogleAuth(context.Background(), "tenant-a", "code", "verifier", "https://app.example/callback")
	if err == nil || err.Error() != "invalid_userinfo" {
		t.Fatalf("GoogleAuth() error = %v, want invalid_userinfo after configured client requests", err)
	}
	want := []string{googleTokenURL, googleUserURL}
	if fmt.Sprint(requests) != fmt.Sprint(want) {
		t.Fatalf("oauth requests = %v, want %v", requests, want)
	}
}

func TestFindOrCreateUserFromGoogleCreatesUserAndAuthAccount(t *testing.T) {
	ctx := context.Background()
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeIdentityAuth})
	service := &Service{Store: storeSvc}

	user, isNew, err := service.findOrCreateUserFromGoogle(ctx, tenantID, googleUserInfo{
		Sub:           "google-sub-1",
		Email:         "google-user@example.test",
		EmailVerified: true,
		Name:          "Google User",
	})
	if err != nil {
		t.Fatalf("findOrCreateUserFromGoogle(): %v", err)
	}
	if !isNew {
		t.Fatal("isNew = false, want true")
	}
	if user.ID <= 0 {
		t.Fatalf("user id = %d, want persisted id", user.ID)
	}
	account, err := storeSvc.FindAuthAccount(ctx, tenantID, googleProvider, "google-sub-1")
	if err != nil {
		t.Fatalf("FindAuthAccount(): %v", err)
	}
	if account.UserID != user.ID {
		t.Fatalf("account user id = %d, want %d", account.UserID, user.ID)
	}
}

func TestFindOrCreateUserFromGoogleLinksExistingEmail(t *testing.T) {
	ctx := context.Background()
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeIdentityAuth})
	service := &Service{Store: storeSvc}
	existing := ebs_fields.User{
		Mobile:   "0990000000",
		Username: "0990000000",
		Email:    "existing-google@example.test",
		Password: "hashed-password",
	}
	if err := storeSvc.CreateUser(ctx, tenantID, &existing); err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}

	user, isNew, err := service.findOrCreateUserFromGoogle(ctx, tenantID, googleUserInfo{
		Sub:           "google-sub-existing",
		Email:         " EXISTING-GOOGLE@example.test ",
		EmailVerified: true,
		Name:          "Existing User",
	})
	if err != nil {
		t.Fatalf("findOrCreateUserFromGoogle(): %v", err)
	}
	if isNew {
		t.Fatal("isNew = true, want false")
	}
	if user.ID != existing.ID {
		t.Fatalf("user id = %d, want existing id %d", user.ID, existing.ID)
	}
	account, err := storeSvc.FindAuthAccount(ctx, tenantID, googleProvider, "google-sub-existing")
	if err != nil {
		t.Fatalf("FindAuthAccount(): %v", err)
	}
	if account.UserID != existing.ID {
		t.Fatalf("account user id = %d, want %d", account.UserID, existing.ID)
	}
}

func TestServiceRefreshJWTRequiresTenantClaim(t *testing.T) {
	tests := []struct {
		name      string
		tenantID  string
		verifyErr error
		wantErr   error
	}{
		{name: "valid token missing tenant", wantErr: store.ErrMissingTenantID},
		{name: "expired token missing tenant", verifyErr: jwt.ErrTokenExpired, wantErr: store.ErrMissingTenantID},
		{name: "valid token reserved tenant", tenantID: "default", wantErr: store.ErrInvalidTenantID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &refreshAuthStub{
				claims:    &gateway.TokenClaims{UserID: 42, Mobile: "0990000000", TenantID: tt.tenantID},
				verifyErr: tt.verifyErr,
			}
			service := &Service{Store: &store.Store{}, Auth: auth}

			_, err := service.RefreshJWT(context.Background(), gateway.Token{JWT: "old-token"})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RefreshJWT() error = %v, want %v", err, tt.wantErr)
			}
			if auth.generated {
				t.Fatalf("RefreshJWT() generated a token with tenant %q", auth.generatedTenant)
			}
		})
	}
}

func TestAuthServiceTenantValidationFailsBeforeDB(t *testing.T) {
	service := &Service{
		Store: &store.Store{},
		Auth:  &refreshAuthStub{},
	}
	ctx := context.Background()
	cases := []struct {
		name string
		run  func(string) error
	}{
		{"GenerateAPIKey", func(tenantID string) error {
			_, err := service.GenerateAPIKey(ctx, tenantID, "user@example.test")
			return err
		}},
		{"Login", func(tenantID string) error {
			_, _, err := service.Login(ctx, tenantID, "0990000000", "password")
			return err
		}},
		{"SingleLogin", func(tenantID string) error {
			_, _, err := service.SingleLogin(ctx, tenantID, gateway.Token{Mobile: "0990000000"})
			return err
		}},
		{"CreateUser", func(tenantID string) error {
			_, err := service.CreateUser(ctx, tenantID, ebs_fields.User{Mobile: "0990000000", Password: "password"})
			return err
		}},
		{"VerifyOTP", func(tenantID string) error {
			_, err := service.VerifyOTP(ctx, tenantID, "0990000000", "123456")
			return err
		}},
		{"ChangePassword", func(tenantID string) error {
			_, err := service.ChangePassword(ctx, tenantID, "0990000000", "new-password")
			return err
		}},
		{"GenerateSignInCode", func(tenantID string) error {
			return service.GenerateSignInCode(ctx, tenantID, "0990000000")
		}},
	}
	tenantCases := []struct {
		tenantID string
		wantErr  error
	}{
		{"", store.ErrMissingTenantID},
		{"default", store.ErrInvalidTenantID},
	}
	for _, tc := range cases {
		for _, tenantCase := range tenantCases {
			t.Run(tc.name+"/"+tenantCase.tenantID, func(t *testing.T) {
				err := tc.run(tenantCase.tenantID)
				if !errors.Is(err, tenantCase.wantErr) {
					t.Fatalf("expected %v, got %v", tenantCase.wantErr, err)
				}
			})
		}
	}
}

func TestCreateUserRequiresMobileBeforeStore(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	_, err := service.CreateUser(context.Background(), "tenant", ebs_fields.User{Mobile: " ", Password: "password"})
	if !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("CreateUser(missing mobile) error = %v, want %v", err, ErrMissingMobile)
	}
}

func TestChangePasswordRequiresExplicitInputsBeforeStore(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	ctx := context.Background()

	if _, err := service.ChangePassword(ctx, "tenant", " ", "new-password"); !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("ChangePassword(missing mobile) error = %v, want %v", err, ErrMissingMobile)
	}
	if _, err := service.ChangePassword(ctx, "tenant", "0990000000", " "); !errors.Is(err, ErrMissingPassword) {
		t.Fatalf("ChangePassword(missing password) error = %v, want %v", err, ErrMissingPassword)
	}
}

func TestCreateUserPropagatesUniquenessLookupErrors(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	_, err := service.CreateUser(context.Background(), "tenant", ebs_fields.User{
		Mobile:   "0990000000",
		Password: "short",
	})
	if err == nil {
		t.Fatal("CreateUser() error = nil, want store lookup error")
	}
	if errors.Is(err, ErrPasswordInvalid) {
		t.Fatalf("CreateUser() error = %v, want uniqueness lookup error before password validation", err)
	}
	if !strings.Contains(err.Error(), "nil db") {
		t.Fatalf("CreateUser() error = %v, want nil db lookup error", err)
	}
}

func TestGenerateSignInCodeRecordsLoginAttempt(t *testing.T) {
	env := newTestEnv(t)
	user := seedUser(t, env.Store, env.Tenant, "0990000000", "password")
	if err := env.Store.UpdateUserColumns(context.Background(), env.Tenant, user.ID, map[string]any{"public_key": "otp-public-key"}); err != nil {
		t.Fatalf("set public key: %v", err)
	}

	if err := env.Service.GenerateSignInCode(context.Background(), env.Tenant, "0990000000"); err != nil {
		t.Fatalf("GenerateSignInCode(): %v", err)
	}

	loginCount, suspiciousCount := readLoginMetric(t, env, "0990000000")
	if loginCount != 1 {
		t.Fatalf("login count = %d, want 1", loginCount)
	}
	if suspiciousCount != 0 {
		t.Fatalf("suspicious count = %d, want 0", suspiciousCount)
	}
}

func TestGenerateSignInCodeReturnsSMSError(t *testing.T) {
	env := newTestEnv(t)
	user := seedUser(t, env.Store, env.Tenant, "0990000000", "password")
	if err := env.Store.UpdateUserColumns(context.Background(), env.Tenant, user.ID, map[string]any{"public_key": "otp-public-key"}); err != nil {
		t.Fatalf("set public key: %v", err)
	}

	smsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(smsServer.Close)
	env.Service.NoebsConfig.SMSGateway = smsServer.URL + "?"

	err := env.Service.GenerateSignInCode(context.Background(), env.Tenant, "0990000000")
	if !errors.Is(err, utils.ErrSMSDeliveryFailed) {
		t.Fatalf("GenerateSignInCode() error = %v, want %v", err, utils.ErrSMSDeliveryFailed)
	}
}

func TestVerifyOTPMarksUserVerified(t *testing.T) {
	env := newTestEnv(t)
	user := seedUser(t, env.Store, env.Tenant, "0990000000", "password")
	if err := env.Store.UpdateUserColumns(context.Background(), env.Tenant, user.ID, map[string]any{"public_key": "otp-public-key"}); err != nil {
		t.Fatalf("set public key: %v", err)
	}
	stored, err := env.Store.GetUserByMobile(context.Background(), env.Tenant, "0990000000")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	code, err := stored.GenerateOtp()
	if err != nil {
		t.Fatalf("generate otp: %v", err)
	}

	verified, err := env.Service.VerifyOTP(context.Background(), env.Tenant, "0990000000", code)
	if err != nil {
		t.Fatalf("VerifyOTP(): %v", err)
	}
	if !verified.IsVerified || !verified.IsPasswordOTP {
		t.Fatalf("verified user flags = verified:%v password_otp:%v", verified.IsVerified, verified.IsPasswordOTP)
	}

	var isVerified bool
	var isPasswordOTP bool
	stmt := env.DB.Rebind("SELECT is_verified, is_password_otp FROM users WHERE tenant_id = ? AND id = ?")
	if err := env.DB.QueryRowContext(context.Background(), stmt, env.Tenant, user.ID).Scan(&isVerified, &isPasswordOTP); err != nil {
		t.Fatalf("read user flags: %v", err)
	}
	if !isVerified || !isPasswordOTP {
		t.Fatalf("stored flags = verified:%v password_otp:%v", isVerified, isPasswordOTP)
	}
}

func TestVerifyOTPInvalidCodeIncrementsSuspiciousMetric(t *testing.T) {
	env := newTestEnv(t)
	user := seedUser(t, env.Store, env.Tenant, "0990000000", "password")
	if err := env.Store.UpdateUserColumns(context.Background(), env.Tenant, user.ID, map[string]any{"public_key": "otp-public-key"}); err != nil {
		t.Fatalf("set public key: %v", err)
	}

	_, err := env.Service.VerifyOTP(context.Background(), env.Tenant, "0990000000", "not-otp")
	if !errors.Is(err, ErrInvalidOTP) {
		t.Fatalf("VerifyOTP() error = %v, want %v", err, ErrInvalidOTP)
	}

	loginCount, suspiciousCount := readLoginMetric(t, env, "0990000000")
	if loginCount != 0 {
		t.Fatalf("login count = %d, want 0", loginCount)
	}
	if suspiciousCount != 1 {
		t.Fatalf("suspicious count = %d, want 1", suspiciousCount)
	}
}

func readLoginMetric(t *testing.T, env *testEnv, mobile string) (int, int) {
	t.Helper()
	var loginCount int
	var suspiciousCount int
	stmt := env.DB.Rebind("SELECT login_count, suspicious_count FROM login_metrics WHERE tenant_id = ? AND mobile = ?")
	if err := env.DB.QueryRowContext(context.Background(), stmt, env.Tenant, mobile).Scan(&loginCount, &suspiciousCount); err != nil {
		t.Fatalf("read login metric: %v", err)
	}
	return loginCount, suspiciousCount
}

func TestServiceRefreshJWTRequiresSignatureProofForValidToken(t *testing.T) {
	env := newTestEnv(t)
	user := seedUser(t, env.Store, env.Tenant, "0990000000", "password")
	if err := env.Store.UpdateUserColumns(context.Background(), env.Tenant, user.ID, map[string]any{"public_key": refreshProofPublicKey}); err != nil {
		t.Fatalf("set public key: %v", err)
	}
	token, err := env.Auth.GenerateJWT(user.ID, user.Mobile, env.Tenant)
	if err != nil {
		t.Fatalf("generate jwt: %v", err)
	}

	_, err = env.Service.RefreshJWT(context.Background(), gateway.Token{JWT: token})
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("RefreshJWT() error = %v, want %v", err, ErrInvalidSignature)
	}
}

func TestServiceRefreshJWTUsesClaimTenant(t *testing.T) {
	env := newTestEnv(t)
	user := seedUser(t, env.Store, env.Tenant, "0990000000", "password")
	if err := env.Store.UpdateUserColumns(context.Background(), env.Tenant, user.ID, map[string]any{"public_key": refreshProofPublicKey}); err != nil {
		t.Fatalf("set public key: %v", err)
	}
	oldToken, err := env.Auth.GenerateJWT(user.ID, user.Mobile, env.Tenant)
	if err != nil {
		t.Fatalf("generate jwt: %v", err)
	}

	token, err := env.Service.RefreshJWT(context.Background(), gateway.Token{
		JWT:       oldToken,
		Signature: refreshProofSignature,
		Message:   refreshProofMessage,
	})
	if err != nil {
		t.Fatalf("RefreshJWT() error = %v", err)
	}
	if token == "" {
		t.Fatal("RefreshJWT() token is empty")
	}
	claims, err := env.Auth.VerifyJWT(token)
	if err != nil {
		t.Fatalf("VerifyJWT(refreshed): %v", err)
	}
	if claims.TenantID != env.Tenant {
		t.Fatalf("RefreshJWT() generated tenant = %q, want %q", claims.TenantID, env.Tenant)
	}
	if claims.UserID != user.ID || claims.Mobile != user.Mobile {
		t.Fatalf("RefreshJWT() claims = %+v, want user id %d mobile %q", claims, user.ID, user.Mobile)
	}
}
