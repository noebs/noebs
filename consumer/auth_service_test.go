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

func oauthHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
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

func TestServiceRefreshJWTUsesClaimTenant(t *testing.T) {
	auth := &refreshAuthStub{
		claims: &gateway.TokenClaims{UserID: 42, Mobile: "0990000000", TenantID: "tenant-a"},
	}
	service := &Service{Store: &store.Store{}, Auth: auth}

	token, err := service.RefreshJWT(context.Background(), gateway.Token{JWT: "old-token"})
	if err != nil {
		t.Fatalf("RefreshJWT() error = %v", err)
	}
	if token != "new-token" {
		t.Fatalf("RefreshJWT() token = %q, want %q", token, "new-token")
	}
	if auth.generatedTenant != "tenant-a" {
		t.Fatalf("RefreshJWT() generated tenant = %q, want %q", auth.generatedTenant, "tenant-a")
	}
}
