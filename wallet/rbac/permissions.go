package rbac

import "strings"

type Permission string

const (
	PermViewWallets  Permission = "wallet:view"
	PermApproveSmall Permission = "transfer:approve:small"
	PermApproveLarge Permission = "transfer:approve:large"
	PermManageConfig Permission = "config:manage"
	PermManageUsers  Permission = "users:manage"
	PermViewAudit    Permission = "audit:view"
	PermManualCredit Permission = "wallet:manual_credit"
	PermManualDebit  Permission = "wallet:manual_debit"
)

type Role struct {
	RoleName    string
	RoleLevel   int
	Permissions []Permission
}

var (
	roleViewer = Role{
		RoleName:  "viewer",
		RoleLevel: 10,
		Permissions: []Permission{
			PermViewWallets,
			PermViewAudit,
		},
	}
	roleOperator = Role{
		RoleName:  "operator",
		RoleLevel: 20,
		Permissions: []Permission{
			PermViewWallets,
			PermViewAudit,
			PermManualCredit,
			PermManualDebit,
		},
	}
	roleSupervisor = Role{
		RoleName:  "supervisor",
		RoleLevel: 30,
		Permissions: []Permission{
			PermViewWallets,
			PermViewAudit,
			PermManualCredit,
			PermManualDebit,
			PermApproveSmall,
			PermApproveLarge,
		},
	}
	roleAdmin = Role{
		RoleName:  "admin",
		RoleLevel: 40,
		Permissions: []Permission{
			PermViewWallets,
			PermViewAudit,
			PermManualCredit,
			PermManualDebit,
			PermApproveSmall,
			PermApproveLarge,
			PermManageConfig,
			PermManageUsers,
		},
	}
)

func RoleForName(name string) *Role {
	normalized := strings.ToLower(strings.TrimSpace(name))
	switch normalized {
	case "viewer":
		return &roleViewer
	case "operator", "ops":
		return &roleOperator
	case "supervisor", "manager":
		return &roleSupervisor
	case "admin", "administrator":
		return &roleAdmin
	default:
		return nil
	}
}

func (r *Role) HasPermission(p Permission) bool {
	if r == nil {
		return false
	}
	for _, perm := range r.Permissions {
		if perm == p {
			return true
		}
	}
	return false
}
