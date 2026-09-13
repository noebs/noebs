package tenantauth

import "slices"

// PermissionsForRoles documents the managed role bundles without making roles
// imply permissions during authorization. Routes still require actual token
// permissions and the selected tenant role independently.
func PermissionsForRoles(roles []Role) []Permission {
	result := []Permission{}
	for _, role := range roles {
		var permissions []Permission
		switch role {
		case RoleUser:
		case RoleBackoffice:
			permissions = []Permission{PermissionReportingRead, PermissionWalletRead, PermissionWalletAuditRead, PermissionIdentityReviewRead}
		case RoleTenantAdmin:
			permissions = []Permission{PermissionReportingRead, PermissionWalletRead, PermissionWalletAuditRead, PermissionIdentityReviewRead, PermissionIdentityReviewDecide, PermissionWalletManualCreate, PermissionWalletFeesWrite, PermissionWalletRatesWrite, PermissionWalletWorkflowApprove, PermissionWalletWorkflowReject, PermissionWalletTransactionResolve, PermissionIdentityAccessRead, PermissionIdentityAccessWrite}
		}
		for _, permission := range permissions {
			if !slices.Contains(result, permission) {
				result = append(result, permission)
			}
		}
	}
	slices.Sort(result)
	return result
}
