package rbac

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
