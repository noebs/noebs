package walletgrpc

import (
	"strings"

	gateway "github.com/adonese/noebs/apigateway"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func (s *Server) requireAdmin(md metadata.MD) error {
	if s == nil || s.Service == nil {
		return status.Error(codes.FailedPrecondition, "missing wallet service")
	}
	values := md.Get(strings.ToLower(gateway.GatewayAdminIdentityHeader))
	if len(values) != 1 {
		return status.Error(codes.PermissionDenied, "unauthorized")
	}
	if values[0] != gateway.GatewayAdminIdentityValue {
		return status.Error(codes.PermissionDenied, "unauthorized")
	}
	return nil
}
