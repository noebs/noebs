package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestStatusNotificationsRejectMissingIdentityAndInvalidLimits(t *testing.T) {
	app := fiber.New()
	h := &Handler{}
	app.Get("/notifications", h.StatusNotifications)
	response, err := app.Test(httptest.NewRequest("GET", "/notifications", nil))
	if err != nil || response.StatusCode != 401 {
		t.Fatalf("unauthenticated response=%v err=%v", response, err)
	}
	app = fiber.New()
	app.Use(authenticatedUserTestIdentity)
	app.Get("/notifications", h.StatusNotifications)
	for _, value := range []string{"0", "101", "-1", "x"} {
		response, err := app.Test(httptest.NewRequest("GET", "/notifications?limit="+value, nil))
		if err != nil || response.StatusCode != 400 {
			t.Fatalf("limit=%s response=%v err=%v", value, response, err)
		}
	}
}
