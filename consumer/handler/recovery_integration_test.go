package handler

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/testdb"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

func TestPasswordRecoveryHTTPJourney(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	postgres, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = postgres.Terminate(context.Background()) })
	dbURL, err := postgres.CreateDatabase(ctx, "handler_recovery")
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	db, err := store.OpenFromConfig(dbURL, store.DriverPostgres)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	const tenantID = "http-test-tenant"
	if err := store.MigrateScope(ctx, db, tenantID, store.MigrationScopeIdentityAuth); err != nil {
		t.Fatalf("migrate identity auth: %v", err)
	}
	storeSvc := store.New(db)
	if err := storeSvc.EnsureTenant(ctx, tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}

	messages := make(chan string, 4)
	sms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		messages <- r.URL.Query().Get("sms")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(sms.Close)
	cfg := ebs_fields.NoebsConfig{
		JWTKey:     "http-recovery-secret",
		SMSGateway: sms.URL + "?",
		SMSAPIKey:  "test",
		SMSSender:  "noebs",
	}
	auth := &gateway.JWTAuth{NoebsConfig: cfg}
	auth.Init()
	service := &consumer.Service{Store: storeSvc, NoebsConfig: cfg, Auth: auth, Logger: logrus.New()}
	h, err := New(service)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	user := ebs_fields.User{
		Mobile:      "0990000000",
		Username:    "0990000000",
		Password:    "Old1!Password",
		PublicKey:   httpTestPublicKey(t),
		IsVerified:  true,
		DeviceID:    "lost-device",
		DeviceToken: "lost-push-token",
	}
	if err := user.HashPassword(); err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := storeSvc.CreateUser(ctx, tenantID, &user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	app := fiber.New()
	public := gateway.InternalTenantIdentityMiddleware()
	app.Post("/consumer/recovery/request", public, h.RequestPasswordRecovery)
	app.Post("/consumer/recovery/verify", public, h.VerifyPasswordRecovery)
	app.Post("/consumer/recovery/reset", public, h.ResetPasswordWithRecovery)

	requestBody := fmt.Sprintf(`{"mobile":%q}`, user.Mobile)
	resp := recoveryHTTPCall(t, app, "/consumer/recovery/request", requestBody, tenantID)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("request status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}
	var accepted map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode request response: %v", err)
	}
	_ = resp.Body.Close()
	if accepted["result"] != "accepted" {
		t.Fatalf("request response = %v", accepted)
	}
	match := regexp.MustCompile(`recovery code is: ([0-9]{6})`).FindStringSubmatch(<-messages)
	if len(match) != 2 {
		t.Fatal("recovery SMS did not contain an OTP")
	}

	verifyBody := fmt.Sprintf(`{"mobile":%q,"otp":%q}`, user.Mobile, match[1])
	resp = recoveryHTTPCall(t, app, "/consumer/recovery/verify", verifyBody, tenantID)
	if resp.StatusCode != http.StatusOK || resp.Header.Get(fiber.HeaderCacheControl) != "no-store" {
		t.Fatalf("verify status/cache = %d/%q", resp.StatusCode, resp.Header.Get(fiber.HeaderCacheControl))
	}
	var grant consumer.RecoveryCredentialResult
	if err := json.NewDecoder(resp.Body).Decode(&grant); err != nil {
		t.Fatalf("decode recovery grant: %v", err)
	}
	_ = resp.Body.Close()
	if grant.RecoveryCredential == "" || grant.ExpiresIn != 600 || resp.Header.Get(fiber.HeaderAuthorization) != "" {
		t.Fatalf("recovery grant/header = %+v/%q", grant, resp.Header.Get(fiber.HeaderAuthorization))
	}

	newPublicKey := httpTestPublicKey(t)
	resetBody := fmt.Sprintf(`{"recovery_credential":%q,"new_password":"New2@Password","new_public_key":%q}`, grant.RecoveryCredential, newPublicKey)
	resp = recoveryHTTPCall(t, app, "/consumer/recovery/reset", resetBody, tenantID)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reset status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	resp = recoveryHTTPCall(t, app, "/consumer/recovery/reset", resetBody, tenantID)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replay status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	var replay map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&replay); err != nil {
		t.Fatalf("decode replay response: %v", err)
	}
	if replay["code"] != "invalid_recovery_credential" {
		t.Fatalf("replay response = %v", replay)
	}

	stored, err := storeSvc.GetUserByMobile(ctx, tenantID, user.Mobile)
	if err != nil {
		t.Fatalf("get recovered user: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored.Password), []byte("New2@Password")); err != nil {
		t.Fatalf("new password was not persisted: %v", err)
	}
	if stored.PublicKey == user.PublicKey || stored.DeviceID != "" || stored.DeviceToken != "" || stored.SessionEpoch != 2 {
		t.Fatalf("recovered identity = keyRotated:%v device:%q push:%q epoch:%d", stored.PublicKey != user.PublicKey, stored.DeviceID, stored.DeviceToken, stored.SessionEpoch)
	}
}

func recoveryHTTPCall(t *testing.T, app *fiber.App, path, body, tenantID string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	req.Header.Set(gateway.GatewayTenantIDHeader, tenantID)
	req.Header.Set(gateway.GatewaySourceIPHeader, "203.0.113.8")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func httpTestPublicKey(t *testing.T) string {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal RSA key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(der)
}
