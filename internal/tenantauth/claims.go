package tenantauth

import (
	"errors"
	"slices"
	"time"
)

var (
	ErrInvalidClaims = errors.New("invalid tenant authentication claims")
	ErrInvalidRole   = errors.New("invalid tenant role")
)

// Role names are the exact values emitted for the noebs-api client. There are
// deliberately no aliases or implicit role hierarchy.
type Role string

const (
	RoleUser          Role = "user"
	RoleBackoffice    Role = "backoffice"
	RoleTenantAdmin   Role = "tenant-admin"
	RolePlatformAdmin Role = "platform-admin"
)

// ParseTenantRole accepts only tenant-scoped roles. PlatformAdmin is a realm
// role and must never be accepted from an organization's resource access.
func ParseTenantRole(raw string) (Role, error) {
	switch Role(raw) {
	case RoleUser, RoleBackoffice, RoleTenantAdmin:
		return Role(raw), nil
	default:
		return "", ErrInvalidRole
	}
}

// Organization is an immutable tenant membership extracted from one entry in
// Keycloak's organization claim.
type Organization struct {
	id    string
	roles []Role
}

func NewOrganization(id string, roles []Role) (Organization, error) {
	if id == "" {
		return Organization{}, ErrInvalidClaims
	}
	seen := make(map[Role]struct{}, len(roles))
	copyRoles := make([]Role, 0, len(roles))
	for _, role := range roles {
		if _, err := ParseTenantRole(string(role)); err != nil {
			return Organization{}, err
		}
		if _, exists := seen[role]; exists {
			continue
		}
		seen[role] = struct{}{}
		copyRoles = append(copyRoles, role)
	}
	slices.Sort(copyRoles)
	return Organization{id: id, roles: copyRoles}, nil
}

func (o Organization) ID() string { return o.id }

func (o Organization) Roles() []Role { return slices.Clone(o.roles) }

func (o Organization) has(role Role) bool {
	return slices.Contains(o.roles, role)
}

// Identity contains the immutable OIDC identity and token lifetime needed by
// downstream audit records.
type Identity struct {
	Issuer          string
	Subject         string
	AuthorizedParty string
	IssuedAt        time.Time
	ExpiresAt       time.Time
}

// Claims is the verified identity plus all tenant memberships in one access
// token. Its maps and role slices are copied at construction.
type Claims struct {
	identity      Identity
	organizations map[string]Organization
	platformAdmin bool
}

func NewClaims(identity Identity, organizations map[string]Organization, platformAdmin bool) (Claims, error) {
	if identity.Issuer == "" || identity.Subject == "" || identity.AuthorizedParty == "" ||
		identity.IssuedAt.IsZero() || identity.ExpiresAt.IsZero() || !identity.ExpiresAt.After(identity.IssuedAt) {
		return Claims{}, ErrInvalidClaims
	}
	copyOrganizations := make(map[string]Organization, len(organizations))
	for tenant, organization := range organizations {
		if tenant == "" || organization.id == "" {
			return Claims{}, ErrInvalidClaims
		}
		copyOrganizations[tenant] = Organization{
			id:    organization.id,
			roles: slices.Clone(organization.roles),
		}
	}
	return Claims{
		identity:      identity,
		organizations: copyOrganizations,
		platformAdmin: platformAdmin,
	}, nil
}

func (c Claims) Identity() Identity { return c.identity }

func (c Claims) PlatformAdmin() bool { return c.platformAdmin }
