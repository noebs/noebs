package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// App Links authorizes browser callbacks for each established signing identity.
// It does not make Android installations signed by different keys upgradeable.
func TestAndroidAssetLinksPreserveExistingAndManagedReleaseCertificates(t *testing.T) {
	app := fiber.New()
	registerAndroidAssetLinksRoute(app, serviceRoleAPIGateway)
	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/.well-known/assetlinks.json", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer closeResponseBody(t, response.Body)
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != fiber.MIMEApplicationJSON ||
		response.Header.Get("Cache-Control") != "public, max-age=3600" {
		t.Fatalf("unexpected assetlinks response: status=%d headers=%v", response.StatusCode, response.Header)
	}
	var links []struct {
		Relation []string `json:"relation"`
		Target   struct {
			Namespace    string   `json:"namespace"`
			Package      string   `json:"package_name"`
			Fingerprints []string `json:"sha256_cert_fingerprints"`
		} `json:"target"`
	}
	if err := json.NewDecoder(response.Body).Decode(&links); err != nil {
		t.Fatalf("assetlinks response must be valid JSON: %v", err)
	}
	if len(links) != 1 {
		t.Fatal("only the existing TutiPay alpha package is authorized")
	}
	link := links[0]
	if !reflect.DeepEqual(link.Relation, []string{"delegate_permission/common.handle_all_urls"}) ||
		link.Target.Namespace != "android_app" || link.Target.Package != "com.tutipay.app.alpha" {
		t.Fatalf("unexpected App Links target: %+v", link)
	}
	want := []string{
		"B4:45:C2:79:FE:FB:B0:95:AA:33:4F:67:42:4D:EA:6B:52:77:38:EA:FF:A5:EF:FB:80:B5:E2:F5:9B:66:1C:AE",
		"BB:1F:DB:0B:76:AE:89:D6:B6:BD:7A:D4:A1:5F:85:59:60:16:55:48:73:8E:E4:B8:DC:89:4A:8F:BC:1A:AE:F5",
		"08:16:D2:E0:36:91:A9:FB:4C:39:A4:49:DB:18:B6:FD:DB:35:7C:52:83:96:1F:1A:19:34:D7:6D:2A:99:0A:A7",
	}
	if !reflect.DeepEqual(link.Target.Fingerprints, want) {
		t.Fatalf("Android link certificates changed: got %v, want %v", link.Target.Fingerprints, want)
	}
	fingerprint := regexp.MustCompile(`^(?:[0-9A-F]{2}:){31}[0-9A-F]{2}$`)
	for _, value := range link.Target.Fingerprints {
		if !fingerprint.MatchString(value) {
			t.Fatalf("invalid SHA-256 certificate fingerprint: %q", value)
		}
	}
}

func TestAndroidAssetLinksOnlyServedByGateway(t *testing.T) {
	app := fiber.New()
	registerAndroidAssetLinksRoute(app, serviceRoleIdentityAuth)
	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/.well-known/assetlinks.json", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer closeResponseBody(t, response.Body)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.StatusCode)
	}
}
