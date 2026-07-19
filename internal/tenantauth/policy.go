package tenantauth

import "errors"

var (
	ErrMissingTenant = errors.New("active tenant is required")
	ErrUnknownTenant = errors.New("subject is not a member of the active tenant")
	ErrInvalidPolicy = errors.New("invalid tenant authorization policy")
	ErrForbidden     = errors.New("tenant authorization denied")
)

// Principal is scoped to exactly one selected organization. Tenant roles from
// other organizations in the token are never visible through this type.
type Principal struct {
	identity      Identity
	tenant        string
	organization  Organization
	platformAdmin bool
}

func SelectTenant(claims Claims, activeTenant string) (Principal, error) {
	if activeTenant == "" {
		return Principal{}, ErrMissingTenant
	}
	organization, exists := claims.organizations[activeTenant]
	if !exists {
		return Principal{}, ErrUnknownTenant
	}
	return Principal{
		identity:      claims.identity,
		tenant:        activeTenant,
		organization:  organization,
		platformAdmin: claims.platformAdmin,
	}, nil
}

func Authorize(claims Claims, activeTenant string, allowed ...Role) (Principal, error) {
	principal, err := SelectTenant(claims, activeTenant)
	if err != nil {
		return Principal{}, err
	}
	if len(allowed) == 0 {
		return Principal{}, ErrInvalidPolicy
	}
	for _, role := range allowed {
		if !validAuthorizationRole(role) {
			return Principal{}, ErrInvalidPolicy
		}
	}
	for _, role := range allowed {
		if principal.HasRole(role) {
			return principal, nil
		}
	}
	return Principal{}, ErrForbidden
}

func (p Principal) Identity() Identity { return p.identity }

func (p Principal) Tenant() string { return p.tenant }

func (p Principal) OrganizationID() string { return p.organization.id }

func (p Principal) Roles() []Role { return p.organization.Roles() }

func (p Principal) HasRole(role Role) bool {
	if role == RolePlatformAdmin {
		return p.platformAdmin
	}
	return p.organization.has(role)
}

func validAuthorizationRole(role Role) bool {
	if role == RolePlatformAdmin {
		return true
	}
	_, err := ParseTenantRole(string(role))
	return err == nil
}
