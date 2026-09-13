package tenantaccess

import (
	"errors"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/google/uuid"
	"strings"
	"testing"
)

func TestAccessShapeAndAuthorityFailBeforeDependencies(t *testing.T) {
	catalog, _ := tenantcatalog.New([]tenantcatalog.Tenant{{ID: "tenant-a", Name: "A"}})
	service := &Service{config: Config{Issuer: fixtureIssuer, Catalog: catalog}}
	actor := Actor{Issuer: fixtureIssuer, Subject: uuid.NewString(), TenantID: "tenant-a", Roles: []tenantauth.Role{tenantauth.RoleTenantAdmin}, Permissions: []tenantauth.Permission{tenantauth.PermissionIdentityAccessWrite}, SourceIP: "100.64.0.2", RequestID: "request-one"}
	base := ChangeRequest{OperationID: uuid.NewString(), TargetSubject: uuid.NewString(), GrantRoles: []tenantauth.Role{tenantauth.RoleBackoffice}, RevokeRoles: []tenantauth.Role{}, ExpectedRevision: strings.Repeat("a", 64), Reason: "Reviewed grant"}
	for _, edit := range []func(*Actor){func(a *Actor) { a.Issuer = "https://elsewhere" }, func(a *Actor) { a.TenantID = "tenant-b" }, func(a *Actor) { a.Roles = []tenantauth.Role{tenantauth.RoleBackoffice} }, func(a *Actor) { a.Permissions = nil }, func(a *Actor) { a.Kind = "deployment-bootstrap" }} {
		copy := actor
		edit(&copy)
		if _, err := service.Change(t.Context(), copy, base); !errors.Is(err, ErrForbidden) {
			t.Fatalf("unauthorized=%v", err)
		}
	}
	for _, edit := range []func(*ChangeRequest){func(c *ChangeRequest) { c.GrantRoles = nil }, func(c *ChangeRequest) { c.OperationID = "" }, func(c *ChangeRequest) { c.Reason = " a " }, func(c *ChangeRequest) { c.ExpectedRevision = "" }, func(c *ChangeRequest) {
		c.GrantRoles = []tenantauth.Role{tenantauth.RoleUser, tenantauth.RoleBackoffice}
	}, func(c *ChangeRequest) { c.RevokeRoles = []tenantauth.Role{tenantauth.RoleBackoffice} }} {
		copy := base
		edit(&copy)
		if _, err := service.Change(t.Context(), actor, copy); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid shape=%v", err)
		}
	}
	for _, edit := range []func(*Actor){func(a *Actor) { a.SourceIP = "" }, func(a *Actor) { a.RequestID = "" }, func(a *Actor) { a.RequestID = "two words" }} {
		copy := actor
		edit(&copy)
		if _, err := service.Change(t.Context(), copy, base); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid audit=%v", err)
		}
	}
}
