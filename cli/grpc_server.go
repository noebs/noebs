package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	gateway "github.com/adonese/noebs/apigateway"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
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
	walletAuthNone walletAuthRequirement = iota
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
		walletv1.RegisterWalletInternalServiceServer(server, walletSrv)
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
	case walletAuthNone:
		return handler(ctx, req)
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
		case walletAuthNone:
			next.ServeHTTP(w, r)
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
	if strings.HasPrefix(fullMethod, "/noebs.wallet.v1.WalletInternalService/") {
		return walletAuthAdmin
	}
	if strings.HasPrefix(fullMethod, "/noebs.wallet.v1.WalletAdminService/") {
		return walletAuthAdmin
	}
	switch fullMethod {
	case walletv1.WalletPublicService_RequestManualTransfer_FullMethodName,
		walletv1.WalletPublicService_SignalManualTransferDecision_FullMethodName,
		walletv1.WalletPublicService_SignalWithdrawalApproval_FullMethodName,
		walletv1.WalletPublicService_SignalWithdrawalVerification_FullMethodName:
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
		return walletAuthNone
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
		return walletAuthNone
	}
}

func contextHasGatewayAdminIdentity(ctx context.Context) bool {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	values := md.Get(strings.ToLower(gateway.GatewayAdminIdentityHeader))
	if len(values) != 1 {
		return false
	}
	return values[0] == gateway.GatewayAdminIdentityValue
}

func requestHasGatewayAdminIdentity(r *http.Request) bool {
	if r == nil {
		return false
	}
	return r.Header.Get(gateway.GatewayAdminIdentityHeader) == gateway.GatewayAdminIdentityValue
}

func grpcGatewayIncomingHeaderMatcher(key string) (string, bool) {
	if isPublicCredentialHeader(key) {
		return "", false
	}
	if strings.EqualFold(key, gateway.GatewayAdminIdentityHeader) {
		return strings.ToLower(gateway.GatewayAdminIdentityHeader), true
	}
	if strings.EqualFold(key, gateway.GatewayTenantIDHeader) {
		return strings.ToLower(gateway.GatewayTenantIDHeader), true
	}
	if strings.EqualFold(key, gateway.GatewayUserIDHeader) {
		return strings.ToLower(gateway.GatewayUserIDHeader), true
	}
	if strings.EqualFold(key, gateway.GatewayMobileHeader) {
		return strings.ToLower(gateway.GatewayMobileHeader), true
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
	_, err := gateway.ParseInternalUserIdentity(
		singleMetadataValue(md, gateway.GatewayTenantIDHeader),
		singleMetadataValue(md, gateway.GatewayUserIDHeader),
		singleMetadataValue(md, gateway.GatewayMobileHeader),
	)
	return err == nil
}

func requestHasGatewayUserIdentity(r *http.Request) bool {
	if r == nil {
		return false
	}
	_, err := gateway.ParseInternalUserIdentity(
		r.Header.Get(gateway.GatewayTenantIDHeader),
		r.Header.Get(gateway.GatewayUserIDHeader),
		r.Header.Get(gateway.GatewayMobileHeader),
	)
	return err == nil
}

func singleMetadataValue(md metadata.MD, header string) string {
	values := md.Get(strings.ToLower(header))
	if len(values) != 1 {
		return ""
	}
	return values[0]
}
