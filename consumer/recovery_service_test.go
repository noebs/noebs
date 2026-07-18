package consumer

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
)

func TestPasswordRecoveryWorksWithoutOldDeviceKeyAndNeverIssuesSession(t *testing.T) {
	env := newTestEnv(t)
	env.Auth.Now = func() time.Time { return authTestNow }
	ctx := context.Background()
	user := seedUser(t, env.Store, env.Tenant, "0990000000", "Old1!Password")
	stmt := env.DB.Rebind(`UPDATE users SET is_verified = TRUE, public_key = ?, device_id = ?, device_token = ?
		WHERE tenant_id = ? AND id = ?`)
	if _, err := env.DB.ExecContext(ctx, stmt, refreshProofPublicKey, "old-device", "old-push-token", env.Tenant, user.ID); err != nil {
		t.Fatalf("prepare verified user: %v", err)
	}
	messages := captureSMSMessages(t, env.Service, http.StatusOK)
	preResetToken, _, err := env.Service.Login(ctx, env.Tenant, user.Mobile, "Old1!Password", authTestSource, authTestNow)
	if err != nil {
		t.Fatalf("pre-reset login: %v", err)
	}
	env.Auth.Sessions = gateway.SessionValidatorFunc(func(ctx context.Context, tenantID string, userID, epoch int64) error {
		if err := env.Store.ValidateSessionEpoch(ctx, tenantID, userID, epoch); err != nil {
			if errors.Is(err, store.ErrSessionRevoked) {
				return gateway.ErrSessionRevoked
			}
			return err
		}
		return nil
	})
	protected := fiber.New()
	protected.Get("/protected", env.Auth.AuthMiddleware(), func(c *fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) })
	assertBearerStatus(t, protected, preResetToken, http.StatusNoContent)

	if err := env.Service.RequestPasswordRecovery(ctx, env.Tenant, user.Mobile, authTestSource, authTestNow); err != nil {
		t.Fatalf("RequestPasswordRecovery(): %v", err)
	}
	match := regexp.MustCompile(`recovery code is: ([0-9]{6})`).FindStringSubmatch(<-messages)
	if len(match) != 2 {
		t.Fatal("recovery SMS did not contain a six-digit challenge")
	}
	grant, err := env.Service.VerifyPasswordRecoveryOTP(ctx, env.Tenant, user.Mobile, match[1], authTestSource, authTestNow.Add(time.Second))
	if err != nil {
		t.Fatalf("VerifyPasswordRecoveryOTP() without device signature: %v", err)
	}
	if grant.RecoveryCredential == "" || grant.ExpiresIn != 600 {
		t.Fatalf("recovery grant = %+v", grant)
	}
	if claims, err := env.Auth.VerifyJWT(grant.RecoveryCredential); err == nil || claims != nil {
		t.Fatalf("opaque recovery credential was accepted as a session: claims=%+v err=%v", claims, err)
	}

	newPublicKey := generateTestPublicKey(t)
	reset := PasswordRecoveryReset{
		RecoveryCredential: grant.RecoveryCredential,
		NewPassword:        "New2@Password",
		NewPublicKey:       newPublicKey,
	}
	if err := env.Service.ResetPasswordWithRecoveryCredential(ctx, "other-tenant", reset, authTestSource, authTestNow.Add(2*time.Second)); !errors.Is(err, ErrInvalidRecoveryCredential) {
		t.Fatalf("cross-tenant reset error = %v, want %v", err, ErrInvalidRecoveryCredential)
	}
	if err := env.Service.ResetPasswordWithRecoveryCredential(ctx, env.Tenant, reset, authTestSource, authTestNow.Add(2*time.Second)); err != nil {
		t.Fatalf("ResetPasswordWithRecoveryCredential(): %v", err)
	}
	assertBearerStatus(t, protected, preResetToken, http.StatusUnauthorized)
	if _, err := env.Service.RefreshJWT(ctx, env.Tenant, gateway.Token{JWT: preResetToken}, authTestSource, authTestNow.Add(3*time.Second)); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("pre-reset refresh error = %v, want %v", err, ErrSessionRevoked)
	}
	if err := env.Service.ResetPasswordWithRecoveryCredential(ctx, env.Tenant, reset, authTestSource, authTestNow.Add(3*time.Second)); !errors.Is(err, ErrInvalidRecoveryCredential) {
		t.Fatalf("replayed reset error = %v, want %v", err, ErrInvalidRecoveryCredential)
	}

	if _, _, err := env.Service.Login(ctx, env.Tenant, user.Mobile, "Old1!Password", authTestSource, authTestNow.Add(4*time.Second)); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("old password login error = %v, want %v", err, ErrWrongPassword)
	}
	token, recovered, err := env.Service.Login(ctx, env.Tenant, user.Mobile, "New2@Password", authTestSource, authTestNow.Add(5*time.Second))
	if err != nil {
		t.Fatalf("new password login: %v", err)
	}
	if token == "" || !recovered.IsVerified {
		t.Fatalf("recovered login token=%q verified=%v", token, recovered.IsVerified)
	}
	stored, err := env.Store.GetUserByMobile(ctx, env.Tenant, user.Mobile)
	if err != nil {
		t.Fatalf("get recovered user: %v", err)
	}
	_, canonicalKey, err := parseUserPublicKey(newPublicKey)
	if err != nil {
		t.Fatalf("canonicalize new key: %v", err)
	}
	if stored.PublicKey != canonicalKey || stored.DeviceID != "" || stored.DeviceToken != "" {
		t.Fatalf("recovered identity = key:%q device:%q push:%q", stored.PublicKey, stored.DeviceID, stored.DeviceToken)
	}
}

func assertBearerStatus(t *testing.T, app *fiber.App, token string, want int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+token)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("protected request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("protected status = %d, want %d", resp.StatusCode, want)
	}
}

func TestPasswordRecoveryRequestDoesNotEnumerateAccounts(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	verified := seedUser(t, env.Store, env.Tenant, "0990000000", "Password1!")
	if err := env.Store.UpdateUserColumns(ctx, env.Tenant, verified.ID, map[string]any{"is_verified": true}); err != nil {
		t.Fatalf("verify user: %v", err)
	}
	seedUser(t, env.Store, env.Tenant, "0990000001", "Password1!")
	messages := captureSMSMessages(t, env.Service, http.StatusOK)

	for i, mobile := range []string{"0990000000", "0990000001", "0990000002"} {
		source := "192.0.2." + string(rune('1'+i))
		if err := env.Service.RequestPasswordRecovery(ctx, env.Tenant, mobile, source, authTestNow); err != nil {
			t.Fatalf("request for %s: %v", mobile, err)
		}
	}
	select {
	case message := <-messages:
		if !strings.Contains(message, "recovery code") {
			t.Fatalf("unexpected SMS: %q", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("verified account did not receive a recovery challenge")
	}
	select {
	case message := <-messages:
		t.Fatalf("unverified or missing account received SMS: %q", message)
	default:
	}

	var count int
	stmt := env.DB.Rebind(`SELECT count(*) FROM otp_challenges
		WHERE tenant_id = ? AND purpose = ?`)
	if err := env.DB.QueryRowContext(ctx, stmt, env.Tenant, store.OTPChallengePurposePasswordRecovery).Scan(&count); err != nil {
		t.Fatalf("count recovery challenges: %v", err)
	}
	if count != 1 {
		t.Fatalf("recovery challenge count = %d, want 1", count)
	}
}

func TestDuplicateRegistrationResumesOnlyUnverifiedAccount(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	firstKey := generateTestPublicKey(t)
	secondKey := generateTestPublicKey(t)
	thirdKey := generateTestPublicKey(t)
	request := ebs_fields.User{Mobile: "0990000000", Password: "First1!Password", PublicKey: firstKey}
	created, err := env.Service.CreateUser(ctx, env.Tenant, request, authTestSource, authTestNow)
	if err != nil {
		t.Fatalf("initial registration: %v", err)
	}
	if created.ID <= 0 || created.Mobile != request.Mobile {
		t.Fatalf("initial registration result = %+v", created)
	}

	resumed, err := env.Service.CreateUser(ctx, env.Tenant, ebs_fields.User{
		Mobile: request.Mobile, Password: "Second2@Password", PublicKey: secondKey,
	}, authTestSource, authTestNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("resume registration: %v", err)
	}
	if resumed.ID != 0 || resumed.Mobile != request.Mobile || resumed.IsVerified {
		t.Fatalf("resume response leaked account state: %+v", resumed)
	}
	stored, err := env.Store.GetUserByMobile(ctx, env.Tenant, request.Mobile)
	if err != nil {
		t.Fatalf("get resumed user: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored.Password), []byte("Second2@Password")); err != nil {
		t.Fatalf("resumed password not rotated: %v", err)
	}
	_, canonicalSecondKey, _ := parseUserPublicKey(secondKey)
	if stored.PublicKey != canonicalSecondKey {
		t.Fatal("resumed device public key was not rotated")
	}
	if err := env.Store.SetUserVerified(ctx, env.Tenant, stored.ID, true); err != nil {
		t.Fatalf("verify resumed user: %v", err)
	}

	verifiedRepeat, err := env.Service.CreateUser(ctx, env.Tenant, ebs_fields.User{
		Mobile: request.Mobile, Password: "Third3#Password", PublicKey: thirdKey,
	}, authTestSource, authTestNow.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("repeat verified registration: %v", err)
	}
	if verifiedRepeat.ID != 0 || verifiedRepeat.Mobile != request.Mobile || verifiedRepeat.IsVerified {
		t.Fatalf("verified repeat response leaked account state: %+v", verifiedRepeat)
	}
	stored, err = env.Store.GetUserByMobile(ctx, env.Tenant, request.Mobile)
	if err != nil {
		t.Fatalf("get verified repeated user: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored.Password), []byte("Second2@Password")); err != nil || stored.PublicKey != canonicalSecondKey {
		t.Fatalf("verified account credentials changed: passwordErr=%v keyChanged=%v", err, stored.PublicKey != canonicalSecondKey)
	}
}

func TestSignupChallengeOnlyTargetsUnverifiedAccounts(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	unverified := seedUser(t, env.Store, env.Tenant, "0990000000", "Password1!")
	verified := seedUser(t, env.Store, env.Tenant, "0990000001", "Password1!")
	if err := env.Store.SetUserVerified(ctx, env.Tenant, verified.ID, true); err != nil {
		t.Fatalf("verify user: %v", err)
	}
	messages := captureSMSMessages(t, env.Service, http.StatusOK)
	for i, mobile := range []string{unverified.Mobile, verified.Mobile, "0990000002"} {
		source := "198.51.100." + string(rune('1'+i))
		if err := env.Service.GenerateSignInCode(ctx, env.Tenant, mobile, source, authTestNow); err != nil {
			t.Fatalf("GenerateSignInCode(%s): %v", mobile, err)
		}
	}
	select {
	case message := <-messages:
		if !strings.Contains(message, "mobile verification code") {
			t.Fatalf("unexpected signup SMS: %q", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unverified account did not receive signup challenge")
	}
	select {
	case message := <-messages:
		t.Fatalf("verified or missing account received signup SMS: %q", message)
	default:
	}
	var count int
	stmt := env.DB.Rebind(`SELECT count(*) FROM otp_challenges WHERE tenant_id = ? AND purpose = ?`)
	if err := env.DB.QueryRowContext(ctx, stmt, env.Tenant, store.OTPChallengePurposeSignIn).Scan(&count); err != nil {
		t.Fatalf("count signup challenges: %v", err)
	}
	if count != 1 {
		t.Fatalf("signup challenge count = %d, want 1", count)
	}
}

func captureSMSMessages(t *testing.T, service *Service, status int) <-chan string {
	t.Helper()
	messages := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		messages <- r.URL.Query().Get("sms")
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	service.NoebsConfig.SMSGateway = server.URL + "?"
	return messages
}

func generateTestPublicKey(t *testing.T) string {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal RSA public key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(der)
}
