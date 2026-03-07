package main

import (
	"context"
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
		server := grpc.NewServer(grpc.UnaryInterceptor(requireAuthForManualTransferMethods))
		walletv1.RegisterWalletInternalServiceServer(server, walletSrv)
		walletv1.RegisterWalletPublicServiceServer(server, walletSrv)
		grpcServer = server
		grpcListener = listener
	}

	if noebsConfig.GRPCGatewayEnabled {
		mux := runtime.NewServeMux(runtime.WithIncomingHeaderMatcher(grpcGatewayIncomingHeaderMatcher))
		if err := walletgrpc.RegisterPublicGatewayServer(context.Background(), mux, walletSrv); err != nil {
			return err
		}
		grpcGatewayHandler = requireAuthForManualTransferHTTP(mux)
	}

	return nil
}

func requireAuthForManualTransferMethods(
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	if !manualTransferMethodRequiresAuth(info.FullMethod) {
		return handler(ctx, req)
	}
	if !contextHasValidBearerToken(ctx) {
		return nil, status.Error(codes.Unauthenticated, "missing or invalid authorization token")
	}
	return handler(ctx, req)
}

func requireAuthForManualTransferHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !manualTransferPathRequiresAuth(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		token := extractBearerToken(r.Header.Get("Authorization"))
		if token == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if _, err := auth.VerifyJWT(token); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func manualTransferMethodRequiresAuth(fullMethod string) bool {
	switch fullMethod {
	case walletv1.WalletInternalService_RequestManualTransfer_FullMethodName,
		walletv1.WalletInternalService_SignalManualTransferDecision_FullMethodName,
		walletv1.WalletPublicService_RequestManualTransfer_FullMethodName,
		walletv1.WalletPublicService_SignalManualTransferDecision_FullMethodName:
		return true
	default:
		return false
	}
}

func manualTransferPathRequiresAuth(path string) bool {
	if path == "/wallet/manual_transfers" {
		return true
	}
	if !strings.HasPrefix(path, "/wallet/manual_transfers/") {
		return false
	}
	return strings.HasSuffix(path, "/decision")
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
