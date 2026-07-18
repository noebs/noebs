package handler

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
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
)

func TestRegistrationHTTPContractAndVerificationFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	postgres, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatalf("start postgres: %v", err)
	}
	databaseName := fmt.Sprintf("handler_registration_%d", time.Now().UnixNano())
	dbURL, err := postgres.CreateDatabase(ctx, databaseName)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	db, err := store.OpenFromConfig(dbURL, store.DriverPostgres)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_ = postgres.DropDatabase(context.Background(), databaseName)
		_ = postgres.Terminate(context.Background())
	})

	const tenantID = "registration-http-test"
	if err := store.MigrateScope(ctx, db, tenantID, store.MigrationScopeIdentityAuth); err != nil {
		t.Fatalf("migrate identity auth: %v", err)
	}
	storeSvc := store.New(db, store.WithDataKey("registration-test-data-key"))
	if err := storeSvc.EnsureTenant(ctx, tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}

	messages := make(chan string, 1)
	sms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		messages <- r.URL.Query().Get("sms")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(sms.Close)
	cfg := ebs_fields.NoebsConfig{
		JWTKey:     "registration-http-secret",
		SMSGateway: sms.URL + "?",
		SMSAPIKey:  "test",
		SMSSender:  "noebs",
		SMSMessage: "test",
	}
	auth := &gateway.JWTAuth{NoebsConfig: cfg}
	auth.Init()
	service := &consumer.Service{Store: storeSvc, NoebsConfig: cfg, Auth: auth, Logger: logrus.New()}
	h, err := New(service)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	app := fiber.New()
	publicIdentity := gateway.InternalTenantIdentityMiddleware()
	app.Post("/consumer/register", publicIdentity, h.CreateUser)
	app.Post("/consumer/login", publicIdentity, h.LoginHandler)
	app.Post("/consumer/otp/generate", publicIdentity, h.GenerateSignInCode)
	app.Post("/consumer/otp/verify", publicIdentity, h.VerifyOTP)

	publicKey, sign := registrationSignatureProof(t)
	const (
		mobile        = "0992220000"
		firstPassword = "First1!Password"
		finalPassword = "Second2@Password"
	)
	validRegistration := map[string]any{
		"mobile":      mobile,
		"password":    firstPassword,
		"user_pubkey": publicKey,
		"fullname":    "Alpha Tester",
		"username":    "alpha-tester",
		"birthday":    "1990-01-01",
		"email":       "alpha@example.test",
	}

	unsupported := map[string]any{
		"public_key":      publicKey,
		"is_verified":     true,
		"is_password_otp": true,
		"is_merchant":     true,
		"device_id":       "attacker-device",
		"device_token":    "attacker-push-token",
		"otp":             "123456",
		"signed_otp":      "signed",
		"main_card":       "6394123456789012",
		"main_card_enc":   "enc:attacker",
		"exp_date":        "2912",
		"firebase_token":  "legacy-token",
		"password2":       firstPassword,
	}
	for field, value := range unsupported {
		t.Run("reject_"+field, func(t *testing.T) {
			body := cloneRegistrationPayload(validRegistration)
			body[field] = value
			resp := registrationHTTPCall(t, app, "/consumer/register", body, tenantID)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}
		})
	}
	invalidAllowedFields := map[string]func(map[string]any){
		"missing_fullname": func(body map[string]any) { delete(body, "fullname") },
		"blank_fullname":   func(body map[string]any) { body["fullname"] = "   " },
		"blank_username":   func(body map[string]any) { body["username"] = "   " },
		"invalid_email":    func(body map[string]any) { body["email"] = "not-an-email" },
	}
	for name, mutate := range invalidAllowedFields {
		t.Run("reject_"+name, func(t *testing.T) {
			body := cloneRegistrationPayload(validRegistration)
			mutate(body)
			resp := registrationHTTPCall(t, app, "/consumer/register", body, tenantID)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}
		})
	}
	if count := registrationUserCount(t, db, tenantID, mobile); count != 0 {
		t.Fatalf("unsupported registration created %d users", count)
	}

	encoded, err := json.Marshal(validRegistration)
	if err != nil {
		t.Fatalf("marshal registration: %v", err)
	}
	trailingRequest := httptest.NewRequest(http.MethodPost, "/consumer/register", strings.NewReader(string(encoded)+` {}`))
	trailingRequest.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	trailingRequest.Header.Set(gateway.GatewayTenantIDHeader, tenantID)
	trailingRequest.Header.Set(gateway.GatewaySourceIPHeader, "203.0.113.8")
	trailingResponse, err := app.Test(trailingRequest, -1)
	if err != nil {
		t.Fatalf("trailing registration: %v", err)
	}
	_ = trailingResponse.Body.Close()
	if trailingResponse.StatusCode != http.StatusBadRequest || registrationUserCount(t, db, tenantID, mobile) != 0 {
		t.Fatalf("trailing JSON status/count = %d/%d", trailingResponse.StatusCode, registrationUserCount(t, db, tenantID, mobile))
	}

	response := registrationHTTPCall(t, app, "/consumer/register", validRegistration, tenantID)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("registration status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	assertRegistrationSafeDefaults(t, db, tenantID, mobile, 1)

	if _, err := db.ExecContext(ctx, db.Rebind(`UPDATE users SET
		is_merchant = TRUE, is_password_otp = TRUE,
		device_id = 'unsafe-device', device_token = 'unsafe-token',
		otp = 'unsafe-otp', signed_otp = 'unsafe-signature',
		main_card = 'unsafe-pan', main_card_enc = 'unsafe-ciphertext', main_expdate = '2912',
		session_epoch = 7
		WHERE tenant_id = ? AND mobile = ? AND is_verified = FALSE`), tenantID, mobile); err != nil {
		t.Fatalf("seed unsafe resume state: %v", err)
	}
	resumedRegistration := cloneRegistrationPayload(validRegistration)
	resumedRegistration["password"] = finalPassword
	response = registrationHTTPCall(t, app, "/consumer/register", resumedRegistration, tenantID)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("resume status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	assertRegistrationSafeDefaults(t, db, tenantID, mobile, 8)

	response = registrationHTTPCall(t, app, "/consumer/login", map[string]any{
		"mobile": mobile, "password": finalPassword,
	}, tenantID)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("pre-verification login status = %d, want %d", response.StatusCode, http.StatusForbidden)
	}

	response = registrationHTTPCall(t, app, "/consumer/otp/generate", map[string]any{"mobile": mobile}, tenantID)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("OTP generation status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	match := regexp.MustCompile(`verification code is: ([0-9]{6})`).FindStringSubmatch(<-messages)
	if len(match) != 2 {
		t.Fatal("verification SMS did not contain a six-digit OTP")
	}
	response = registrationHTTPCall(t, app, "/consumer/otp/verify", map[string]any{
		"mobile": mobile, "otp": match[1], "signature": sign(match[1]),
	}, tenantID)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("OTP verification status = %d, want %d", response.StatusCode, http.StatusOK)
	}

	response = registrationHTTPCall(t, app, "/consumer/login", map[string]any{
		"mobile": mobile, "password": finalPassword,
	}, tenantID)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get(fiber.HeaderAuthorization) == "" {
		t.Fatalf("verified login status/authorization = %d/%q", response.StatusCode, response.Header.Get(fiber.HeaderAuthorization))
	}
}

func cloneRegistrationPayload(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source)+1)
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func registrationHTTPCall(t *testing.T, app *fiber.App, path string, body map[string]any, tenantID string) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s request: %v", path, err)
	}
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(encoded)))
	request.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	request.Header.Set(gateway.GatewayTenantIDHeader, tenantID)
	request.Header.Set(gateway.GatewaySourceIPHeader, "203.0.113.8")
	response, err := app.Test(request, -1)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return response
}

func registrationUserCount(t *testing.T, db *store.DB, tenantID, mobile string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(), db.Rebind(`SELECT COUNT(*) FROM users WHERE tenant_id = ? AND mobile = ?`), tenantID, mobile).Scan(&count); err != nil {
		t.Fatalf("count registration users: %v", err)
	}
	return count
}

func assertRegistrationSafeDefaults(t *testing.T, db *store.DB, tenantID, mobile string, expectedSessionEpoch int64) {
	t.Helper()
	var state struct {
		Fullname       string
		Username       string
		Birthday       string
		Email          string
		Verified       bool
		PasswordOTP    bool
		Merchant       bool
		DeviceID       string
		DeviceToken    string
		OTP            string
		SignedOTP      string
		MainCard       string
		MainCardCipher string
		MainExpiry     string
		SessionEpoch   int64
	}
	err := db.QueryRowContext(context.Background(), db.Rebind(`SELECT
		fullname, username, birthday, email,
		is_verified, is_password_otp, is_merchant,
		COALESCE(device_id, ''), COALESCE(device_token, ''),
		COALESCE(otp, ''), COALESCE(signed_otp, ''),
		COALESCE(main_card, ''), COALESCE(main_card_enc, ''), COALESCE(main_expdate, ''),
		session_epoch
		FROM users WHERE tenant_id = ? AND mobile = ?`), tenantID, mobile).Scan(
		&state.Fullname, &state.Username, &state.Birthday, &state.Email,
		&state.Verified, &state.PasswordOTP, &state.Merchant,
		&state.DeviceID, &state.DeviceToken, &state.OTP, &state.SignedOTP,
		&state.MainCard, &state.MainCardCipher, &state.MainExpiry, &state.SessionEpoch,
	)
	if err != nil {
		t.Fatalf("read registration state: %v", err)
	}
	if state.Fullname != "Alpha Tester" || state.Username != "alpha-tester" ||
		state.Birthday != "1990-01-01" || state.Email != "alpha@example.test" ||
		state.Verified || state.PasswordOTP || state.Merchant ||
		state.DeviceID != "" || state.DeviceToken != "" || state.OTP != "" || state.SignedOTP != "" ||
		state.MainCard != "" || state.MainCardCipher != "" || state.MainExpiry != "" || state.SessionEpoch != expectedSessionEpoch {
		t.Fatalf("unsafe registration state persisted: %+v", state)
	}
}

func registrationSignatureProof(t *testing.T) (string, func(string) string) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal RSA public key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(der), func(message string) string {
		digest := sha256.Sum256([]byte(message))
		signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
		if err != nil {
			t.Fatalf("sign OTP: %v", err)
		}
		return base64.StdEncoding.EncodeToString(signature)
	}
}
