package gateway

import (
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/workloadauth"
	"github.com/gofiber/fiber/v2"
)

const (
	GatewayTenantIDHeader        = workloadauth.HeaderTenantID
	GatewayIssuerHeader          = workloadauth.HeaderIssuer
	GatewaySubjectHeader         = workloadauth.HeaderSubject
	GatewayOrganizationIDHeader  = workloadauth.HeaderOrganizationID
	GatewayAuthorizedPartyHeader = workloadauth.HeaderAuthorizedParty
	GatewayRolesHeader           = workloadauth.HeaderRoles
	GatewayPermissionHeader      = workloadauth.HeaderPermission
	GatewayUserIDHeader          = workloadauth.HeaderUserID
	GatewaySourceIPHeader        = workloadauth.HeaderSourceIP
	GatewayTokenExpiresAtHeader  = workloadauth.HeaderTokenExpiresAt
)

type PrincipalHeaderValues struct {
	TenantID        string
	Issuer          string
	Subject         string
	OrganizationID  string
	AuthorizedParty string
	Roles           string
	Permission      string
	UserID          string
	SourceIP        string
	TokenExpiresAt  string
}

// PrincipalIdentity is the complete external authority accepted by a
// downstream workload. It is constructed only after the V2 workload signature
// has authenticated every field.
type PrincipalIdentity struct {
	TenantID        string
	Issuer          string
	Subject         string
	OrganizationID  string
	AuthorizedParty string
	UserID          int64
	SourceIP        string
	TokenExpiresAt  time.Time
	roles           []tenantauth.Role
	permission      tenantauth.Permission
}

func (p PrincipalIdentity) Roles() []tenantauth.Role {
	return slices.Clone(p.roles)
}

func (p PrincipalIdentity) HasRole(role tenantauth.Role) bool {
	return slices.Contains(p.roles, role)
}

func (p PrincipalIdentity) Permission() tenantauth.Permission {
	return p.permission
}

func (p PrincipalIdentity) HeaderValues() PrincipalHeaderValues {
	roles := make([]string, len(p.roles))
	for index, role := range p.roles {
		roles[index] = string(role)
	}
	values := PrincipalHeaderValues{
		TenantID:        p.TenantID,
		Issuer:          p.Issuer,
		Subject:         p.Subject,
		OrganizationID:  p.OrganizationID,
		AuthorizedParty: p.AuthorizedParty,
		Roles:           strings.Join(roles, ","),
		Permission:      string(p.permission),
		SourceIP:        p.SourceIP,
		TokenExpiresAt:  strconv.FormatInt(p.TokenExpiresAt.Unix(), 10),
	}
	if p.UserID != 0 {
		values.UserID = strconv.FormatInt(p.UserID, 10)
	}
	return values
}

type principalIdentityLocalKey struct{}

var internalPrincipalIdentityLocal principalIdentityLocalKey

// InternalTenantIdentityMiddleware is for signed machine requests that carry
// tenant scope but no human principal, such as verified provider webhooks.
func InternalTenantIdentityMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		tenantID, err := ParseInternalTenantIdentity(c.Get(GatewayTenantIDHeader))
		if err != nil {
			return unauthorizedGatewayIdentity(c)
		}
		c.Locals("tenant_id", tenantID)
		if err := bindGatewayRequestSource(c, c.Get(GatewaySourceIPHeader)); err != nil {
			return unauthorizedGatewayIdentity(c)
		}
		return c.Next()
	}
}

func InternalPrincipalIdentityMiddleware() fiber.Handler {
	return internalPrincipalIdentityMiddleware(false, time.Now)
}

func InternalUserIdentityMiddleware() fiber.Handler {
	return internalPrincipalIdentityMiddleware(true, time.Now)
}

func internalPrincipalIdentityMiddleware(requireUser bool, now func() time.Time) fiber.Handler {
	return func(c *fiber.Ctx) error {
		identity, err := ParseInternalPrincipalIdentity(PrincipalHeaderValues{
			TenantID:        c.Get(GatewayTenantIDHeader),
			Issuer:          c.Get(GatewayIssuerHeader),
			Subject:         c.Get(GatewaySubjectHeader),
			OrganizationID:  c.Get(GatewayOrganizationIDHeader),
			AuthorizedParty: c.Get(GatewayAuthorizedPartyHeader),
			Roles:           c.Get(GatewayRolesHeader),
			Permission:      c.Get(GatewayPermissionHeader),
			UserID:          c.Get(GatewayUserIDHeader),
			SourceIP:        c.Get(GatewaySourceIPHeader),
			TokenExpiresAt:  c.Get(GatewayTokenExpiresAtHeader),
		}, now().UTC())
		if err != nil || requireUser && (identity.UserID == 0 || !identity.HasRole(tenantauth.RoleUser)) {
			return unauthorizedGatewayIdentity(c)
		}
		c.Locals(internalPrincipalIdentityLocal, identity)
		c.Locals("tenant_id", identity.TenantID)
		if identity.UserID != 0 {
			c.Locals("user_id", identity.UserID)
		}
		c.Locals("request_source", identity.SourceIP)
		return c.Next()
	}
}

func InternalPrincipalIdentity(c *fiber.Ctx) (PrincipalIdentity, bool) {
	identity, ok := c.Locals(internalPrincipalIdentityLocal).(PrincipalIdentity)
	return identity, ok
}

func ParseInternalTenantIdentity(rawTenantID string) (string, error) {
	return validateTenantID(rawTenantID)
}

func ParseInternalPrincipalIdentity(values PrincipalHeaderValues, now time.Time) (PrincipalIdentity, error) {
	tenantID, err := validateTenantID(values.TenantID)
	if err != nil || tenantID != values.TenantID || !validIssuer(values.Issuer) ||
		!validPrincipalText(values.Subject, 512) || !validPrincipalText(values.OrganizationID, 512) ||
		!validPrincipalText(values.AuthorizedParty, 255) {
		return PrincipalIdentity{}, ErrInvalidUserIdentity
	}
	roles, err := parsePrincipalRoles(values.Roles)
	if err != nil {
		return PrincipalIdentity{}, ErrInvalidUserIdentity
	}
	var permission tenantauth.Permission
	if values.Permission != "" {
		permission, err = tenantauth.ParsePermission(values.Permission)
		if err != nil {
			return PrincipalIdentity{}, ErrInvalidUserIdentity
		}
	}
	userID, err := parseOptionalGatewayUserID(values.UserID)
	if err != nil {
		return PrincipalIdentity{}, ErrInvalidUserIdentity
	}
	source := net.ParseIP(values.SourceIP)
	if source == nil || source.String() != values.SourceIP {
		return PrincipalIdentity{}, ErrInvalidUserIdentity
	}
	expiresUnix, err := strconv.ParseInt(values.TokenExpiresAt, 10, 64)
	if err != nil || strconv.FormatInt(expiresUnix, 10) != values.TokenExpiresAt {
		return PrincipalIdentity{}, ErrInvalidUserIdentity
	}
	expiresAt := time.Unix(expiresUnix, 0).UTC()
	if now.IsZero() || !expiresAt.After(now) {
		return PrincipalIdentity{}, ErrInvalidUserIdentity
	}
	return PrincipalIdentity{
		TenantID:        tenantID,
		Issuer:          values.Issuer,
		Subject:         values.Subject,
		OrganizationID:  values.OrganizationID,
		AuthorizedParty: values.AuthorizedParty,
		UserID:          userID,
		SourceIP:        values.SourceIP,
		TokenExpiresAt:  expiresAt,
		roles:           roles,
		permission:      permission,
	}, nil
}

func validIssuer(raw string) bool {
	issuer, err := url.Parse(raw)
	return err == nil && issuer.Scheme == "https" && issuer.Host != "" && issuer.User == nil &&
		issuer.RawQuery == "" && issuer.Fragment == "" && issuer.String() == raw
}

func validPrincipalText(raw string, maxBytes int) bool {
	return raw != "" && len(raw) <= maxBytes && utf8.ValidString(raw) && raw == strings.TrimSpace(raw) &&
		strings.IndexFunc(raw, unicode.IsControl) < 0
}

func parsePrincipalRoles(raw string) ([]tenantauth.Role, error) {
	if raw == "" {
		return nil, ErrInvalidUserIdentity
	}
	parts := strings.Split(raw, ",")
	roles := make([]tenantauth.Role, len(parts))
	for index, part := range parts {
		parsed, err := tenantauth.ParseTenantRole(part)
		if err != nil {
			return nil, err
		}
		roles[index] = parsed
	}
	slices.Sort(roles)
	encoded := make([]string, len(roles))
	for index, role := range roles {
		if index != 0 && role == roles[index-1] {
			return nil, ErrInvalidUserIdentity
		}
		encoded[index] = string(role)
	}
	if strings.Join(encoded, ",") != raw {
		return nil, ErrInvalidUserIdentity
	}
	return roles, nil
}

func parseOptionalGatewayUserID(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	userID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || userID <= 0 || strconv.FormatInt(userID, 10) != raw {
		return 0, ErrInvalidUserIdentity
	}
	return userID, nil
}

func bindGatewayRequestSource(c *fiber.Ctx, sourceIP string) error {
	if sourceIP == "" {
		return nil
	}
	parsed := net.ParseIP(sourceIP)
	if parsed == nil || parsed.String() != sourceIP {
		return ErrInvalidUserIdentity
	}
	c.Locals("request_source", sourceIP)
	return nil
}

func unauthorizedGatewayIdentity(c *fiber.Ctx) error {
	return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"message": "missing gateway identity", "code": "unauthorized"})
}
