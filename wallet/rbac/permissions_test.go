package rbac

import "testing"

func TestRoleForName(t *testing.T) {
	admin := RoleForName("admin")
	if admin == nil {
		t.Fatalf("expected admin role")
	}
	if !admin.HasPermission(PermManageConfig) {
		t.Fatalf("expected admin to have manage config permission")
	}

	viewer := RoleForName("viewer")
	if viewer == nil {
		t.Fatalf("expected viewer role")
	}
	if viewer.HasPermission(PermManageConfig) {
		t.Fatalf("did not expect viewer to have manage config permission")
	}

	if RoleForName("Administrator") == nil {
		t.Fatalf("expected administrator alias")
	}
	if RoleForName("unknown") != nil {
		t.Fatalf("expected unknown role to be nil")
	}
}
