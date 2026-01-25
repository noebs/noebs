package gateway

import (
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// AdminAuthConfig controls access to admin-only endpoints.
type AdminAuthConfig struct {
	Key      string
	User     string
	Password string
	Debug    bool
}

// RequireAdmin guards admin endpoints using X-Admin-Key or HTTP Basic auth.
// If Debug is true, the guard is bypassed.
func RequireAdmin(cfg AdminAuthConfig) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if cfg.Debug {
			return c.Next()
		}

		if cfg.Key != "" {
			key := strings.TrimSpace(c.Get("X-Admin-Key"))
			if key != "" && subtle.ConstantTimeCompare([]byte(key), []byte(cfg.Key)) == 1 {
				return c.Next()
			}
		}

		if cfg.User != "" && cfg.Password != "" {
			if ok := checkBasicAuth(c.Get("Authorization"), cfg.User, cfg.Password); ok {
				return c.Next()
			}
		}

		if cfg.Key == "" && (cfg.User == "" || cfg.Password == "") {
			return c.Status(http.StatusServiceUnavailable).JSON(fiber.Map{
				"code":    "admin_auth_not_configured",
				"message": "admin auth not configured",
			})
		}

		return c.Status(http.StatusUnauthorized).JSON(fiber.Map{
			"code":    "unauthorized",
			"message": "unauthorized",
		})
	}
}

func checkBasicAuth(header, user, pass string) bool {
	if header == "" {
		return false
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "basic" {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
	if err != nil {
		return false
	}
	creds := strings.SplitN(string(decoded), ":", 2)
	if len(creds) != 2 {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(creds[0]), []byte(user)) != 1 {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(creds[1]), []byte(pass)) != 1 {
		return false
	}
	return true
}
