package handler

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
)

const identityTestID = "43c5e722-e622-4c3c-a3c6-5fa8c954e49d"

func TestIdentityEvidenceRoutesRequireAuthenticatedOwner(t *testing.T) {
	app := fiber.New()
	RegisterIdentityAuthedRoutes(app.Group("/consumer"), &Handler{})
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/consumer/identity/sessions"},
		{http.MethodGet, "/consumer/identity/sessions/" + identityTestID},
		{http.MethodPut, "/consumer/identity/sessions/" + identityTestID + "/evidence/selfie"},
		{http.MethodPost, "/consumer/identity/sessions/" + identityTestID + "/submit"},
		{http.MethodDelete, "/consumer/identity/sessions/" + identityTestID},
	} {
		response, err := app.Test(httptest.NewRequest(tc.method, tc.path, nil))
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s %s = %d", tc.method, tc.path, response.StatusCode)
		}
	}
}

func TestIdentityEvidenceRejectsInvalidInputAtBoundary(t *testing.T) {
	app := fiber.New()
	app.Use(authenticatedUserTestIdentity)
	RegisterIdentityAuthedRoutes(app.Group("/consumer"), &Handler{})
	for _, payload := range []string{
		`{"session_id":"` + identityTestID + `","document_type":"passport","synthetic":false}`,
		`{"session_id":"` + identityTestID + `","document_type":"passport","synthetic":true,"user_id":2}`,
		`{"session_id":"` + identityTestID + `","document_type":"driving_licence","synthetic":true}`,
		`{"session_id":"bad-id","document_type":"passport","synthetic":true}`,
	} {
		response, err := app.Test(httptest.NewRequest(http.MethodPost, "/consumer/identity/sessions", bytes.NewBufferString(payload)))
		if err != nil {
			t.Fatal(err)
		}
		assertJSONStatusCode(t, response, http.StatusBadRequest, "invalid_identity_evidence")
		_ = response.Body.Close()
	}
}

func TestIdentityEvidenceJPEGAndRevisionChecks(t *testing.T) {
	app := fiber.New(fiber.Config{BodyLimit: 3 * 1024 * 1024})
	app.Use(authenticatedUserTestIdentity)
	RegisterIdentityAuthedRoutes(app.Group("/consumer"), &Handler{})
	var tiny bytes.Buffer
	if err := jpeg.Encode(&tiny, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body        []byte
		contentType string
		revision    string
		status      int
	}{{[]byte("not a jpeg"), "image/jpeg", "1", 400},
		{tiny.Bytes(), "image/jpeg", "1", 400},
		{tiny.Bytes(), "image/png", "1", 415},
		{tiny.Bytes(), "image/jpeg", "", 400},
		{tiny.Bytes(), "image/jpeg", "01", 400},
		{make([]byte, store.MaxIdentityImageBytes+1), "image/jpeg", "1", 413}} {
		request := httptest.NewRequest(http.MethodPut, "/consumer/identity/sessions/"+identityTestID+"/evidence/selfie", bytes.NewReader(tc.body))
		request.Header.Set("Content-Type", tc.contentType)
		request.Header.Set("X-Identity-Revision", tc.revision)
		response, err := app.Test(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("upload = %d, want %d", response.StatusCode, tc.status)
		}
	}
}

func TestIdentityEvidenceReceiptNeverClaimsVerification(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		return identityResult(c, store.IdentitySession{Status: "submitted", Synthetic: true}, nil)
	})
	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var receipt identitySessionResponse
	if err := json.NewDecoder(response.Body).Decode(&receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "submitted" || receipt.LivenessStatus != "not_evaluated" ||
		receipt.DocumentStatus != "not_evaluated" || receipt.ReviewStatus != "pending_provider" {
		t.Fatalf("false verification receipt: %+v", receipt)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("identity status is cacheable")
	}
}
