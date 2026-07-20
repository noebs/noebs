package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/workloadauth"
	walletgrpc "github.com/adonese/noebs/wallet/grpc"
	walletworker "github.com/adonese/noebs/wallet/worker"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var grpcServer *grpc.Server
var grpcListener net.Listener
var grpcGatewayHandler http.Handler

type walletAuthRequirement uint8

const (
	walletAuthDeny walletAuthRequirement = iota
	walletAuthUserIdentity
	walletAuthAdmin
)

func initGRPCServers() error {
	if !noebsConfig.GRPCEnabled && !noebsConfig.GRPCGatewayEnabled {
		return nil
	}
	if walletService == nil {
		return errors.New("wallet service not initialized")
	}

	walletSrv := walletgrpc.NewServer(walletService)
	walletSrv.TemporalOptions = walletworker.Options{
		Host:      noebsConfig.TemporalHost,
		Port:      noebsConfig.TemporalPort,
		Namespace: noebsConfig.TemporalNamespace,
		TaskQueue: walletworker.TaskQueueMain,
	}
	if walletWorker != nil {
		walletSrv.TemporalClient = walletWorker.Client
	}
	if noebsConfig.GRPCEnabled {
		if noebsConfig.GRPCPort == "" {
			return errors.New("missing grpc_port")
		}
		listener, err := net.Listen("tcp", noebsConfig.GRPCPort)
		if err != nil {
			return err
		}
		if internalTransportServerTLS == nil {
			return errors.New("wallet-ledger grpc requires internal transport TLS")
		}
		server := grpc.NewServer(
			grpc.Creds(credentials.NewTLS(internalTransportServerTLS.Clone())),
			grpc.UnaryInterceptor(requireAuthForWalletMethods),
		)
		walletv1.RegisterWalletPublicServiceServer(server, walletSrv)
		walletv1.RegisterWalletAdminServiceServer(server, walletSrv)
		grpcServer = server
		grpcListener = listener
	}

	if noebsConfig.GRPCGatewayEnabled {
		mux := runtime.NewServeMux(runtime.WithIncomingHeaderMatcher(grpcGatewayIncomingHeaderMatcher))
		if err := walletgrpc.RegisterPublicGatewayServer(context.Background(), mux, walletSrv); err != nil {
			return err
		}
		grpcGatewayHandler = requireAuthForWalletHTTP(mux)
	}

	return nil
}

func requireAuthForWalletMethods(
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	switch walletMethodAuthRequirement(info.FullMethod) {
	case walletAuthDeny:
		return nil, status.Error(codes.PermissionDenied, "wallet method is outside the authentication catalog")
	case walletAuthUserIdentity:
		if !contextHasGatewayUserIdentity(ctx) {
			return nil, status.Error(codes.Unauthenticated, "missing or invalid gateway identity")
		}
	case walletAuthAdmin:
		if !contextHasGatewayAdminIdentity(ctx) {
			return nil, status.Error(codes.PermissionDenied, "unauthorized")
		}
	}
	return handler(ctx, req)
}

func requireAuthForWalletHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		switch walletPathAuthRequirement(r.URL.Path) {
		case walletAuthDeny:
			http.NotFound(w, r)
			return
		case walletAuthUserIdentity:
			if !requestHasGatewayUserIdentity(r) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		case walletAuthAdmin:
			if !requestHasGatewayAdminIdentity(r) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func walletMethodAuthRequirement(fullMethod string) walletAuthRequirement {
	switch fullMethod {
	case walletv1.WalletAdminService_RenderWalletAdmin_FullMethodName:
		return walletAuthAdmin
	case walletv1.WalletPublicService_GetWalletPublic_FullMethodName,
		walletv1.WalletPublicService_EnsureWalletPublic_FullMethodName,
		walletv1.WalletPublicService_ListPaymentMethodsPublic_FullMethodName,
		walletv1.WalletPublicService_ListWalletTransactionsPublic_FullMethodName,
		walletv1.WalletPublicService_RequestP2PTransfer_FullMethodName,
		walletv1.WalletPublicService_RequestDeposit_FullMethodName,
		walletv1.WalletPublicService_RequestWithdrawal_FullMethodName,
		walletv1.WalletPublicService_ListFundingSources_FullMethodName,
		walletv1.WalletPublicService_CreateWithdrawalDestination_FullMethodName,
		walletv1.WalletPublicService_ListWithdrawalDestinations_FullMethodName,
		walletv1.WalletPublicService_DeactivateWithdrawalDestination_FullMethodName,
		walletv1.WalletPublicService_RequestOwnershipVerification_FullMethodName,
		walletv1.WalletPublicService_CompleteOwnershipVerification_FullMethodName,
		walletv1.WalletPublicService_SetWalletPIN_FullMethodName,
		walletv1.WalletPublicService_EnrollUser2FA_FullMethodName,
		walletv1.WalletPublicService_ConfirmUser2FA_FullMethodName,
		walletv1.WalletPublicService_DisableUser2FA_FullMethodName:
		return walletAuthUserIdentity
	default:
		return walletAuthDeny
	}
}

func walletPathAuthRequirement(path string) walletAuthRequirement {
	switch {
	case path == "/wallet/manual_transfers":
		return walletAuthAdmin
	case strings.HasPrefix(path, "/wallet/manual_transfers/") && strings.HasSuffix(path, "/decision"):
		return walletAuthAdmin
	case strings.HasPrefix(path, "/wallet/withdrawals/") && strings.HasSuffix(path, "/approval"):
		return walletAuthAdmin
	case strings.HasPrefix(path, "/wallet/withdrawals/") && strings.HasSuffix(path, "/verification"):
		return walletAuthAdmin
	case path == "/wallet" || strings.HasPrefix(path, "/wallet/"):
		return walletAuthUserIdentity
	default:
		return walletAuthDeny
	}
}

func contextHasGatewayAdminIdentity(ctx context.Context) bool {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	principal, ok := gatewayPrincipalFromMetadata(md)
	return ok && isGatewayOperator(principal)
}

func requestHasGatewayAdminIdentity(r *http.Request) bool {
	if r == nil {
		return false
	}
	principal, ok := gatewayPrincipalFromRequest(r)
	return ok && isGatewayOperator(principal)
}

func grpcGatewayIncomingHeaderMatcher(key string) (string, bool) {
	if isPublicCredentialHeader(key) {
		return "", false
	}
	for _, identityHeader := range workloadauth.IdentityHeaderNames() {
		if strings.EqualFold(key, identityHeader) {
			return strings.ToLower(identityHeader), true
		}
	}
	return runtime.DefaultHeaderMatcher(key)
}

func isPublicCredentialHeader(key string) bool {
	return strings.EqualFold(key, "Authorization") ||
		strings.EqualFold(key, "X-Admin-Key") ||
		strings.EqualFold(key, "X-Admin-Role") ||
		strings.EqualFold(key, "X-Admin-Permissions")
}

func contextHasGatewayUserIdentity(ctx context.Context) bool {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	principal, ok := gatewayPrincipalFromMetadata(md)
	return ok && principal.UserID > 0 && principal.HasRole(tenantauth.RoleUser)
}

func requestHasGatewayUserIdentity(r *http.Request) bool {
	if r == nil {
		return false
	}
	principal, ok := gatewayPrincipalFromRequest(r)
	return ok && principal.UserID > 0 && principal.HasRole(tenantauth.RoleUser)
}

func gatewayPrincipalFromMetadata(md metadata.MD) (gateway.PrincipalIdentity, bool) {
	value := func(header string) (string, bool) {
		values := md.Get(strings.ToLower(header))
		return singleIdentityValue(values)
	}
	return parseGatewayPrincipal(value)
}

func gatewayPrincipalFromRequest(r *http.Request) (gateway.PrincipalIdentity, bool) {
	value := func(header string) (string, bool) {
		return singleIdentityValue(r.Header.Values(header))
	}
	return parseGatewayPrincipal(value)
}

func parseGatewayPrincipal(value func(string) (string, bool)) (gateway.PrincipalIdentity, bool) {
	tenantID, ok := value(gateway.GatewayTenantIDHeader)
	if !ok {
		return gateway.PrincipalIdentity{}, false
	}
	issuer, ok := value(gateway.GatewayIssuerHeader)
	if !ok {
		return gateway.PrincipalIdentity{}, false
	}
	subject, ok := value(gateway.GatewaySubjectHeader)
	if !ok {
		return gateway.PrincipalIdentity{}, false
	}
	organizationID, ok := value(gateway.GatewayOrganizationIDHeader)
	if !ok {
		return gateway.PrincipalIdentity{}, false
	}
	authorizedParty, ok := value(gateway.GatewayAuthorizedPartyHeader)
	if !ok {
		return gateway.PrincipalIdentity{}, false
	}
	roles, ok := value(gateway.GatewayRolesHeader)
	if !ok {
		return gateway.PrincipalIdentity{}, false
	}
	permission, ok := value(gateway.GatewayPermissionHeader)
	if !ok {
		return gateway.PrincipalIdentity{}, false
	}
	userID, ok := value(gateway.GatewayUserIDHeader)
	if !ok {
		return gateway.PrincipalIdentity{}, false
	}
	sourceIP, ok := value(gateway.GatewaySourceIPHeader)
	if !ok {
		return gateway.PrincipalIdentity{}, false
	}
	tokenExpiresAt, ok := value(gateway.GatewayTokenExpiresAtHeader)
	if !ok {
		return gateway.PrincipalIdentity{}, false
	}
	principal, err := gateway.ParseInternalPrincipalIdentity(gateway.PrincipalHeaderValues{
		TenantID:        tenantID,
		Issuer:          issuer,
		Subject:         subject,
		OrganizationID:  organizationID,
		AuthorizedParty: authorizedParty,
		Roles:           roles,
		Permission:      permission,
		UserID:          userID,
		SourceIP:        sourceIP,
		TokenExpiresAt:  tokenExpiresAt,
	}, time.Now().UTC())
	return principal, err == nil
}

func singleIdentityValue(values []string) (string, bool) {
	if len(values) == 0 {
		return "", true
	}
	if len(values) > 1 {
		return "", false
	}
	return values[0], true
}

func isGatewayOperator(principal gateway.PrincipalIdentity) bool {
	return principal.HasRole(tenantauth.RoleBackoffice) ||
		principal.HasRole(tenantauth.RoleTenantAdmin)
}
