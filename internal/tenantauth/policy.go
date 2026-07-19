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

// Policy is an immutable any-of role policy. Roles are validated once when the
// owning route or middleware is constructed.
type Policy struct {
	allowed []Role
}

func NewPolicy(allowed ...Role) (Policy, error) {
	if len(allowed) == 0 {
		return Policy{}, ErrInvalidPolicy
	}
	copyAllowed := make([]Role, len(allowed))
	copy(copyAllowed, allowed)
	for _, role := range copyAllowed {
		if !validAuthorizationRole(role) {
			return Policy{}, ErrInvalidPolicy
		}
	}
	return Policy{allowed: copyAllowed}, nil
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
	policy, err := NewPolicy(allowed...)
	if err != nil {
		return Principal{}, err
	}
	return policy.Authorize(claims, activeTenant)
}

func (p Policy) Authorize(claims Claims, activeTenant string) (Principal, error) {
	if len(p.allowed) == 0 {
		return Principal{}, ErrInvalidPolicy
	}
	principal, err := SelectTenant(claims, activeTenant)
	if err != nil {
		return Principal{}, err
	}
	for _, role := range p.allowed {
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
