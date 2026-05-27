package main

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strings"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	walletgrpc "github.com/adonese/noebs/wallet/grpc"
	walletworker "github.com/adonese/noebs/wallet/worker"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var grpcServer *grpc.Server
var grpcListener net.Listener
var grpcGatewayHandler http.Handler

type walletAuthRequirement uint8

const (
	walletAuthNone walletAuthRequirement = iota
	walletAuthJWT
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
		server := grpc.NewServer(grpc.UnaryInterceptor(requireAuthForWalletMethods))
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
	case walletAuthJWT:
		if !contextHasValidBearerToken(ctx) {
			return nil, status.Error(codes.Unauthenticated, "missing or invalid authorization token")
		}
	case walletAuthAdmin:
		ok, configured := contextHasValidAdminCredentials(ctx)
		if !configured {
			return nil, status.Error(codes.Unavailable, "admin auth not configured")
		}
		if !ok {
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
		case walletAuthJWT:
			token := extractBearerToken(r.Header.Get("Authorization"))
			if token == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if _, err := auth.VerifyJWT(token); err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		case walletAuthAdmin:
			ok, configured := requestHasValidAdminCredentials(r)
			if !configured {
				http.Error(w, "admin auth not configured", http.StatusServiceUnavailable)
				return
			}
			if !ok {
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
		return walletAuthJWT
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
		return walletAuthJWT
	default:
		return walletAuthNone
	}
}

func contextHasValidAdminCredentials(ctx context.Context) (bool, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false, adminAuthConfigured()
	}
	configured := adminAuthConfigured()
	if !configured {
		return false, false
	}
	if adminKeyConfigured() {
		for _, candidate := range md.Get("x-admin-key") {
			if validAdminKey(candidate) {
				return true, true
			}
		}
	}
	if adminBasicConfigured() {
		for _, header := range md.Get("authorization") {
			if validAdminBasicAuth(header) {
				return true, true
			}
		}
	}
	return false, true
}

func requestHasValidAdminCredentials(r *http.Request) (bool, bool) {
	if r == nil {
		return false, adminAuthConfigured()
	}
	configured := adminAuthConfigured()
	if !configured {
		return false, false
	}
	if validAdminKey(r.Header.Get("X-Admin-Key")) {
		return true, true
	}
	if validAdminBasicAuth(r.Header.Get("Authorization")) {
		return true, true
	}
	return false, true
}

func adminAuthConfigured() bool {
	return adminKeyConfigured() || adminBasicConfigured()
}

func adminKeyConfigured() bool {
	return strings.TrimSpace(noebsConfig.AdminKey) != ""
}

func adminBasicConfigured() bool {
	return strings.TrimSpace(noebsConfig.AdminUser) != "" && strings.TrimSpace(noebsConfig.AdminPassword) != ""
}

func validAdminKey(candidate string) bool {
	if !adminKeyConfigured() {
		return false
	}
	key := strings.TrimSpace(candidate)
	if key == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(key), []byte(noebsConfig.AdminKey)) == 1
}

func validAdminBasicAuth(header string) bool {
	if !adminBasicConfigured() {
		return false
	}
	parts := strings.SplitN(strings.TrimSpace(header), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "basic") {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
	if err != nil {
		return false
	}
	creds := strings.SplitN(string(decoded), ":", 2)
	if len(creds) != 2 {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(creds[0]), []byte(noebsConfig.AdminUser)) != 1 {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(creds[1]), []byte(noebsConfig.AdminPassword)) != 1 {
		return false
	}
	return true
}

func grpcGatewayIncomingHeaderMatcher(key string) (string, bool) {
	if strings.EqualFold(key, "X-Admin-Key") {
		return "x-admin-key", true
	}
	return runtime.DefaultHeaderMatcher(key)
}

func contextHasValidBearerToken(ctx context.Context) bool {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	vals := md.Get("authorization")
	for _, val := range vals {
		token := extractBearerToken(val)
		if token == "" {
			continue
		}
		if _, err := auth.VerifyJWT(token); err == nil {
			return true
		}
	}
	return false
}

func extractBearerToken(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(raw), "bearer ") {
		return strings.TrimSpace(raw[7:])
	}
	return ""
}
