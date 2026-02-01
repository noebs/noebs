package main

import (
	"context"
	"errors"
	"net"
	"net/http"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	walletgrpc "github.com/adonese/noebs/wallet/grpc"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
)

var grpcServer *grpc.Server
var grpcListener net.Listener
var grpcGateway *http.Server

func initGRPCServers() error {
	if !noebsConfig.GRPCEnabled && !noebsConfig.GRPCGatewayEnabled {
		return nil
	}
	if walletService == nil {
		return errors.New("wallet service not initialized")
	}

	walletSrv := walletgrpc.NewServer(walletService)
	if noebsConfig.GRPCEnabled {
		if noebsConfig.GRPCPort == "" {
			return errors.New("missing grpc_port")
		}
		listener, err := net.Listen("tcp", noebsConfig.GRPCPort)
		if err != nil {
			return err
		}
		server := grpc.NewServer()
		walletv1.RegisterWalletInternalServiceServer(server, walletSrv)
		walletv1.RegisterWalletPublicServiceServer(server, walletSrv)
		grpcServer = server
		grpcListener = listener
	}

	if noebsConfig.GRPCGatewayEnabled {
		if noebsConfig.GRPCGatewayPort == "" {
			return errors.New("missing grpc_gateway_port")
		}
		mux := runtime.NewServeMux()
		if err := walletgrpc.RegisterPublicGatewayServer(context.Background(), mux, walletSrv); err != nil {
			return err
		}
		grpcGateway = &http.Server{
			Addr:    noebsConfig.GRPCGatewayPort,
			Handler: mux,
		}
	}

	return nil
}
