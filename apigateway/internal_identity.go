package gateway

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
)

const (
	GatewayTenantIDHeader         = "X-Noebs-Tenant-ID"
	GatewayIssuerHeader           = "X-Noebs-Issuer"
	GatewaySubjectHeader          = "X-Noebs-Subject"
	GatewayUserIDHeader           = "X-Noebs-User-ID"
	GatewayMobileHeader           = "X-Noebs-Mobile"
	GatewaySessionEpochHeader     = "X-Noebs-Session-Epoch"
	GatewaySessionTokenHeader     = "X-Noebs-Session-Token"
	GatewaySourceIPHeader         = "X-Noebs-Source-IP"
	GatewayAdminIdentityHeader    = "X-Noebs-Admin-Identity"
	GatewayAdminIdentityValue     = "gateway-admin"
	GatewayAdminRoleHeader        = "X-Noebs-Admin-Role"
	GatewayAdminRoleValue         = "admin"
	GatewayAdminPermissionsHeader = "X-Noebs-Admin-Permissions"
)

type UserIdentity struct {
	TenantID     string
	UserID       int64
	Mobile       string
	SessionEpoch int64
}

// PrincipalIdentity is the verified external identity forwarded by the API
// gateway. Services must only construct it after the signed workload boundary.
type PrincipalIdentity struct {
	TenantID string
	Issuer   string
	Subject  string
}

type principalIdentityLocalKey struct{}

var internalPrincipalIdentityLocal principalIdentityLocalKey

// InternalTenantIdentityMiddleware binds a gateway-issued tenant identity header
// to Fiber locals for public service routes that still require tenant scope.
func InternalTenantIdentityMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		tenantID, err := ParseInternalTenantIdentity(c.Get(GatewayTenantIDHeader))
		if err != nil {
			return unauthorizedGatewayIdentity(c)
		}
		c.Locals("tenant_id", tenantID)
		if err := bindGatewayRequestSource(c); err != nil {
			return unauthorizedGatewayIdentity(c)
		}
		return c.Next()
	}
}

// InternalPrincipalIdentityMiddleware binds a gateway-verified OIDC principal
// to a typed local. Mount it only behind signedWorkloadBoundary.
func InternalPrincipalIdentityMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		identity, err := ParseInternalPrincipalIdentity(
			c.Get(GatewayTenantIDHeader),
			c.Get(GatewayIssuerHeader),
			c.Get(GatewaySubjectHeader),
		)
		if err != nil {
			return unauthorizedGatewayIdentity(c)
		}
		c.Locals(internalPrincipalIdentityLocal, identity)
		c.Locals("tenant_id", identity.TenantID)
		if err := bindGatewayRequestSource(c); err != nil {
			return unauthorizedGatewayIdentity(c)
		}
		return c.Next()
	}
}

func InternalPrincipalIdentity(c *fiber.Ctx) (PrincipalIdentity, bool) {
	identity, ok := c.Locals(internalPrincipalIdentityLocal).(PrincipalIdentity)
	return identity, ok
}

// InternalUserIdentityMiddleware binds the gateway-issued user identity headers
// to Fiber locals for service-owned user routes.
func InternalUserIdentityMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		identity, err := ParseInternalUserIdentity(
			c.Get(GatewayTenantIDHeader),
			c.Get(GatewayUserIDHeader),
			c.Get(GatewayMobileHeader),
		)
		if err != nil {
			return unauthorizedGatewayIdentity(c)
		}

		c.Locals("tenant_id", identity.TenantID)
		c.Locals("user_id", identity.UserID)
		if identity.Mobile != "" {
			c.Locals("mobile", identity.Mobile)
			c.Locals("username", identity.Mobile)
		}
		if rawEpoch := strings.TrimSpace(c.Get(GatewaySessionEpochHeader)); rawEpoch != "" {
			epoch, err := parseGatewaySessionEpoch(rawEpoch)
			if err != nil {
				return unauthorizedGatewayIdentity(c)
			}
			c.Locals("session_epoch", epoch)
		}
		if token := strings.TrimSpace(c.Get(GatewaySessionTokenHeader)); token != "" {
			c.Locals("session_token", token)
		}
		if err := bindGatewayRequestSource(c); err != nil {
			return unauthorizedGatewayIdentity(c)
		}
		return c.Next()
	}
}

func bindGatewayRequestSource(c *fiber.Ctx) error {
	sourceIP := strings.TrimSpace(c.Get(GatewaySourceIPHeader))
	if sourceIP == "" {
		return nil
	}
	parsed := net.ParseIP(sourceIP)
	if parsed == nil {
		return ErrInvalidUserIdentity
	}
	c.Locals("request_source", parsed.String())
	return nil
}

func InternalAdminIdentityMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if c.Get(GatewayAdminIdentityHeader) != GatewayAdminIdentityValue {
			return unauthorizedGatewayIdentity(c)
		}
		c.Locals("admin_identity", true)
		return c.Next()
	}
}

func ParseInternalTenantIdentity(rawTenantID string) (string, error) {
	return validateTenantID(rawTenantID)
}

func ParseInternalPrincipalIdentity(rawTenantID, rawIssuer, rawSubject string) (PrincipalIdentity, error) {
	tenantID, err := validateTenantID(rawTenantID)
	if err != nil || tenantID != rawTenantID {
		return PrincipalIdentity{}, ErrInvalidUserIdentity
	}
	issuer, err := url.Parse(rawIssuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil ||
		issuer.RawQuery != "" || issuer.Fragment != "" || rawIssuer != strings.TrimSpace(rawIssuer) {
		return PrincipalIdentity{}, ErrInvalidUserIdentity
	}
	if rawSubject == "" || len(rawSubject) > 255 || !utf8.ValidString(rawSubject) ||
		rawSubject != strings.TrimSpace(rawSubject) || strings.IndexFunc(rawSubject, unicode.IsControl) >= 0 {
		return PrincipalIdentity{}, ErrInvalidUserIdentity
	}
	return PrincipalIdentity{TenantID: tenantID, Issuer: rawIssuer, Subject: rawSubject}, nil
}

func ParseInternalUserIdentity(rawTenantID, rawUserID, rawMobile string) (UserIdentity, error) {
	tenantID, err := validateTenantID(rawTenantID)
	if err != nil {
		return UserIdentity{}, err
	}
	userID, err := parseGatewayUserID(rawUserID)
	if err != nil {
		return UserIdentity{}, err
	}
	mobile := strings.TrimSpace(rawMobile)
	if mobile != "" && !isValidMobile(mobile) {
		return UserIdentity{}, ErrInvalidUserIdentity
	}
	return UserIdentity{TenantID: tenantID, UserID: userID, Mobile: mobile}, nil
}

func parseGatewayUserID(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	userID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	if userID <= 0 {
		return 0, ErrInvalidUserIdentity
	}
	return userID, nil
}

func parseGatewaySessionEpoch(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	epoch, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || epoch <= 0 {
		return 0, ErrInvalidUserIdentity
	}
	return epoch, nil
}

func unauthorizedGatewayIdentity(c *fiber.Ctx) error {
	return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"message": "missing gateway identity", "code": "unauthorized"})
}
