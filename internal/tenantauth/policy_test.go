package tenantauth

import (
	"errors"
	"testing"
	"time"
)

var policyTestNow = time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

func TestClaimsConstructorsRejectMissingIdentityAndRealmRoleInTenant(t *testing.T) {
	if _, err := NewOrganization("", []Role{RoleUser}); !errors.Is(err, ErrInvalidClaims) {
		t.Fatalf("empty organization error = %v, want invalid claims", err)
	}
	if _, err := NewOrganization("org-a", []Role{RolePlatformAdmin}); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("tenant platform-admin error = %v, want invalid role", err)
	}
	if _, err := NewClaims(Identity{}, nil, false); !errors.Is(err, ErrInvalidClaims) {
		t.Fatalf("empty identity error = %v, want invalid claims", err)
	}
}

func TestAuthorizeNeverUnionsRolesAcrossTenants(t *testing.T) {
	claims := policyTestClaims(t, map[string][]Role{
		"tenant-a": {RoleUser, RoleTenantAdmin},
		"tenant-b": {RoleUser},
	}, false)

	principal, err := Authorize(claims, "tenant-a", RoleTenantAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Tenant() != "tenant-a" || !principal.HasRole(RoleTenantAdmin) {
		t.Fatalf("principal = tenant %q roles %v", principal.Tenant(), principal.Roles())
	}
	if _, err := Authorize(claims, "tenant-b", RoleTenantAdmin); !errors.Is(err, ErrForbidden) {
		t.Fatalf("tenant-b admin error = %v, want forbidden", err)
	}
	if _, err := Authorize(claims, "tenant-b", RoleUser); err != nil {
		t.Fatalf("tenant-b user: %v", err)
	}
}

func TestPlatformAdminStillRequiresTenantMembership(t *testing.T) {
	claims := policyTestClaims(t, map[string][]Role{"tenant-a": {RoleUser}}, true)

	if _, err := Authorize(claims, "tenant-a", RolePlatformAdmin); err != nil {
		t.Fatalf("member platform admin: %v", err)
	}
	if _, err := Authorize(claims, "tenant-b", RolePlatformAdmin); !errors.Is(err, ErrUnknownTenant) {
		t.Fatalf("non-member platform admin error = %v, want unknown tenant", err)
	}
}

func TestAuthorizeRequiresExplicitTenantAndExactRole(t *testing.T) {
	claims := policyTestClaims(t, map[string][]Role{"tenant-a": {RoleBackoffice}}, false)

	if _, err := Authorize(claims, "", RoleBackoffice); !errors.Is(err, ErrMissingTenant) {
		t.Fatalf("missing tenant error = %v", err)
	}
	if _, err := Authorize(claims, "tenant-a", Role("operator")); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("role alias error = %v, want invalid policy", err)
	}
	if _, err := Authorize(claims, "tenant-a", RoleBackoffice, Role("operator")); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("partly invalid policy error = %v, want invalid policy", err)
	}
	if _, err := Authorize(claims, "tenant-a"); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("empty policy error = %v, want invalid policy", err)
	}
}

func TestClaimsCopyMemberships(t *testing.T) {
	organization, err := NewOrganization("org-a", []Role{RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	organizations := map[string]Organization{"tenant-a": organization}
	claims, err := NewClaims(policyTestIdentity(), organizations, false)
	if err != nil {
		t.Fatal(err)
	}
	delete(organizations, "tenant-a")

	principal, err := SelectTenant(claims, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	roles := principal.Roles()
	roles[0] = RoleTenantAdmin
	if principal.HasRole(RoleTenantAdmin) || !principal.HasRole(RoleUser) {
		t.Fatalf("principal roles were mutable: %v", principal.Roles())
	}
}

func BenchmarkAuthorizeTenantWarm(b *testing.B) {
	claims := policyTestClaims(b, map[string][]Role{
		"tenant-a": {RoleUser, RoleTenantAdmin},
		"tenant-b": {RoleUser},
	}, false)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := Authorize(claims, "tenant-a", RoleTenantAdmin); err != nil {
			b.Fatal(err)
		}
	}
}

func policyTestClaims(tb testing.TB, memberships map[string][]Role, platformAdmin bool) Claims {
	tb.Helper()
	organizations := make(map[string]Organization, len(memberships))
	for tenant, roles := range memberships {
		organization, err := NewOrganization("org-"+tenant, roles)
		if err != nil {
			tb.Fatal(err)
		}
		organizations[tenant] = organization
	}
	claims, err := NewClaims(policyTestIdentity(), organizations, platformAdmin)
	if err != nil {
		tb.Fatal(err)
	}
	return claims
}

func policyTestIdentity() Identity {
	return Identity{
		Issuer:          "https://identity.example/realms/noebs",
		Subject:         "08dc85cf-8f09-43c4-a840-3d8f17b5fac7",
		AuthorizedParty: "noebs-mobile",
		IssuedAt:        policyTestNow,
		ExpiresAt:       policyTestNow.Add(5 * time.Minute),
	}
}
