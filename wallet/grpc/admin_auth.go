package walletgrpc

import (
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/internal/tenantauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func (s *Server) requireAdmin(md metadata.MD) error {
	if s == nil || s.Service == nil {
		return status.Error(codes.FailedPrecondition, "missing wallet service")
	}
	_, err := walletOperatorPrincipal(md)
	if err != nil {
		return status.Error(codes.PermissionDenied, "unauthorized")
	}
	return nil
}

func (s *Server) requireAdminPermission(md metadata.MD, permission tenantauth.Permission) (gateway.PrincipalIdentity, error) {
	if s == nil || s.Service == nil {
		return gateway.PrincipalIdentity{}, status.Error(codes.FailedPrecondition, "missing wallet service")
	}
	principal, err := walletOperatorPrincipal(md)
	if err != nil || principal.Permission() != permission {
		return gateway.PrincipalIdentity{}, status.Error(codes.PermissionDenied, "unauthorized")
	}
	if isWalletWritePermission(permission) &&
		!principal.HasRole(tenantauth.RoleTenantAdmin) {
		return gateway.PrincipalIdentity{}, status.Error(codes.PermissionDenied, "unauthorized")
	}
	return principal, nil
}

func walletOperatorPrincipal(md metadata.MD) (gateway.PrincipalIdentity, error) {
	principal, err := principalFromMetadata(md, time.Now().UTC())
	if err != nil || principal == nil || !isWalletOperatorPrincipal(*principal) {
		return gateway.PrincipalIdentity{}, status.Error(codes.PermissionDenied, "unauthorized")
	}
	return *principal, nil
}

func isWalletWritePermission(permission tenantauth.Permission) bool {
	switch permission {
	case tenantauth.PermissionWalletManualCreate,
		tenantauth.PermissionWalletFeesWrite,
		tenantauth.PermissionWalletRatesWrite,
		tenantauth.PermissionWalletWorkflowApprove,
		tenantauth.PermissionWalletWorkflowReject:
		return true
	default:
		return false
	}
}

func isWalletOperatorPrincipal(principal gateway.PrincipalIdentity) bool {
	return principal.HasRole(tenantauth.RoleBackoffice) ||
		principal.HasRole(tenantauth.RoleTenantAdmin)
}
