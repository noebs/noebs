package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestAPIGatewayPublicHealthRouteMatchesKubernetesProbes(t *testing.T) {
	app := fiber.New()
	registerAPIGatewayHealthRoute(app, serviceRoleAPIGateway)
	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/test", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer closeResponseBody(t, response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /test status = %d, want 200", response.StatusCode)
	}
	var payload map[string]bool
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if !payload["message"] {
		t.Fatalf("GET /test payload = %#v", payload)
	}

	nonGateway := fiber.New()
	registerAPIGatewayHealthRoute(nonGateway, serviceRoleIdentityAuth)
	response, err = nonGateway.Test(httptest.NewRequest(http.MethodGet, "/test", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer closeResponseBody(t, response.Body)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("identity-auth GET /test status = %d, want 404", response.StatusCode)
	}

	objects := decodeManifestObjects(t, filepath.Join("..", "deploy", "kubernetes", "base", "api-gateway.yaml"))
	for _, object := range objects {
		if object.Kind != "Deployment" || object.Metadata.Name != "api-gateway" {
			continue
		}
		container := object.Spec.Template.Spec.Containers[0]
		assertProbePath(t, "readiness", container.ReadinessProbe, "/test")
		assertProbePath(t, "liveness", container.LivenessProbe, "/test")
		return
	}
	t.Fatal("api-gateway Deployment not found")
}

func TestAPIGatewayDoesNotExposeMetrics(t *testing.T) {
	ensureInit()
	configureGatewayProxyForTest(t)
	assertFiberRouteAbsent(t, GetMainEngine(), roleRoute{
		method: http.MethodGet,
		path:   "/metrics",
	})
}

func assertProbePath(t testing.TB, name string, probe map[string]any, want string) {
	t.Helper()
	httpGet, ok := probe["httpGet"].(map[string]any)
	if !ok || httpGet["path"] != want {
		t.Fatalf("%s probe = %#v, want path %s", name, probe, want)
	}
}
