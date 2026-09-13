package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestPrivateBackofficeBoundaryCoversLifecycleAssetsAndUnknownRoutes(t *testing.T) {
	const host = "noebs-workers.tail09832.ts.net"
	h := &backofficeHTTP{host: host}
	app := fiber.New()
	app.Use(h.privateBoundary)
	reached := false
	app.Use(func(c *fiber.Ctx) error { reached = true; return c.SendStatus(http.StatusNoContent) })
	paths := []string{"/backoffice", "/backoffice/", "/backoffice/login", "/backoffice/home", "/backoffice/oauth/callback", "/backoffice/oauth/logout/callback", "/backoffice/logout", "/backoffice/assets/style.css", "/backoffice/t/noebs/verifications", "/BACKOFFICE/login", "/backoffice/unknown"}
	for _, path := range paths {
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodOptions} {
			for _, test := range []struct {
				name, hostname, source, funnel string
				want                           int
			}{
				{"tailnet IPv4", host, "100.64.0.7, 127.0.0.1", "", 204},
				{"tailnet IPv6", host, "fd7a:115c:a1e0::7, 127.0.0.1", "", 204},
				{"public host", "api.noebs.sd", "100.64.0.7, 127.0.0.1", "", 404},
				{"public source", host, "203.0.113.7, 127.0.0.1", "", 404},
				{"forged prefix", host, "100.64.0.7, 203.0.113.7, 127.0.0.1", "", 404},
				{"loopback source", host, "127.0.0.1", "", 404},
				{"missing source", host, "", "", 404},
				{"Funnel", host, "100.64.0.7, 127.0.0.1", "?1", 404},
				{"wrong host suffix", host + ".evil.example", "100.64.0.7, 127.0.0.1", "", 404},
			} {
				t.Run(method+path+"/"+test.name, func(t *testing.T) {
					reached = false
					r := httptest.NewRequest(method, path, nil)
					r.Host = test.hostname
					if test.source != "" {
						r.Header.Set("X-Forwarded-For", test.source)
					}
					if test.funnel != "" {
						r.Header.Set("Tailscale-Funnel-Request", test.funnel)
					}
					r.Header.Set("X-Forwarded-Host", host)
					r.Header.Set("X-Real-IP", "100.64.0.7")
					response, err := app.Test(r)
					if err != nil {
						t.Fatal(err)
					}
					defer response.Body.Close()
					if response.StatusCode != test.want || reached != (test.want == 204) {
						t.Fatalf("status %d reached %v; expected %d", response.StatusCode, reached, test.want)
					}
					if test.want == 404 && (len(response.Cookies()) != 0 || response.Header.Get("Location") != "") {
						t.Fatal("denied public request started browser auth")
					}
				})
			}
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/account", nil)
	r.Host = "api.noebs.sd"
	response, err := app.Test(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal("private backoffice gate intercepted public account")
	}
}

func TestBackofficeRuntimeUsesExplicitPrivateOrigin(t *testing.T) {
	cfg := cliTestConfig(serviceRoleAPIGateway, "")
	runtime, err := buildBackofficeRuntimeDependencies(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.requestHost != "backoffice.example" {
		t.Fatal("BFF ignored private origin")
	}
	for _, mutation := range []func(){
		func() { cfg.BackofficeOrigin = "" },
		func() { cfg.BackofficeOrigin = "https://app.example" },
		func() { cfg.BackofficeRedirectURL = "https://app.example/backoffice/oauth/callback" },
		func() { cfg.BackofficePostLogoutURL = "https://app.example/backoffice/oauth/logout/callback" },
	} {
		cfg = cliTestConfig(serviceRoleAPIGateway, "")
		mutation()
		if _, err := buildBackofficeRuntimeDependencies(cfg); err == nil {
			t.Fatal("accepted ambiguous private browser configuration")
		}
	}
}
