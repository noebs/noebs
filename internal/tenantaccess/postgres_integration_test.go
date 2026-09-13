package tenantaccess

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/accountenrollment"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/adonese/noebs/store"
	"github.com/google/uuid"
)

const fixtureIssuer = "https://accounts.example.test/auth/realms/noebs"

type fixtureAuthority struct {
	mu       sync.Mutex
	accounts map[string]AuthorityAccount
	markers  map[string]string
	fail     bool
	calls    int
	before   func()
}

func (a *fixtureAuthority) Account(_ context.Context, tenant, subject string) (AuthorityAccount, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	v := a.accounts[tenant+subject]
	v.Subject = subject
	v.Roles = slices.Clone(v.Roles)
	return v, nil
}
func (a *fixtureAuthority) Members(ctx context.Context, tenant string) ([]AuthorityAccount, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := []AuthorityAccount{}
	for key, v := range a.accounts {
		if strings.HasPrefix(key, tenant) && v.Member {
			result = append(result, v)
		}
	}
	return result, nil
}
func (a *fixtureAuthority) SubjectExists(_ context.Context, subject string) (bool, error) {
	return canonicalID(subject), nil
}
func (a *fixtureAuthority) ApplyDelta(_ context.Context, tenant, subject string, grant, revoke []tenantauth.Role) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if a.before != nil {
		a.before()
	}
	if a.fail {
		return errors.New("synthetic authority interruption")
	}
	v := a.accounts[tenant+subject]
	v.Subject = subject
	if len(grant) > 0 {
		v.Member = true
	}
	v.Roles = changedRoles(v.Roles, grant, revoke)
	a.accounts[tenant+subject] = v
	return nil
}
func (a *fixtureAuthority) Inspect(_ context.Context, subject string) (accountenrollment.Account, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := accountenrollment.Account{Email: "fixture@example.test", EmailVerified: true, Fullname: "Fixture", Memberships: map[string][]string{}, BootstrapOperationID: a.markers[subject]}
	for key, v := range a.accounts {
		if strings.HasSuffix(key, subject) && v.Member {
			roles := []string{}
			for _, role := range v.Roles {
				roles = append(roles, string(role))
			}
			result.Memberships[strings.TrimSuffix(key, subject)] = roles
		}
	}
	return result, nil
}
func (a *fixtureAuthority) EnsureUserMembership(ctx context.Context, subject, tenant string) error {
	return a.ApplyDelta(ctx, tenant, subject, []tenantauth.Role{tenantauth.RoleUser}, []tenantauth.Role{})
}

func postgresFixture(t *testing.T) (*store.DB, *store.DB) {
	t.Helper()
	dsn := os.Getenv("TEST_TENANT_ACCESS_POSTGRES_URL")
	if dsn == "" {
		t.Skip("set TEST_TENANT_ACCESS_POSTGRES_URL to an isolated native PostgreSQL fixture")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Hostname() != "127.0.0.1" || u.User.Username() != "access_fixture" || u.Path != "/postgres" {
		t.Fatal("fixture must be isolated loopback access_fixture/postgres")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	for _, role := range []string{"identity_auth_migrate", "identity_auth_runtime"} {
		var exists bool
		if err = admin.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, role).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			if _, err = admin.Exec(`CREATE ROLE ` + role + ` LOGIN`); err != nil {
				t.Fatal(err)
			}
		}
	}
	var exists bool
	if err = admin.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname='identity_auth')`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		if _, err = admin.Exec(`CREATE DATABASE identity_auth OWNER identity_auth_migrate`); err != nil {
			t.Fatal(err)
		}
	}
	open := func(role string) *store.DB {
		t.Helper()
		copy := *u
		copy.User = url.User(role)
		copy.Path = "/identity_auth"
		db, err := store.OpenFromConfig(copy.String(), "postgres")
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(12)
		t.Cleanup(func() { db.Close() })
		return db
	}
	migration := open("identity_auth_migrate")
	if _, err = migration.Exec(`ALTER SCHEMA public OWNER TO identity_auth_migrate`); err != nil {
		t.Fatal(err)
	}
	if err = store.MigrateScope(t.Context(), migration, store.MigrationScopeIdentityAuth); err != nil {
		t.Fatal(err)
	}
	if _, err = migration.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tenant-a','Fixture A',now()),('tenant-b','Fixture B',now()),('tenant-c','Fixture C',now()) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	runtime := open("identity_auth_runtime")
	return migration, runtime
}
func TestPostgresTenantAccessAuthorityAndRecovery(t *testing.T) {
	migrate, runtime := postgresFixture(t)
	catalog, _ := tenantcatalog.New([]tenantcatalog.Tenant{{ID: "tenant-a", Name: "Fixture A"}, {ID: "tenant-b", Name: "Fixture B"}, {ID: "tenant-c", Name: "Fixture C"}})
	enrollment, _ := accountenrollment.NewPostgresStore(runtime.DB.DB)
	journal, _ := NewPostgresJournal(runtime.DB.DB)
	authority := &fixtureAuthority{accounts: map[string]AuthorityAccount{}}
	service, err := New(Config{Issuer: fixtureIssuer, Catalog: catalog, Enrollment: enrollment, Journal: journal, Authority: authority, Clock: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{Issuer: fixtureIssuer, Subject: uuid.NewString(), TenantID: "tenant-a", Roles: []tenantauth.Role{tenantauth.RoleTenantAdmin}, Permissions: []tenantauth.Permission{tenantauth.PermissionIdentityAccessRead, tenantauth.PermissionIdentityAccessWrite}, SourceIP: "100.64.0.2", RequestID: "fixture-initial"}
	inspect := func(subject string) Account {
		t.Helper()
		v, err := service.Inspect(t.Context(), actor, subject)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	request := func(subject string, grant, revoke []tenantauth.Role) ChangeRequest {
		return ChangeRequest{OperationID: uuid.NewString(), TargetSubject: subject, ExpectedRevision: inspect(subject).Revision, GrantRoles: grant, RevokeRoles: revoke, Reason: "Isolated access fixture"}
	}
	t.Run("migration owner and immutable audit authority", func(t *testing.T) {
		if err := store.MigrateScope(t.Context(), runtime, store.MigrationScopeIdentityAuth); err == nil {
			t.Fatal("runtime migrated schema")
		}
		for _, query := range []string{`DELETE FROM tenant_access_operations`, `UPDATE tenant_access_operations SET audit='{}'`, `DELETE FROM tenant_access_recovery_attempts`, `UPDATE tenant_access_recovery_attempts SET attempt='{}'`, `DELETE FROM account_enrollments`} {
			if _, err := runtime.Exec(query); err == nil {
				t.Fatalf("runtime allowed %s", query)
			}
		}
	})
	t.Run("composition delta idempotency and conflicts", func(t *testing.T) {
		subject := uuid.NewString()
		authority.accounts["tenant-a"+subject] = AuthorityAccount{Member: true, Roles: []tenantauth.Role{tenantauth.RoleUser}}
		cmd := request(subject, []tenantauth.Role{tenantauth.RoleBackoffice, tenantauth.RoleTenantAdmin}, []tenantauth.Role{})
		result, err := service.Change(t.Context(), actor, cmd)
		if err != nil || !slices.Equal(result.Account.Roles, []tenantauth.Role{tenantauth.RoleBackoffice, tenantauth.RoleTenantAdmin, tenantauth.RoleUser}) {
			t.Fatalf("composition: %+v %v", result, err)
		}
		calls := authority.calls
		replay := actor
		replay.Subject = uuid.NewString()
		replay.RequestID = "other-admin-complete"
		if _, err = service.Change(t.Context(), replay, cmd); err != nil || authority.calls != calls {
			t.Fatalf("completed replay mutated: %v", err)
		}
		conflict := cmd
		conflict.Reason = "Different terms"
		if _, err = service.Change(t.Context(), actor, conflict); !errors.Is(err, ErrOperationConflict) {
			t.Fatalf("payload conflict=%v", err)
		}
		conflict = cmd
		conflict.TargetSubject = uuid.NewString()
		if _, err = service.Change(t.Context(), actor, conflict); !errors.Is(err, ErrOperationConflict) {
			t.Fatalf("cross-target conflict=%v", err)
		}
		stale := cmd
		stale.OperationID = uuid.NewString()
		if _, err = service.Change(t.Context(), actor, stale); !errors.Is(err, ErrRevisionConflict) {
			t.Fatalf("revision conflict=%v", err)
		}
		other := actor
		other.TenantID = "tenant-b"
		unrelated, err := service.Inspect(t.Context(), other, subject)
		if err != nil || unrelated.Member || len(unrelated.Roles) > 0 {
			t.Fatalf("tenant leak: %+v %v", unrelated, err)
		}
	})
	t.Run("atomic denial before external revoke and departed operator recovery", func(t *testing.T) {
		subject := uuid.NewString()
		authority.accounts["tenant-a"+subject] = AuthorityAccount{Member: true, Roles: []tenantauth.Role{tenantauth.RoleUser}}
		cmd := request(subject, []tenantauth.Role{}, []tenantauth.Role{tenantauth.RoleUser})
		authority.fail = true
		authority.before = func() {
			var complete bool
			var count int
			if err := runtime.QueryRow(`SELECT (progress->>'complete')::boolean FROM account_enrollments WHERE issuer=$1 AND subject=$2 AND tenant_id=$3`, fixtureIssuer, subject, actor.TenantID).Scan(&complete); err != nil || !complete {
				t.Errorf("denial not committed before write: %v", err)
			}
			if err := runtime.QueryRow(`SELECT count(*) FROM tenant_access_operations WHERE operation_id=$1 AND status='pending'`, cmd.OperationID).Scan(&count); err != nil || count != 1 {
				t.Errorf("intent not committed before write: %v", err)
			}
		}
		if _, err := service.Change(t.Context(), actor, cmd); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("partial failure=%v", err)
		}
		authority.before = nil
		pending := inspect(subject)
		if pending.PendingOperationID != cmd.OperationID || !pending.EnrollmentSuppressed {
			t.Fatalf("pending receipt=%+v", pending)
		}
		different := request(subject, []tenantauth.Role{tenantauth.RoleBackoffice}, []tenantauth.Role{})
		if _, err := service.Change(t.Context(), actor, different); !errors.Is(err, ErrPendingOperation) {
			t.Fatalf("competing change=%v", err)
		}
		authority.fail = false
		recoverer := actor
		recoverer.Subject = uuid.NewString()
		recoverer.RequestID = "fixture-recovery"
		recoverer.SourceIP = "100.64.0.3"
		result, err := service.Change(t.Context(), recoverer, cmd)
		if err != nil || len(result.Account.Roles) != 0 || result.Operation.ActorSubject != actor.Subject || len(result.Operation.RecoveryAttempts) != 1 || result.Operation.RecoveryAttempts[0].ActorSubject != recoverer.Subject {
			t.Fatalf("recovery: %+v %v", result, err)
		}
		policy := accountenrollment.RuntimeConfig{Enabled: true, Issuer: fixtureIssuer, TenantIDs: []string{"tenant-a"}, AllowVerifiedAccounts: true, KeycloakBaseURL: "https://keycloak.example.test/auth", KeycloakClientID: "noebs-account-enroller", KeycloakClientSecret: "fixture"}
		signup, err := accountenrollment.New(policy, catalog, authority, enrollment)
		if err != nil {
			t.Fatal(err)
		}
		value, err := signup.Execute(t.Context(), accountenrollment.Identity{Issuer: fixtureIssuer, Subject: subject, TenantID: "tenant-a"}, true)
		if err != nil || len(value.Tenants) != 0 || value.EnrollmentStatus != "complete" {
			t.Fatalf("signup restored revoked access: %+v %v", value, err)
		}
	})
	t.Run("receipt failure atomically rolls back intent", func(t *testing.T) {
		subject := uuid.NewString()
		cmd := request(subject, []tenantauth.Role{}, []tenantauth.Role{tenantauth.RoleUser})
		_, err := migrate.Exec(`CREATE FUNCTION fixture_reject_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.subject='` + subject + `' THEN RAISE EXCEPTION 'fixture receipt failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fixture_reject_receipt BEFORE INSERT ON account_enrollments FOR EACH ROW EXECUTE FUNCTION fixture_reject_receipt()`)
		if err != nil {
			t.Fatal(err)
		}
		defer migrate.Exec(`DROP TRIGGER fixture_reject_receipt ON account_enrollments; DROP FUNCTION fixture_reject_receipt()`)
		calls := authority.calls
		if _, err = service.Change(t.Context(), actor, cmd); !errors.Is(err, ErrUnavailable) || authority.calls != calls {
			t.Fatalf("receipt failure allowed authority: %v", err)
		}
		op, err := journal.Operation(t.Context(), accountenrollment.Identity{Issuer: fixtureIssuer, Subject: subject, TenantID: "tenant-a"}, cmd.OperationID)
		if err != nil || op != nil {
			t.Fatalf("partial intent committed: %+v %v", op, err)
		}
	})
	t.Run("interrupted operator only grant blocks signup", func(t *testing.T) {
		subject := uuid.NewString()
		cmd := request(subject, []tenantauth.Role{tenantauth.RoleBackoffice}, []tenantauth.Role{})
		authority.fail = true
		if _, err := service.Change(t.Context(), actor, cmd); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		authority.fail = false
		// Simulate a crashed adapter after creating organization membership only.
		authority.accounts["tenant-a"+subject] = AuthorityAccount{Member: true, Roles: []tenantauth.Role{}}
		policy := accountenrollment.RuntimeConfig{Enabled: true, Issuer: fixtureIssuer, TenantIDs: []string{"tenant-a"}, AllowVerifiedAccounts: true, KeycloakBaseURL: "https://keycloak.example.test/auth", KeycloakClientID: "noebs-account-enroller", KeycloakClientSecret: "fixture"}
		signup, _ := accountenrollment.New(policy, catalog, authority, enrollment)
		if _, err := signup.Execute(t.Context(), accountenrollment.Identity{Issuer: fixtureIssuer, Subject: subject, TenantID: "tenant-a"}, true); !errors.Is(err, accountenrollment.ErrUnavailable) {
			t.Fatalf("pending signup=%v", err)
		}
		if _, err := service.Change(t.Context(), actor, cmd); err != nil {
			t.Fatal(err)
		}
		if _, err := signup.Execute(t.Context(), accountenrollment.Identity{Issuer: fixtureIssuer, Subject: subject, TenantID: "tenant-a"}, true); err != nil {
			t.Fatal(err)
		}
		if got := inspect(subject); !slices.Equal(got.Roles, []tenantauth.Role{tenantauth.RoleBackoffice}) {
			t.Fatalf("implicit user=%+v", got)
		}
	})
	t.Run("concurrent administrators share the enrollment identity lock", func(t *testing.T) {
		subject := uuid.NewString()
		first := request(subject, []tenantauth.Role{tenantauth.RoleBackoffice}, []tenantauth.Role{})
		second := first
		second.OperationID = uuid.NewString()
		second.GrantRoles = []tenantauth.Role{tenantauth.RoleTenantAdmin}
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, cmd := range []ChangeRequest{first, second} {
			go func(cmd ChangeRequest) { <-start; _, err := service.Change(t.Context(), actor, cmd); results <- err }(cmd)
		}
		close(start)
		one, two := <-results, <-results
		if !((one == nil && errors.Is(two, ErrRevisionConflict)) || (two == nil && errors.Is(one, ErrRevisionConflict))) {
			t.Fatalf("concurrent=%v/%v", one, two)
		}
	})
	t.Run("receipt absent explicit no-op revoke remains denied", func(t *testing.T) {
		subject := uuid.NewString()
		cmd := request(subject, []tenantauth.Role{}, []tenantauth.Role{tenantauth.RoleUser})
		result, err := service.Change(t.Context(), actor, cmd)
		if err != nil || !result.Account.EnrollmentSuppressed || len(result.Account.Roles) != 0 {
			t.Fatalf("absent revoke %+v %v", result, err)
		}
	})
	t.Run("pending recovery preserves unrelated external role drift", func(t *testing.T) {
		subject := uuid.NewString()
		authority.accounts["tenant-a"+subject] = AuthorityAccount{Member: true, Roles: []tenantauth.Role{tenantauth.RoleUser}}
		cmd := request(subject, []tenantauth.Role{tenantauth.RoleBackoffice}, []tenantauth.Role{})
		authority.fail = true
		if _, err := service.Change(t.Context(), actor, cmd); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		authority.fail = false
		authority.accounts["tenant-a"+subject] = AuthorityAccount{Member: true, Roles: []tenantauth.Role{tenantauth.RoleTenantAdmin, tenantauth.RoleUser}}
		result, err := service.Change(t.Context(), actor, cmd)
		if err != nil || !slices.Equal(result.Operation.CompletedRoles, []tenantauth.Role{tenantauth.RoleBackoffice, tenantauth.RoleTenantAdmin, tenantauth.RoleUser}) || slices.Contains(result.Operation.AfterRoles, tenantauth.RoleTenantAdmin) {
			t.Fatalf("drift recovery=%+v %v", result, err)
		}
	})
	t.Run("deployment bootstrap reserves before setup and cannot restore later revocation", func(t *testing.T) {
		migrationEnrollment, _ := accountenrollment.NewPostgresStore(migrate.DB.DB)
		migrationJournal, _ := NewPostgresJournal(migrate.DB.DB)
		bootService, _ := New(Config{Issuer: fixtureIssuer, Catalog: catalog, Enrollment: migrationEnrollment, Journal: migrationJournal, Authority: authority, Clock: time.Now})
		bootstrapActor := Actor{Kind: "deployment-bootstrap", Issuer: fixtureIssuer, Subject: uuid.NewString(), TenantID: "tenant-b", SourceIP: "127.0.0.1", RequestID: uuid.NewString()}
		boot := BootstrapRequest{OperationID: uuid.NewString(), Reason: "Initialize approved isolated operator", BackofficeOrigin: "https://private.example.test"}
		provider := &fixtureOperator{subject: uuid.NewString(), failMail: true}
		provider.before = func() {
			receipt, err := migrationJournal.BootstrapReceipt(t.Context(), fixtureIssuer, "tenant-b")
			if err != nil || receipt == nil || receipt.OperationID != boot.OperationID {
				t.Errorf("identity setup before durable reservation: %+v %v", receipt, err)
			}
		}
		if _, err := service.BootstrapOperator(t.Context(), bootstrapActor, boot, provider); !errors.Is(err, ErrForbidden) || provider.prepared != 0 {
			t.Fatalf("runtime bootstrapped: %v", err)
		}
		if _, err := bootService.BootstrapOperator(t.Context(), bootstrapActor, boot, provider); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("mail interruption: %v", err)
		}
		receipt, err := migrationJournal.BootstrapReceipt(t.Context(), fixtureIssuer, "tenant-b")
		if err != nil || receipt.Subject != provider.subject || receipt.Change == nil || receipt.Complete {
			t.Fatalf("bound pending receipt: %+v %v", receipt, err)
		}
		// Signup must not add user between preparing the operator and granting roles.
		var blocked bool
		if err := enrollment.WithIdentity(t.Context(), accountenrollment.Identity{Issuer: fixtureIssuer, Subject: provider.subject, TenantID: "tenant-b"}, func(p accountenrollment.ProgressAccess) error {
			var err error
			blocked, err = p.(accountenrollment.PendingAccess).HasPendingAccessChange(t.Context())
			return err
		}); err != nil || !blocked {
			t.Fatalf("bootstrap signup gap: %v", err)
		}
		provider.failMail = false
		result, err := bootService.BootstrapOperator(t.Context(), bootstrapActor, boot, provider)
		if err != nil || provider.prepared != 1 || !slices.Equal(result.Account.Roles, []tenantauth.Role{tenantauth.RoleBackoffice, tenantauth.RoleTenantAdmin}) || result.Operation.ActorKind != "deployment-bootstrap" {
			t.Fatalf("bootstrap recovery: %+v %v", result, err)
		}
		operator := actor
		operator.TenantID = "tenant-b"
		current, _ := service.Inspect(t.Context(), operator, provider.subject)
		revoke := ChangeRequest{OperationID: uuid.NewString(), TargetSubject: provider.subject, GrantRoles: []tenantauth.Role{}, RevokeRoles: []tenantauth.Role{tenantauth.RoleTenantAdmin}, ExpectedRevision: current.Revision, Reason: "Test last administrator protection"}
		if _, err := service.Change(t.Context(), operator, revoke); !errors.Is(err, ErrLastAdministrator) {
			t.Fatalf("removed last admin: %v", err)
		}
		// An independent authority revocation must never reopen initial bootstrap.
		authority.accounts["tenant-b"+provider.subject] = AuthorityAccount{Member: true, Roles: []tenantauth.Role{}}
		calls := authority.calls
		sent := provider.sent
		if _, err := bootService.BootstrapOperator(t.Context(), bootstrapActor, boot, provider); err != nil || authority.calls != calls || provider.sent != sent || provider.prepared != 1 {
			t.Fatalf("completed bootstrap rewrote identity: %v", err)
		}
		replacement := boot
		replacement.OperationID = uuid.NewString()
		if _, err := bootService.BootstrapOperator(t.Context(), bootstrapActor, replacement, provider); !errors.Is(err, ErrOperationConflict) {
			t.Fatalf("reused initial authority: %v", err)
		}
	})

	t.Run("nested sessions complete with one pool connection", func(t *testing.T) {
		runtime.SetMaxOpenConns(1)
		defer runtime.SetMaxOpenConns(12)
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		subject := uuid.NewString()
		current, err := service.Inspect(ctx, actor, subject)
		if err != nil {
			t.Fatal(err)
		}
		cmd := ChangeRequest{OperationID: uuid.NewString(), TargetSubject: subject, ExpectedRevision: current.Revision, GrantRoles: []tenantauth.Role{tenantauth.RoleBackoffice}, RevokeRoles: []tenantauth.Role{}, Reason: "Single session pool proof"}
		if _, err = service.Change(ctx, actor, cmd); err != nil {
			t.Fatal(err)
		}
		if _, err = service.History(ctx, actor, subject); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("different-target concurrent revocations retain one administrator", func(t *testing.T) {
		first, second := uuid.NewString(), uuid.NewString()
		operator := actor
		operator.TenantID = "tenant-c"
		for _, subject := range []string{first, second} {
			authority.accounts["tenant-c"+subject] = AuthorityAccount{Subject: subject, Member: true, Roles: []tenantauth.Role{tenantauth.RoleTenantAdmin}}
		}
		cmds := []ChangeRequest{}
		for _, subject := range []string{first, second} {
			current, err := service.Inspect(t.Context(), operator, subject)
			if err != nil {
				t.Fatal(err)
			}
			cmds = append(cmds, ChangeRequest{OperationID: uuid.NewString(), TargetSubject: subject, ExpectedRevision: current.Revision, GrantRoles: []tenantauth.Role{}, RevokeRoles: []tenantauth.Role{tenantauth.RoleTenantAdmin}, Reason: "Concurrent last-administrator check"})
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, cmd := range cmds {
			go func(cmd ChangeRequest) { <-start; _, err := service.Change(t.Context(), operator, cmd); results <- err }(cmd)
		}
		close(start)
		one, two := <-results, <-results
		if !((one == nil && errors.Is(two, ErrLastAdministrator)) || (two == nil && errors.Is(one, ErrLastAdministrator))) {
			t.Fatalf("last admins=%v/%v", one, two)
		}
	})

	t.Run("pending administrator revocation is not counted as surviving access", func(t *testing.T) {
		// Use A's existing fixture admins to isolate the exact pair by a new tenant.
		if _, err := migrate.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tenant-d','Fixture D',now())`); err != nil {
			t.Fatal(err)
		}
		isolatedCatalog, _ := tenantcatalog.New([]tenantcatalog.Tenant{{ID: "tenant-d", Name: "Fixture D"}})
		isolated, _ := New(Config{Issuer: fixtureIssuer, Catalog: isolatedCatalog, Enrollment: enrollment, Journal: journal, Authority: authority, Clock: time.Now})
		operator := actor
		operator.TenantID = "tenant-d"
		first, second := uuid.NewString(), uuid.NewString()
		for _, subject := range []string{first, second} {
			authority.accounts["tenant-d"+subject] = AuthorityAccount{Subject: subject, Member: true, Roles: []tenantauth.Role{tenantauth.RoleTenantAdmin}}
		}
		command := func(subject string) ChangeRequest {
			v, err := isolated.Inspect(t.Context(), operator, subject)
			if err != nil {
				t.Fatal(err)
			}
			return ChangeRequest{OperationID: uuid.NewString(), TargetSubject: subject, GrantRoles: []tenantauth.Role{}, RevokeRoles: []tenantauth.Role{tenantauth.RoleTenantAdmin}, ExpectedRevision: v.Revision, Reason: "Pending last admin recovery"}
		}
		pending := command(first)
		authority.fail = true
		if _, err := isolated.Change(t.Context(), operator, pending); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		authority.fail = false
		if _, err := isolated.Change(t.Context(), operator, command(second)); !errors.Is(err, ErrLastAdministrator) {
			t.Fatalf("counted pending removal as survivor: %v", err)
		}
		// Independent authority removal can still happen; retry must inspect live state.
		authority.accounts["tenant-d"+second] = AuthorityAccount{Subject: second, Member: true, Roles: []tenantauth.Role{}}
		if _, err := isolated.Change(t.Context(), operator, pending); !errors.Is(err, ErrLastAdministrator) {
			t.Fatalf("pending retry removed last admin: %v", err)
		}
	})

	t.Run("operation marker blocks signup before bootstrap subject is persisted", func(t *testing.T) {
		if _, err := migrate.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tenant-e','Fixture E',now())`); err != nil {
			t.Fatal(err)
		}
		markerCatalog, _ := tenantcatalog.New([]tenantcatalog.Tenant{{ID: "tenant-e", Name: "Fixture E"}})
		ownerJournal, _ := NewPostgresJournal(migrate.DB.DB)
		op, subject := uuid.NewString(), uuid.NewString()
		if err := ownerJournal.ReserveBootstrap(t.Context(), BootstrapReceipt{Issuer: fixtureIssuer, TenantID: "tenant-e", OperationID: op, Email: BootstrapEmail, Username: BootstrapUsername, Actor: actor, PayloadHash: strings.Repeat("a", 64)}); err != nil {
			t.Fatal(err)
		}
		// Native CREATE already committed its admin-only marker, but the process died
		// before it could persist this subject in the reserved SQL row.
		authority.markers = map[string]string{subject: op}
		policy := accountenrollment.RuntimeConfig{Enabled: true, Issuer: fixtureIssuer, TenantIDs: []string{"tenant-e"}, AllowVerifiedAccounts: true, KeycloakBaseURL: "https://keycloak.example.test/auth", KeycloakClientID: "noebs-account-enroller", KeycloakClientSecret: "fixture"}
		signup, err := accountenrollment.New(policy, markerCatalog, authority, enrollment)
		if err != nil {
			t.Fatal(err)
		}
		calls := authority.calls
		if _, err := signup.Execute(t.Context(), accountenrollment.Identity{Issuer: fixtureIssuer, Subject: subject, TenantID: "tenant-e"}, true); !errors.Is(err, accountenrollment.ErrUnavailable) || authority.calls != calls {
			t.Fatalf("created-unbound operator enrolled: %v", err)
		}
	})

	t.Run("disabled administrators do not satisfy last administrator guard", func(t *testing.T) {
		if _, err := migrate.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tenant-f','Fixture F',now())`); err != nil {
			t.Fatal(err)
		}
		c, _ := tenantcatalog.New([]tenantcatalog.Tenant{{ID: "tenant-f", Name: "Fixture F"}})
		s, _ := New(Config{Issuer: fixtureIssuer, Catalog: c, Enrollment: enrollment, Journal: journal, Authority: authority, Clock: time.Now})
		first, second := uuid.NewString(), uuid.NewString()
		authority.accounts["tenant-f"+first] = AuthorityAccount{Subject: first, Member: true, Roles: []tenantauth.Role{tenantauth.RoleTenantAdmin}}
		authority.accounts["tenant-f"+second] = AuthorityAccount{Subject: second, Member: true, Disabled: true, Roles: []tenantauth.Role{tenantauth.RoleTenantAdmin}}
		operator := actor
		operator.TenantID = "tenant-f"
		current, err := s.Inspect(t.Context(), operator, first)
		if err != nil {
			t.Fatal(err)
		}
		cmd := ChangeRequest{OperationID: uuid.NewString(), TargetSubject: first, GrantRoles: []tenantauth.Role{}, RevokeRoles: []tenantauth.Role{tenantauth.RoleTenantAdmin}, ExpectedRevision: current.Revision, Reason: "Disabled survivor check"}
		if _, err := s.Change(t.Context(), operator, cmd); !errors.Is(err, ErrLastAdministrator) {
			t.Fatalf("disabled survivor=%v", err)
		}
	})

}

type fixtureOperator struct {
	subject        string
	prepared, sent int
	before         func()
	failMail       bool
}

func (p *fixtureOperator) PrepareOperator(_ context.Context, _, _, _, _ string) (string, error) {
	if p.before != nil {
		p.before()
	}
	p.prepared++
	return p.subject, nil
}
func (p *fixtureOperator) SendOperatorSetup(_ context.Context, _, _ string) error {
	p.sent++
	if p.failMail {
		return errors.New("fixture mail failure")
	}
	return nil
}
