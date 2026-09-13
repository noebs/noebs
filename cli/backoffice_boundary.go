package main

import (
	"net/http"
	"net/netip"
	"strings"

	"github.com/gofiber/fiber/v2"
)

var backofficeTailnetPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fd7a:115c:a1e0::/48"),
}

// The authenticated edge preserves the exact Tailscale Serve caller address.
// Apply this before every backoffice route, including assets and OAuth callbacks.
func (h *backofficeHTTP) privateBoundary(c *fiber.Ctx) error {
	path := strings.ToLower(c.Path())
	if path != "/backoffice" && !strings.HasPrefix(path, "/backoffice/") {
		return c.Next()
	}
	source, err := gatewayRequestSource(c)
	address, parseErr := netip.ParseAddr(source)
	allowed := false
	if err == nil && parseErr == nil && !address.Is4In6() {
		for _, prefix := range backofficeTailnetPrefixes {
			allowed = allowed || prefix.Contains(address)
		}
	}
	if h == nil || string(c.Request().Header.Host()) != h.host || !allowed || len(c.Request().Header.PeekAll("Tailscale-Funnel-Request")) != 0 {
		return backofficeFiberError(c, http.StatusNotFound)
	}
	return c.Next()
}
