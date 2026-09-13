package accountenrollment

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/adonese/noebs/internal/tenantcatalog"
)

type memoryStore struct {
	sync.Mutex
	rows map[Identity]Progress
}
type memoryProgress struct {
	store    *memoryStore
	identity Identity
}

func (m *memoryStore) WithIdentity(_ context.Context, id Identity, f func(ProgressAccess) error) error {
	m.Lock()
	defer m.Unlock()
	return f(memoryProgress{m, id})
}
func (m memoryProgress) Load(context.Context) (*Progress, error) {
	p, ok := m.store.rows[m.identity]
	if !ok {
		return nil, nil
	}
	return &p, nil
}
func (m memoryProgress) Save(_ context.Context, p Progress) error {
	m.store.rows[m.identity] = p
	return nil
}

type fakeAuthority struct {
	account Account
	grants  []string
	fail    string
}

func (a *fakeAuthority) Inspect(context.Context, string) (Account, error) { return a.account, nil }
func (a *fakeAuthority) EnsureUserMembership(_ context.Context, _ string, tenant string) error {
	if a.fail == tenant {
		return ErrUnavailable
	}
	a.grants = append(a.grants, tenant)
	a.account.Memberships[tenant] = []string{"user"}
	return nil
}

func enrollmentFixture(t *testing.T) (*Service, *fakeAuthority, *memoryStore, Identity) {
	t.Helper()
	catalog, err := tenantcatalog.New([]tenantcatalog.Tenant{{ID: "tenant-a", Name: "Personal A"}, {ID: "tenant-b", Name: "Personal B"}, {ID: "tenant-c", Name: "Personal C"}})
	if err != nil {
		t.Fatal(err)
	}
	policy := RuntimeConfig{Enabled: true, Issuer: "https://example.test/auth/realms/noebs", TenantIDs: []string{"tenant-a", "tenant-b"}, AllowedSubjects: []string{"subject"}, KeycloakBaseURL: "https://keycloak.test/auth", KeycloakClientID: "noebs-account-enroller", KeycloakClientSecret: "synthetic-secret"}
	authority := &fakeAuthority{account: Account{Email: "owner@example.test", EmailVerified: true, Fullname: "Owner Name", Memberships: map[string][]string{}}}
	store := &memoryStore{rows: map[Identity]Progress{}}
	service, err := New(policy, catalog, authority, store)
	if err != nil {
		t.Fatal(err)
	}
	return service, authority, store, Identity{Issuer: policy.Issuer, Subject: "subject", TenantID: "tenant-a"}
}

func TestEnrollmentSelectsExactlyCallerTenantAndPreservesRevocation(t *testing.T) {
	s, a, _, id := enrollmentFixture(t)
	before, err := s.Execute(t.Context(), id, false)
	if err != nil || before.EnrollmentStatus != "required" || len(a.grants) != 0 {
		t.Fatalf("read=%+v,%v", before, err)
	}
	first, err := s.Execute(t.Context(), id, true)
	if err != nil {
		t.Fatal(err)
	}
	if first.EnrollmentStatus != "complete" || !first.RefreshRequired || len(first.Tenants) != 1 || first.Tenants[0].ID != id.TenantID || first.PreferredTenantID == nil || *first.PreferredTenantID != id.TenantID {
		t.Fatalf("selected=%+v", first)
	}
	if !slices.Equal(a.grants, []string{"tenant-a"}) {
		t.Fatalf("granted other tenant: %+v", a.grants)
	}
	// Enrollment in B is independent of A; no picker or bulk membership assignment.
	secondID := id
	secondID.TenantID = "tenant-b"
	second, err := s.Execute(t.Context(), secondID, true)
	if err != nil || len(second.Tenants) != 1 || second.Tenants[0].ID != "tenant-b" {
		t.Fatalf("second=%+v,%v", second, err)
	}
	delete(a.account.Memberships, "tenant-a")
	revoked, err := s.Execute(t.Context(), id, true)
	if err != nil || revoked.EnrollmentStatus != "complete" || len(revoked.Tenants) != 0 || revoked.PreferredTenantID != nil || len(a.grants) != 2 {
		t.Fatalf("revoked=%+v,%v grants=%+v", revoked, err, a.grants)
	}
	if !slices.Equal(a.account.Memberships["tenant-b"], []string{"user"}) {
		t.Fatal("other tenant changed")
	}
}

func TestEnrollmentResumesFailureAndPreservesExistingOperatorClass(t *testing.T) {
	s, a, store, id := enrollmentFixture(t)
	a.fail = id.TenantID
	if _, err := s.Execute(t.Context(), id, true); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("failure=%v", err)
	}
	if p, ok := store.rows[id]; !ok || p.Complete {
		t.Fatalf("pending=%+v", p)
	}
	a.account.Memberships[id.TenantID] = []string{"tenant-admin"}
	a.fail = ""
	result, err := s.Execute(t.Context(), id, true)
	if err != nil || len(a.grants) != 0 || len(result.Tenants) != 0 || !slices.Equal(a.account.Memberships[id.TenantID], []string{"tenant-admin"}) {
		t.Fatalf("operator=%+v,%v", result, err)
	}
}

func TestEnrollmentRequiresExplicitKnownAllowedTenant(t *testing.T) {
	s, a, _, id := enrollmentFixture(t)
	for _, tenant := range []string{"", "unknown", " tenant-a "} {
		bad := id
		bad.TenantID = tenant
		if _, err := s.Execute(t.Context(), bad, true); !errors.Is(err, ErrInvalidTenant) {
			t.Fatalf("tenant %q=%v", tenant, err)
		}
	}
	id.TenantID = "tenant-c"
	if _, err := s.Execute(t.Context(), id, true); !errors.Is(err, ErrNotEligible) {
		t.Fatalf("closed tenant=%v", err)
	}
	if len(a.grants) != 0 {
		t.Fatal("invalid tenant granted membership")
	}
}

func TestEnrollmentEligibilityUsesAuthoritativeVerifiedEmail(t *testing.T) {
	s, a, _, id := enrollmentFixture(t)
	s.policy.AllowedSubjects = nil
	s.policy.AllowedVerifiedEmails = []string{"owner@example.test"}
	a.account.EmailVerified = false
	if _, err := s.Execute(t.Context(), id, true); !errors.Is(err, ErrNotEligible) {
		t.Fatalf("unverified email = %v", err)
	}
	a.account.EmailVerified = true
	a.account.Email = "other@example.test"
	if _, err := s.Execute(t.Context(), id, true); !errors.Is(err, ErrNotEligible) {
		t.Fatalf("other email = %v", err)
	}
	a.account.Email = "OWNER@example.test"
	if _, err := s.Execute(t.Context(), id, true); err != nil {
		t.Fatal(err)
	}
}

func TestEnrollmentPublicPolicyStillRequiresVerifiedMailbox(t *testing.T) {
	s, a, _, id := enrollmentFixture(t)
	s.policy.AllowedSubjects = nil
	s.policy.AllowVerifiedAccounts = true
	a.account.Email = ""
	if _, err := s.Execute(t.Context(), id, true); !errors.Is(err, ErrNotEligible) {
		t.Fatalf("phone-only = %v", err)
	}
	a.account.Email = "new@example.test"
	if _, err := s.Execute(t.Context(), id, true); err != nil {
		t.Fatal(err)
	}
}

func TestEnrollmentExistingMembersCanSignInWithoutNewGrants(t *testing.T) {
	s, a, _, id := enrollmentFixture(t)
	s.policy.AllowedSubjects = nil
	a.account.Memberships[id.TenantID] = []string{"user"}
	result, err := s.Execute(t.Context(), id, true)
	if err != nil || len(a.grants) != 0 || len(result.Tenants) != 1 || result.EnrollmentStatus != "complete" {
		t.Fatalf("existing member = %+v, %v", result, err)
	}
}

func TestEnrollmentConcurrentRetriesGrantOnce(t *testing.T) {
	s, a, _, id := enrollmentFixture(t)
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := s.Execute(context.Background(), id, true); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if len(a.grants) != 1 {
		t.Fatalf("grants = %+v", a.grants)
	}
}

func TestEnrollmentRejectsIdentityAndPolicyConfusion(t *testing.T) {
	s, _, _, id := enrollmentFixture(t)
	id.Issuer = "https://another.test/auth/realms/noebs"
	if _, err := s.Execute(t.Context(), id, true); !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("issuer=%v", err)
	}
	for _, mutate := range []func(*RuntimeConfig){func(p *RuntimeConfig) { p.AllowVerifiedAccounts = true }, func(p *RuntimeConfig) { p.TenantIDs = []string{"unknown"} }, func(p *RuntimeConfig) { p.KeycloakClientID = "noebs-keycloak-reconciler" }, func(p *RuntimeConfig) { p.KeycloakBaseURL = "http://keycloak.test/auth" }} {
		policy := s.policy
		mutate(&policy)
		if err := policy.Validate(s.catalog, true); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("policy accepted: %+v", policy)
		}
	}
}
