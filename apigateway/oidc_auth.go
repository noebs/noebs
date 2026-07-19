package gateway

import (
	"errors"
	"net/http"

	"github.com/adonese/noebs/internal/oidcauth"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/gofiber/fiber/v2"
)

var ErrInvalidOIDCAuthConfiguration = errors.New("invalid API gateway OIDC authentication configuration")

// ActiveTenantSelector owns how a route obtains its requested tenant. The
// middleware never falls back to a configured or token-derived tenant.
type ActiveTenantSelector func(*fiber.Ctx) (string, error)

type OIDCAuthConfig struct {
	Verifier     *oidcauth.Verifier
	SelectTenant ActiveTenantSelector
	AllowedRoles []tenantauth.Role
}

type oidcPrincipalLocalKey struct{}

var principalLocalKey oidcPrincipalLocalKey

func NewOIDCAuthMiddleware(config OIDCAuthConfig) (fiber.Handler, error) {
	if config.Verifier == nil || config.SelectTenant == nil {
		return nil, ErrInvalidOIDCAuthConfiguration
	}
	policy, err := tenantauth.NewPolicy(config.AllowedRoles...)
	if err != nil {
		return nil, ErrInvalidOIDCAuthConfiguration
	}
	return func(c *fiber.Ctx) error {
		authorizationValues := c.Request().Header.PeekAll(fiber.HeaderAuthorization)
		if len(authorizationValues) != 1 {
			return oidcAuthenticationFailure(c)
		}
		claims, err := config.Verifier.VerifyBearer(c.UserContext(), string(authorizationValues[0]))
		if err != nil {
			return oidcAuthenticationFailure(c)
		}
		activeTenant, err := config.SelectTenant(c)
		if err != nil {
			return oidcAuthorizationFailure(c)
		}
		principal, err := policy.Authorize(claims, activeTenant)
		if err != nil {
			return oidcAuthorizationFailure(c)
		}
		setOIDCPrincipal(c, principal)
		return c.Next()
	}, nil
}

// OIDCPrincipal returns the verified principal placed by
// NewOIDCAuthMiddleware. The unexported local key prevents handlers from
// confusing caller-controlled locals with an authenticated principal.
func OIDCPrincipal(c *fiber.Ctx) (tenantauth.Principal, bool) {
	principal, ok := c.Locals(principalLocalKey).(tenantauth.Principal)
	return principal, ok
}

func setOIDCPrincipal(c *fiber.Ctx, principal tenantauth.Principal) {
	c.Locals(principalLocalKey, principal)
}

func oidcAuthenticationFailure(c *fiber.Ctx) error {
	c.Set(fiber.HeaderWWWAuthenticate, "Bearer")
	return c.Status(http.StatusUnauthorized).JSON(fiber.Map{
		"code":    "authentication_failed",
		"message": "authentication failed",
	})
}

func oidcAuthorizationFailure(c *fiber.Ctx) error {
	return c.Status(http.StatusForbidden).JSON(fiber.Map{
		"code":    "authorization_denied",
		"message": "authorization denied",
	})
}
