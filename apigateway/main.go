// Package gateway implments various auth logic used across noebs services
package gateway

import (
	"crypto/rand"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/golang-jwt/jwt/v5"
)

// AuthMiddleware is a JWT authorization middleware. It is used in our consumer services
// to get a username from the payload (maybe change it to mobile number at somepoint)
func (a *JWTAuth) AuthMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		// just handle the simplest case, authorization is not provided.
		h := strings.TrimSpace(c.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(h), "bearer ") {
			h = strings.TrimSpace(h[7:])
		}
		if h == "" {
			return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"message": "empty header was sent", "code": "unauthorized"})
		}
		claims, err := a.VerifyJWT(h)
		if err != nil {
			if errors.Is(err, jwt.ErrTokenExpired) {
				return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"message": "Token has expired", "code": "jwt_expired"})
			}
			return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"message": "Malformed token", "code": "jwt_malformed"})
		} else {
			if a.Sessions != nil {
				if err := a.Sessions.ValidateSession(c.UserContext(), claims.TenantID, claims.UserID, claims.SessionEpoch); err != nil {
					if !errors.Is(err, ErrSessionRevoked) {
						return c.Status(http.StatusServiceUnavailable).JSON(fiber.Map{"message": "Session validation is unavailable", "code": "session_validation_unavailable"})
					}
					return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"message": "Session has been revoked", "code": "session_revoked"})
				}
			}
			// FIXME it is better to let the endpoint explicitly Get the claim off the user
			//  as we will assume the auth server will reside in a different domain!
			c.Locals("user_id", claims.UserID)
			c.Locals("session_epoch", claims.SessionEpoch)
			c.Locals("session_token", h)
			if isValidMobile(claims.Mobile) {
				c.Locals("mobile", claims.Mobile)
				c.Locals("username", claims.Mobile)
			}
			tenantID, err := validateTenantID(claims.TenantID)
			if err != nil {
				return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"message": "Malformed token", "code": "jwt_malformed"})
			}
			c.Locals("tenant_id", tenantID)
			return c.Next()
		}
	}

}

// GenerateSecretKey generates secret key for jwt signing
func GenerateSecretKey(n int) ([]byte, error) {
	key := make([]byte, n)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// NoebsCors reads from noebs config to setup cors headers for the server
func NoebsCors(headers []string) fiber.Handler {
	if len(headers) == 0 {
		// No CORS headers when no allowlist is configured.
		return func(c *fiber.Ctx) error { return c.Next() }
	}
	allowOrigins := strings.Join(headers, ",")
	return cors.New(cors.Config{
		AllowOrigins: allowOrigins,
		AllowMethods: "GET,POST,PUT,PATCH,DELETE,OPTIONS",
		AllowHeaders: strings.Join([]string{
			"Authorization",
			"Origin",
			"Content-Type",
			"Accept",
			"X-CSRF-Token",
			"X-Tenant-ID",
			"X-Email",
			"X-API-Key",
			"X-Admin-Key",
		}, ","),
		ExposeHeaders: "Authorization",
		MaxAge:        600,
	})
}

var (
	serverError       = errors.New("unable to connect to the DB")
	ErrCreateDbRow    = errors.New("unable to create a new db row/column")
	errNoServiceID    = errors.New("empty Service ID was entered")
	errObjectNotFound = errors.New("object not found")
)

var mobileRegex = regexp.MustCompile(`^[0-9]{10}$`)

func isValidMobile(mobile string) bool {
	return mobileRegex.MatchString(mobile)
}
