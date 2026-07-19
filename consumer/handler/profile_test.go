package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/internal/testdb"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
)

const (
	profileTestTenant  = "profile-handler-test"
	profileTestIssuer  = "https://identity.example/realms/noebs"
	profileTestSubject = "577871c5-e19c-499f-bcbf-60b3fdc63a49"
)

func TestCreateProfileProjectionRequiresTrustedPrincipalBoundary(t *testing.T) {
	validBody := []byte(`{"fullname":"Profile Owner","mobile":"0990000000"}`)

	t.Run("raw identity headers are not a typed principal", func(t *testing.T) {
		app := fiber.New()
		h := &Handler{Service: &consumer.Service{Store: &store.Store{}}}
		app.Post("/consumer/auth/profile", h.CreateProfileProjection)

		resp := performProfileCreateRequest(t, app, validBody)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
		}
	})

	t.Run("issuer and subject are rejected in the body", func(t *testing.T) {
		app := fiber.New()
		h := &Handler{Service: &consumer.Service{Store: &store.Store{}}}
		app.Post("/consumer/auth/profile", gateway.InternalPrincipalIdentityMiddleware(), h.CreateProfileProjection)
		body := []byte(`{"issuer":"https://attacker.example","subject":"attacker","fullname":"Profile Owner","mobile":"0990000000"}`)

		resp := performProfileCreateRequest(t, app, body)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
	})

	t.Run("typed principal creates once and duplicate is conflict", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		postgres, err := testdb.StartPostgresContainer(ctx)
		if err != nil {
			if testdb.IsContainerRuntimeUnavailable(err) {
				t.Skipf("container runtime unavailable: %v", err)
			}
			t.Fatalf("start postgres: %v", err)
		}
		databaseName := fmt.Sprintf("profile_handler_%d", time.Now().UnixNano())
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
		if err := store.MigrateScope(ctx, db, profileTestTenant, store.MigrationScopeIdentityAuth); err != nil {
			t.Fatalf("migrate identity auth: %v", err)
		}
		storeSvc := store.New(db)
		if err := storeSvc.EnsureTenant(ctx, profileTestTenant); err != nil {
			t.Fatalf("ensure tenant: %v", err)
		}

		app := fiber.New()
		h := &Handler{Service: &consumer.Service{Store: storeSvc}}
		app.Post("/consumer/auth/profile", gateway.InternalPrincipalIdentityMiddleware(), h.CreateProfileProjection)
		created := performProfileCreateRequest(t, app, validBody)
		defer created.Body.Close()
		if created.StatusCode != http.StatusCreated {
			t.Fatalf("create status = %d, want %d", created.StatusCode, http.StatusCreated)
		}
		var payload struct {
			User consumer.ProfileProjection `json:"user"`
		}
		if err := json.NewDecoder(created.Body).Decode(&payload); err != nil {
			t.Fatalf("decode create response: %v", err)
		}
		if payload.User.TenantID != profileTestTenant || payload.User.Issuer != profileTestIssuer ||
			payload.User.Subject != profileTestSubject || payload.User.UserID <= 0 {
			t.Fatalf("created profile authority = %+v", payload.User)
		}

		duplicate := performProfileCreateRequest(t, app, []byte(`{"fullname":"Replay","mobile":"0990000001"}`))
		defer duplicate.Body.Close()
		if duplicate.StatusCode != http.StatusConflict {
			t.Fatalf("duplicate status = %d, want %d", duplicate.StatusCode, http.StatusConflict)
		}
	})
}

func performProfileCreateRequest(t *testing.T, app *fiber.App, body []byte) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/consumer/auth/profile", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(gateway.GatewayTenantIDHeader, profileTestTenant)
	req.Header.Set(gateway.GatewayIssuerHeader, profileTestIssuer)
	req.Header.Set(gateway.GatewaySubjectHeader, profileTestSubject)
	resp, err := app.Test(req, int((30 * time.Second).Milliseconds()))
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	return resp
}
