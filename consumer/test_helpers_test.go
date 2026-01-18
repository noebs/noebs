package consumer

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type testEnv struct {
	Router  *fiber.App
	Service *Service
	Auth    *gateway.JWTAuth
	DB      *gorm.DB
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(
		&ebs_fields.User{},
		&ebs_fields.Card{},
		&ebs_fields.Token{},
		&ebs_fields.EBSResponse{},
		&PushData{},
		&ebs_fields.CacheCards{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	db := newTestDB(t)

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
	}

	auth := &gateway.JWTAuth{NoebsConfig: cfg}
	auth.Init()

	logger := logrus.New()
	service := &Service{
		Db:          db,
		NoebsConfig: cfg,
		Logger:      logger,
		Auth:        auth,
	}

	r := fiber.New()
	r.Post("/register", service.CreateUser)
	r.Post("/login", service.LoginHandler)
	r.Post("/register_with_card", wrapTestHandler(service.RegisterWithCard))
	r.Get("/notifications", auth.AuthMiddleware(), service.Notifications)

	return &testEnv{Router: r, Service: service, Auth: auth, DB: db}
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

func seedUser(t *testing.T, db *gorm.DB, mobile, password string) ebs_fields.User {
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
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}
