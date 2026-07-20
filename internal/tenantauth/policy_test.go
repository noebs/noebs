package tenantauth

import (
	"errors"
	"testing"
	"time"
)

var policyTestNow = time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

func TestClaimsConstructorsRejectMissingIdentityAndRealmRoleInTenant(t *testing.T) {
	if _, err := NewOrganization("", []Role{RoleUser}, nil); !errors.Is(err, ErrInvalidClaims) {
		t.Fatalf("empty organization error = %v, want invalid claims", err)
	}
	if _, err := NewOrganizationClaim("", []string{"user"}); !errors.Is(err, ErrInvalidClaims) {
		t.Fatalf("empty organization claim error = %v, want invalid claims", err)
	}
	if _, err := NewOrganization("org-a", []Role{Role("platform-admin")}, nil); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("unknown role error = %v, want invalid role", err)
	}
	if _, err := NewClaims(Identity{}, nil); !errors.Is(err, ErrInvalidClaims) {
		t.Fatalf("empty identity error = %v, want invalid claims", err)
	}
}

func TestClaimsRejectUnknownRoleOnlyWhenItsTenantIsSelected(t *testing.T) {
	valid, err := NewOrganizationClaim("org-a", []string{"user", "wallet:read"})
	if err != nil {
		t.Fatal(err)
	}
	invalid, err := NewOrganizationClaim("org-b", []string{"user", "wallet:unknown"})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := NewClaims(policyTestIdentity(), map[string]Organization{
		"tenant-a": valid,
		"tenant-b": invalid,
	})
	if err != nil {
		t.Fatal(err)
	}

	principal, err := SelectTenant(claims, "tenant-a")
	if err != nil {
		t.Fatalf("select valid tenant: %v", err)
	}
	if !principal.HasRole(RoleUser) || !principal.HasPermission(PermissionWalletRead) {
		t.Fatalf("principal roles = %v, permissions = %v", principal.Roles(), principal.Permissions())
	}
	if _, err := SelectTenant(claims, "tenant-b"); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("select invalid tenant error = %v, want invalid role", err)
	}
	memberships := claims.Memberships()
	if len(memberships) != 1 || memberships[0].TenantID != "tenant-a" {
		t.Fatalf("memberships = %+v, want only tenant-a", memberships)
	}
}

func TestAuthorizeNeverUnionsRolesAcrossTenants(t *testing.T) {
	claims := policyTestClaims(t, map[string][]Role{
		"tenant-a": {RoleUser, RoleTenantAdmin},
		"tenant-b": {RoleUser},
	})

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

func TestAuthorizationRejectsUnmodeledGlobalRoles(t *testing.T) {
	claims := policyTestClaims(t, map[string][]Role{"tenant-a": {RoleUser}})
	if _, err := Authorize(claims, "tenant-a", Role("platform-admin")); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("unmodeled global role error = %v, want invalid policy", err)
	}
}

func TestAuthorizeRequiresExplicitTenantAndExactRole(t *testing.T) {
	claims := policyTestClaims(t, map[string][]Role{"tenant-a": {RoleBackoffice}})

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

func TestPolicyCopiesConfiguredRoles(t *testing.T) {
	allowed := []Role{RoleUser}
	policy, err := NewPolicy(allowed...)
	if err != nil {
		t.Fatal(err)
	}
	allowed[0] = RoleTenantAdmin

	claims := policyTestClaims(t, map[string][]Role{"tenant-a": {RoleUser}})
	if _, err := policy.Authorize(claims, "tenant-a"); err != nil {
		t.Fatalf("policy changed with caller-owned slice: %v", err)
	}
}

func TestClaimsCopyMemberships(t *testing.T) {
	organization, err := NewOrganization("org-a", []Role{RoleUser}, []Permission{PermissionWalletRead})
	if err != nil {
		t.Fatal(err)
	}
	organizations := map[string]Organization{"tenant-a": organization}
	claims, err := NewClaims(policyTestIdentity(), organizations)
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
	permissions := principal.Permissions()
	permissions[0] = PermissionWalletFeesWrite
	if principal.HasPermission(PermissionWalletFeesWrite) || !principal.HasPermission(PermissionWalletRead) {
		t.Fatalf("principal permissions were mutable: %v", principal.Permissions())
	}
}

func TestClaimsMembershipsAreSortedAndImmutable(t *testing.T) {
	organizations := make(map[string]Organization)
	var err error
	organizations["tenant-b"], err = NewOrganization("org-b", []Role{RoleUser}, nil)
	if err != nil {
		t.Fatal(err)
	}
	organizations["tenant-a"], err = NewOrganization("org-a", []Role{RoleBackoffice}, []Permission{PermissionReportingRead})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := NewClaims(policyTestIdentity(), organizations)
	if err != nil {
		t.Fatal(err)
	}
	memberships := claims.Memberships()
	if len(memberships) != 2 || memberships[0].TenantID != "tenant-a" || memberships[1].TenantID != "tenant-b" {
		t.Fatalf("memberships = %+v", memberships)
	}
	memberships[0].Roles[0] = RoleTenantAdmin
	memberships[0].Permissions[0] = PermissionWalletFeesWrite
	again := claims.Memberships()
	if again[0].Roles[0] != RoleBackoffice || again[0].Permissions[0] != PermissionReportingRead {
		t.Fatalf("claims changed through membership result: %+v", again[0])
	}
}

func TestPermissionPolicyNeverUnionsCapabilitiesAcrossTenants(t *testing.T) {
	organizations := map[string]Organization{}
	var err error
	organizations["tenant-a"], err = NewOrganization("org-a", []Role{RoleTenantAdmin}, []Permission{PermissionWalletWorkflowApprove})
	if err != nil {
		t.Fatal(err)
	}
	organizations["tenant-b"], err = NewOrganization("org-b", []Role{RoleTenantAdmin}, []Permission{PermissionWalletWorkflowReject})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := NewClaims(policyTestIdentity(), organizations)
	if err != nil {
		t.Fatal(err)
	}
	approve, err := NewPermissionPolicy(PermissionWalletWorkflowApprove, RoleTenantAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := approve.Authorize(claims, "tenant-a"); err != nil {
		t.Fatalf("tenant-a approval: %v", err)
	}
	if _, err := approve.Authorize(claims, "tenant-b"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("tenant-b approval error = %v, want forbidden", err)
	}
	if _, err := NewPermissionPolicy(Permission("wallet:all"), RoleTenantAdmin); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("unknown permission error = %v, want invalid policy", err)
	}
}

func BenchmarkAuthorizeTenantWarm(b *testing.B) {
	claims := policyTestClaims(b, map[string][]Role{
		"tenant-a": {RoleUser, RoleTenantAdmin},
		"tenant-b": {RoleUser},
	})
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := Authorize(claims, "tenant-a", RoleTenantAdmin); err != nil {
			b.Fatal(err)
		}
	}
}

func policyTestClaims(tb testing.TB, memberships map[string][]Role) Claims {
	tb.Helper()
	organizations := make(map[string]Organization, len(memberships))
	for tenant, roles := range memberships {
		organization, err := NewOrganization("org-"+tenant, roles, nil)
		if err != nil {
			tb.Fatal(err)
		}
		organizations[tenant] = organization
	}
	claims, err := NewClaims(policyTestIdentity(), organizations)
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
