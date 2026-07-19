package tenantauth

import (
	"errors"
	"slices"
	"time"
)

var (
	ErrInvalidClaims     = errors.New("invalid tenant authentication claims")
	ErrInvalidRole       = errors.New("invalid tenant role")
	ErrInvalidPermission = errors.New("invalid tenant permission")
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

// Permission names are noebs-api client roles attached to organization
// groups. They are separate from membership roles so a route can require both
// an operator class and one exact capability.
type Permission string

const (
	PermissionReportingRead         Permission = "reporting:read"
	PermissionWalletRead            Permission = "wallet:read"
	PermissionWalletAuditRead       Permission = "wallet:audit:read"
	PermissionWalletManualCreate    Permission = "wallet:manual:create"
	PermissionWalletFeesWrite       Permission = "wallet:fees:write"
	PermissionWalletRatesWrite      Permission = "wallet:rates:write"
	PermissionWalletWorkflowApprove Permission = "wallet:workflow:approve"
	PermissionWalletWorkflowReject  Permission = "wallet:workflow:reject"
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

func ParsePermission(raw string) (Permission, error) {
	switch Permission(raw) {
	case PermissionReportingRead,
		PermissionWalletRead,
		PermissionWalletAuditRead,
		PermissionWalletManualCreate,
		PermissionWalletFeesWrite,
		PermissionWalletRatesWrite,
		PermissionWalletWorkflowApprove,
		PermissionWalletWorkflowReject:
		return Permission(raw), nil
	default:
		return "", ErrInvalidPermission
	}
}

// Organization is an immutable tenant membership extracted from one entry in
// Keycloak's organization claim.
type Organization struct {
	id          string
	roles       []Role
	permissions []Permission
}

func NewOrganization(id string, roles []Role, permissions []Permission) (Organization, error) {
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
	seenPermissions := make(map[Permission]struct{}, len(permissions))
	copyPermissions := make([]Permission, 0, len(permissions))
	for _, permission := range permissions {
		if _, err := ParsePermission(string(permission)); err != nil {
			return Organization{}, err
		}
		if _, exists := seenPermissions[permission]; exists {
			continue
		}
		seenPermissions[permission] = struct{}{}
		copyPermissions = append(copyPermissions, permission)
	}
	slices.Sort(copyPermissions)
	return Organization{id: id, roles: copyRoles, permissions: copyPermissions}, nil
}

func (o Organization) ID() string { return o.id }

func (o Organization) Roles() []Role { return slices.Clone(o.roles) }

func (o Organization) Permissions() []Permission { return slices.Clone(o.permissions) }

func (o Organization) has(role Role) bool {
	return slices.Contains(o.roles, role)
}

func (o Organization) permits(permission Permission) bool {
	return slices.Contains(o.permissions, permission)
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

type Membership struct {
	TenantID       string
	OrganizationID string
	Roles          []Role
	Permissions    []Permission
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
			id:          organization.id,
			roles:       slices.Clone(organization.roles),
			permissions: slices.Clone(organization.permissions),
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

func (c Claims) Memberships() []Membership {
	tenants := make([]string, 0, len(c.organizations))
	for tenant := range c.organizations {
		tenants = append(tenants, tenant)
	}
	slices.Sort(tenants)
	memberships := make([]Membership, 0, len(tenants))
	for _, tenant := range tenants {
		organization := c.organizations[tenant]
		memberships = append(memberships, Membership{
			TenantID:       tenant,
			OrganizationID: organization.id,
			Roles:          slices.Clone(organization.roles),
			Permissions:    slices.Clone(organization.permissions),
		})
	}
	return memberships
}
