package consumer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

type testEnv struct {
	Router  *fiber.App
	Service *Service
	Auth    *gateway.JWTAuth
	Store   *store.Store
	DB      *store.DB
	Tenant  string
}

func newTestDB(t *testing.T) (*store.DB, *store.Store, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := store.OpenFromConfig("", dbPath, "")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.Migrate(ctx, db, store.DefaultTenantID); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	storeSvc := store.New(db)
	tenantID := "test-tenant"
	if err := storeSvc.EnsureTenant(ctx, tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}
	return db, storeSvc, tenantID
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	db, storeSvc, tenantID := newTestDB(t)

	smsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(smsServer.Close)

	cfg := ebs_fields.NoebsConfig{
		JWTKey:          "test-secret",
		BillInquiryIPIN: "0000",
		EBSConsumerKey:  "test-key",
		SMSGateway:      smsServer.URL + "?",
		SMSAPIKey:       "test-key",
		SMSSender:       "noebs",
		SMSMessage:      "test",
		DefaultTenantID: tenantID,
	}

	auth := &gateway.JWTAuth{NoebsConfig: cfg}
	auth.Init()

	logger := logrus.New()
	service := &Service{
		Store:       storeSvc,
		NoebsConfig: cfg,
		Logger:      logger,
		Auth:        auth,
	}

	r := fiber.New()
	r.Post("/register", service.CreateUser)
	r.Post("/login", service.LoginHandler)
	r.Post("/register_with_card", wrapTestHandler(service.RegisterWithCard))
	r.Get("/notifications", auth.AuthMiddleware(), service.Notifications)

	return &testEnv{Router: r, Service: service, Auth: auth, Store: storeSvc, DB: db, Tenant: tenantID}
}

func wrapTestHandler(h interface{}) fiber.Handler {
	switch v := h.(type) {
	case func(*fiber.Ctx) error:
		return v
	case func(*fiber.Ctx):
		return func(c *fiber.Ctx) error {
			v(c)
			return nil
		}
	default:
		panic("unsupported handler type")
	}
}

func seedUser(t *testing.T, storeSvc *store.Store, tenantID, mobile, password string) ebs_fields.User {
	t.Helper()
	user := ebs_fields.User{
		Mobile:   mobile,
		Username: mobile,
		Password: password,
		Email:    mobile + "@example.com",
	}
	if err := user.HashPassword(); err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := storeSvc.CreateUser(context.Background(), tenantID, &user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}
