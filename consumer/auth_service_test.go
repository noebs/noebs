package consumer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/adonese/noebs/utils"
	"github.com/golang-jwt/jwt/v5"
)

var authTestNow = time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)

const authTestSource = "192.0.2.10"

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

			_, err := service.RefreshJWT(context.Background(), "tenant-a", gateway.Token{JWT: "old-token"}, authTestSource, authTestNow)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RefreshJWT() error = %v, want %v", err, tt.wantErr)
			}
			if auth.generated {
				t.Fatalf("RefreshJWT() generated a token with tenant %q", auth.generatedTenant)
			}
		})
	}
}

func TestServiceRefreshJWTRequiresUserIDClaimBeforeStore(t *testing.T) {
	auth := &refreshAuthStub{
		claims: &gateway.TokenClaims{Mobile: "0990000000", TenantID: "tenant-a"},
	}
	service := &Service{Store: &store.Store{}, Auth: auth}

	_, err := service.RefreshJWT(context.Background(), "tenant-a", gateway.Token{JWT: "old-token"}, authTestSource, authTestNow)
	if !errors.Is(err, store.ErrInvalidUserID) {
		t.Fatalf("RefreshJWT() error = %v, want %v", err, store.ErrInvalidUserID)
	}
	if auth.generated {
		t.Fatalf("RefreshJWT() generated a token with tenant %q", auth.generatedTenant)
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
			_, _, err := service.Login(ctx, tenantID, "0990000000", "password", authTestSource, authTestNow)
			return err
		}},
		{"SingleLogin", func(tenantID string) error {
			_, _, err := service.SingleLogin(ctx, tenantID, gateway.Token{Mobile: "0990000000"}, authTestSource, authTestNow)
			return err
		}},
		{"CreateUser", func(tenantID string) error {
			_, err := service.CreateUser(ctx, tenantID, ebs_fields.User{Mobile: "0990000000", Password: "password"}, authTestSource, authTestNow)
			return err
		}},
		{"VerifyOTP", func(tenantID string) error {
			_, err := service.VerifyOTP(ctx, tenantID, "0990000000", "123456", authTestSource, authTestNow)
			return err
		}},
		{"ChangePassword", func(tenantID string) error {
			_, err := service.ChangePassword(ctx, tenantID, "0990000000", "new-password")
			return err
		}},
		{"GenerateSignInCode", func(tenantID string) error {
			return service.GenerateSignInCode(ctx, tenantID, "0990000000", authTestSource, authTestNow)
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
	_, err := service.CreateUser(context.Background(), "tenant", ebs_fields.User{Mobile: " ", Password: "password"}, authTestSource, authTestNow)
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
	if _, err := service.ChangePassword(ctx, "tenant", "0990000000", "all-lowercase1!"); !errors.Is(err, ErrPasswordInvalid) {
		t.Fatalf("ChangePassword(weak password) error = %v, want %v", err, ErrPasswordInvalid)
	}
}

func TestCreateUserValidatesCredentialsBeforeStore(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	_, err := service.CreateUser(context.Background(), "tenant", ebs_fields.User{
		Mobile:   "0990000000",
		Password: "short",
	}, authTestSource, authTestNow)
	if !errors.Is(err, ErrPasswordInvalid) {
		t.Fatalf("CreateUser(weak password) error = %v, want %v", err, ErrPasswordInvalid)
	}

	for _, tc := range []struct {
		name      string
		publicKey string
		wantErr   error
	}{
		{name: "missing", wantErr: ErrMissingPublicKey},
		{name: "malformed", publicKey: "not-a-public-key", wantErr: ErrInvalidPublicKey},
	} {
		t.Run(tc.name+"_public_key", func(t *testing.T) {
			_, err := service.CreateUser(context.Background(), "tenant", ebs_fields.User{
				Mobile:    "0990000000",
				Password:  "Valid1!Password",
				PublicKey: tc.publicKey,
			}, authTestSource, authTestNow)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("CreateUser() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCreateUserEnforcesPersistentMobileAndSourceLimits(t *testing.T) {
	t.Run("mobile", func(t *testing.T) {
		env := newTestEnv(t)
		user := ebs_fields.User{Mobile: "0990000000", Password: "Valid1!Password", PublicKey: refreshProofPublicKey}
		for attempt := 1; attempt <= 4; attempt++ {
			_, err := env.Service.CreateUser(context.Background(), env.Tenant, user, authTestSource, authTestNow)
			if attempt < 4 && errors.Is(err, ErrRateLimited) {
				t.Fatalf("attempt %d was rate limited early", attempt)
			}
			if attempt == 4 && !errors.Is(err, ErrRateLimited) {
				t.Fatalf("attempt %d error = %v, want %v", attempt, err, ErrRateLimited)
			}
		}
	})

	t.Run("source", func(t *testing.T) {
		env := newTestEnv(t)
		for attempt := 1; attempt <= 11; attempt++ {
			user := ebs_fields.User{
				Mobile:    fmt.Sprintf("099100%04d", attempt),
				Email:     fmt.Sprintf("registration-%d@example.test", attempt),
				Password:  "Valid1!Password",
				PublicKey: refreshProofPublicKey,
			}
			_, err := env.Service.CreateUser(context.Background(), env.Tenant, user, authTestSource, authTestNow)
			if attempt <= 10 && err != nil {
				t.Fatalf("attempt %d: %v", attempt, err)
			}
			if attempt == 11 && !errors.Is(err, ErrRateLimited) {
				t.Fatalf("attempt %d error = %v, want %v", attempt, err, ErrRateLimited)
			}
		}
	})
}

func TestGenerateSignInCodeRecordsLoginAttempt(t *testing.T) {
	env := newTestEnv(t)
	user := seedUser(t, env.Store, env.Tenant, "0990000000", "password")
	if err := env.Store.UpdateUserColumns(context.Background(), env.Tenant, user.ID, map[string]any{"public_key": "otp-public-key"}); err != nil {
		t.Fatalf("set public key: %v", err)
	}

	if err := env.Service.GenerateSignInCode(context.Background(), env.Tenant, "0990000000", authTestSource, authTestNow); err != nil {
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

	err := env.Service.GenerateSignInCode(context.Background(), env.Tenant, "0990000000", authTestSource, authTestNow)
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
	code := "123456"
	digest, err := env.Service.otpDigest(env.Tenant, "0990000000", code)
	if err != nil {
		t.Fatalf("otp digest: %v", err)
	}
	if err := env.Store.StoreOTPChallenge(context.Background(), env.Tenant, "0990000000", digest, authTestNow, authTestNow.Add(otpChallengeTTL), otpMaxAttempts); err != nil {
		t.Fatalf("store otp challenge: %v", err)
	}

	verified, err := env.Service.VerifyOTP(context.Background(), env.Tenant, "0990000000", code, authTestSource, authTestNow)
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

func TestRegisteredUserMustVerifyOTPBeforePasswordLogin(t *testing.T) {
	env := newTestEnv(t)
	const (
		mobile   = "0992220000"
		password = "Valid1!Password"
		code     = "123456"
	)

	created, err := env.Service.CreateUser(context.Background(), env.Tenant, ebs_fields.User{
		Mobile:    mobile,
		Password:  password,
		PublicKey: refreshProofPublicKey,
	}, authTestSource, authTestNow)
	if err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}
	if created.IsVerified {
		t.Fatal("new user is verified before OTP verification")
	}
	if _, _, err := env.Service.Login(context.Background(), env.Tenant, mobile, password, authTestSource, authTestNow); !errors.Is(err, ErrUserNotVerified) {
		t.Fatalf("Login(unverified) error = %v, want %v", err, ErrUserNotVerified)
	}

	digest, err := env.Service.otpDigest(env.Tenant, mobile, code)
	if err != nil {
		t.Fatalf("otp digest: %v", err)
	}
	if err := env.Store.StoreOTPChallenge(context.Background(), env.Tenant, mobile, digest, authTestNow, authTestNow.Add(otpChallengeTTL), otpMaxAttempts); err != nil {
		t.Fatalf("store otp challenge: %v", err)
	}
	if _, err := env.Service.VerifyOTP(context.Background(), env.Tenant, mobile, code, authTestSource, authTestNow); err != nil {
		t.Fatalf("VerifyOTP(): %v", err)
	}
	if token, _, err := env.Service.Login(context.Background(), env.Tenant, mobile, password, authTestSource, authTestNow.Add(time.Second)); err != nil {
		t.Fatalf("Login(verified): %v", err)
	} else if token == "" {
		t.Fatal("Login(verified) returned an empty token")
	}
}

func TestVerifyOTPInvalidCodeIncrementsSuspiciousMetric(t *testing.T) {
	env := newTestEnv(t)
	user := seedUser(t, env.Store, env.Tenant, "0990000000", "password")
	if err := env.Store.UpdateUserColumns(context.Background(), env.Tenant, user.ID, map[string]any{"public_key": "otp-public-key"}); err != nil {
		t.Fatalf("set public key: %v", err)
	}

	digest, err := env.Service.otpDigest(env.Tenant, "0990000000", "123456")
	if err != nil {
		t.Fatalf("otp digest: %v", err)
	}
	if err := env.Store.StoreOTPChallenge(context.Background(), env.Tenant, "0990000000", digest, authTestNow, authTestNow.Add(otpChallengeTTL), otpMaxAttempts); err != nil {
		t.Fatalf("store otp challenge: %v", err)
	}

	_, err = env.Service.VerifyOTP(context.Background(), env.Tenant, "0990000000", "not-otp", authTestSource, authTestNow)
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

func TestGeneratedOTPCanBeConsumedOnlyOnce(t *testing.T) {
	env := newTestEnv(t)
	seedUser(t, env.Store, env.Tenant, "0990000000", "password")
	messages := make(chan string, 1)
	smsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		messages <- r.URL.Query().Get("sms")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(smsServer.Close)
	env.Service.NoebsConfig.SMSGateway = smsServer.URL + "?"

	if err := env.Service.GenerateSignInCode(context.Background(), env.Tenant, "0990000000", authTestSource, authTestNow); err != nil {
		t.Fatalf("GenerateSignInCode(): %v", err)
	}
	match := regexp.MustCompile(`access code is: ([0-9]{6})`).FindStringSubmatch(<-messages)
	if len(match) != 2 {
		t.Fatalf("SMS did not contain a six-digit OTP")
	}
	if _, err := env.Service.VerifyOTP(context.Background(), env.Tenant, "0990000000", match[1], authTestSource, authTestNow.Add(time.Second)); err != nil {
		t.Fatalf("VerifyOTP(first use): %v", err)
	}
	if _, err := env.Service.VerifyOTP(context.Background(), env.Tenant, "0990000000", match[1], authTestSource, authTestNow.Add(2*time.Second)); !errors.Is(err, ErrInvalidOTP) {
		t.Fatalf("VerifyOTP(replay) error = %v, want %v", err, ErrInvalidOTP)
	}
}

func TestGenerateSignInCodeEnforcesMobileCooldown(t *testing.T) {
	env := newTestEnv(t)
	seedUser(t, env.Store, env.Tenant, "0990000000", "password")
	if err := env.Service.GenerateSignInCode(context.Background(), env.Tenant, "0990000000", authTestSource, authTestNow); err != nil {
		t.Fatalf("first GenerateSignInCode(): %v", err)
	}
	err := env.Service.GenerateSignInCode(context.Background(), env.Tenant, "0990000000", authTestSource, authTestNow.Add(10*time.Second))
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("second GenerateSignInCode() error = %v, want %v", err, ErrRateLimited)
	}
	var limitErr *RateLimitError
	if !errors.As(err, &limitErr) || limitErr.RetryAfter != 50*time.Second {
		t.Fatalf("rate limit = %#v, want retry after 50s", limitErr)
	}
}

func TestSingleLoginConsumesStoredOTP(t *testing.T) {
	env := newTestEnv(t)
	user := seedUser(t, env.Store, env.Tenant, "0990000000", "password")
	if err := env.Store.UpdateUserColumns(context.Background(), env.Tenant, user.ID, map[string]any{"public_key": refreshProofPublicKey}); err != nil {
		t.Fatalf("set public key: %v", err)
	}
	digest, err := env.Service.otpDigest(env.Tenant, user.Mobile, refreshProofMessage)
	if err != nil {
		t.Fatalf("otp digest: %v", err)
	}
	if err := env.Store.StoreOTPChallenge(context.Background(), env.Tenant, user.Mobile, digest, authTestNow, authTestNow.Add(otpChallengeTTL), otpMaxAttempts); err != nil {
		t.Fatalf("store otp challenge: %v", err)
	}
	req := gateway.Token{Mobile: user.Mobile, Message: refreshProofMessage, Signature: refreshProofSignature}
	if _, _, err := env.Service.SingleLogin(context.Background(), env.Tenant, req, authTestSource, authTestNow); err != nil {
		t.Fatalf("SingleLogin(first use): %v", err)
	}
	if _, _, err := env.Service.SingleLogin(context.Background(), env.Tenant, req, authTestSource, authTestNow.Add(time.Second)); !errors.Is(err, ErrWrongOTP) {
		t.Fatalf("SingleLogin(replay) error = %v, want %v", err, ErrWrongOTP)
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
	env.Auth.Now = func() time.Time { return authTestNow }
	user := seedUser(t, env.Store, env.Tenant, "0990000000", "password")
	if err := env.Store.UpdateUserColumns(context.Background(), env.Tenant, user.ID, map[string]any{"public_key": refreshProofPublicKey}); err != nil {
		t.Fatalf("set public key: %v", err)
	}
	token, err := env.Auth.GenerateJWT(user.ID, user.Mobile, env.Tenant)
	if err != nil {
		t.Fatalf("generate jwt: %v", err)
	}

	_, err = env.Service.RefreshJWT(context.Background(), env.Tenant, gateway.Token{JWT: token}, authTestSource, authTestNow)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("RefreshJWT() error = %v, want %v", err, ErrInvalidSignature)
	}
}

func TestServiceRefreshJWTUsesClaimTenant(t *testing.T) {
	env := newTestEnv(t)
	env.Auth.Now = func() time.Time { return authTestNow }
	user := seedUser(t, env.Store, env.Tenant, "0990000000", "password")
	if err := env.Store.UpdateUserColumns(context.Background(), env.Tenant, user.ID, map[string]any{"public_key": refreshProofPublicKey}); err != nil {
		t.Fatalf("set public key: %v", err)
	}
	oldToken, err := env.Auth.GenerateJWT(user.ID, user.Mobile, env.Tenant)
	if err != nil {
		t.Fatalf("generate jwt: %v", err)
	}

	token, err := env.Service.RefreshJWT(context.Background(), env.Tenant, gateway.Token{
		JWT:       oldToken,
		Signature: refreshProofSignature,
		Message:   refreshProofMessage,
		Mobile:    user.Mobile,
	}, authTestSource, authTestNow)
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

func TestServiceRefreshJWTRotatesAndRejectsReplay(t *testing.T) {
	env := newTestEnv(t)
	env.Auth.Now = func() time.Time { return authTestNow }
	user := seedUser(t, env.Store, env.Tenant, "0990000000", "password")
	if err := env.Store.UpdateUserColumns(context.Background(), env.Tenant, user.ID, map[string]any{"public_key": refreshProofPublicKey}); err != nil {
		t.Fatalf("set public key: %v", err)
	}
	oldToken, err := env.Auth.GenerateJWT(user.ID, user.Mobile, env.Tenant)
	if err != nil {
		t.Fatalf("generate jwt: %v", err)
	}
	req := gateway.Token{JWT: oldToken, Signature: refreshProofSignature, Message: refreshProofMessage, Mobile: user.Mobile}
	newToken, err := env.Service.RefreshJWT(context.Background(), env.Tenant, req, authTestSource, authTestNow)
	if err != nil {
		t.Fatalf("RefreshJWT(first use): %v", err)
	}
	if newToken == oldToken {
		t.Fatal("RefreshJWT() returned the presented token")
	}
	if _, err := env.Service.RefreshJWT(context.Background(), env.Tenant, req, authTestSource, authTestNow.Add(time.Second)); !errors.Is(err, ErrRefreshReplay) {
		t.Fatalf("RefreshJWT(replay) error = %v, want %v", err, ErrRefreshReplay)
	}
}

func TestServiceRefreshJWTRejectsExpiredRefreshWindowBeforeStore(t *testing.T) {
	auth := &refreshAuthStub{claims: &gateway.TokenClaims{
		UserID:   42,
		Mobile:   "0990000000",
		TenantID: "tenant-a",
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt: jwt.NewNumericDate(authTestNow.Add(-refreshMaxAge)),
		},
	}}
	service := &Service{Store: &store.Store{}, Auth: auth}
	_, err := service.RefreshJWT(context.Background(), "tenant-a", gateway.Token{JWT: "old-token"}, authTestSource, authTestNow)
	if !errors.Is(err, ErrRefreshExpired) {
		t.Fatalf("RefreshJWT() error = %v, want %v", err, ErrRefreshExpired)
	}
	if auth.generated {
		t.Fatal("RefreshJWT() generated a token outside the refresh window")
	}
}
