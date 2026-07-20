package gateway

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestNoebsCorsAllowsTransactionAuthorizationHeader(t *testing.T) {
	const origin = "https://wallet.example"
	app := fiber.New()
	app.Use(NoebsCors([]string{origin}))
	app.Post("/wallet/p2p", func(c *fiber.Ctx) error {
		return c.SendStatus(http.StatusNoContent)
	})

	request, err := http.NewRequest(http.MethodOptions, "http://gateway.example/wallet/p2p", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", origin)
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "X-Noebs-Transaction-Authorization, Content-Type")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })

	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, origin)
	}
	if !commaSeparatedHeaderContains(response.Header.Get("Access-Control-Allow-Headers"), "X-Noebs-Transaction-Authorization") {
		t.Fatalf("Access-Control-Allow-Headers = %q", response.Header.Get("Access-Control-Allow-Headers"))
	}
}

func TestNoebsCorsDoesNotAllowUnconfiguredOrigin(t *testing.T) {
	app := fiber.New()
	app.Use(NoebsCors([]string{"https://wallet.example"}))
	app.Post("/wallet/p2p", func(c *fiber.Ctx) error {
		return c.SendStatus(http.StatusNoContent)
	})

	request, err := http.NewRequest(http.MethodOptions, "http://gateway.example/wallet/p2p", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "X-Noebs-Transaction-Authorization")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })

	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty", got)
	}
}

func commaSeparatedHeaderContains(header, want string) bool {
	for _, value := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
}
