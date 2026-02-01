package walletgrpc

import (
	"context"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
)

func RegisterPublicGateway(ctx context.Context, mux *runtime.ServeMux, conn *grpc.ClientConn) error {
	return walletv1.RegisterWalletPublicServiceHandler(ctx, mux, conn)
}

func RegisterPublicGatewayServer(ctx context.Context, mux *runtime.ServeMux, server walletv1.WalletPublicServiceServer) error {
	return walletv1.RegisterWalletPublicServiceHandlerServer(ctx, mux, server)
}
