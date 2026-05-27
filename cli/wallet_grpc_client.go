package main

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	errMissingGRPCServiceDiscovery = errors.New("missing noebs.grpc_service_discovery")
	errMissingGRPCServiceEndpoint  = errors.New("missing noebs.grpc_service_discovery entry")
	errInvalidGRPCServiceEndpoint  = errors.New("invalid noebs.grpc_service_discovery entry")
)

var walletLedgerGRPCConn *grpc.ClientConn
var walletPublicClient walletv1.WalletPublicServiceClient
var walletAdminClient walletv1.WalletAdminServiceClient

func grpcServiceDiscoveryEndpoint(cfg ebs_fields.NoebsConfig, role serviceRole) (string, error) {
	if cfg.GRPCServiceDiscovery == nil {
		return "", errMissingGRPCServiceDiscovery
	}
	key := string(role)
	endpoint := strings.TrimSpace(cfg.GRPCServiceDiscovery[key])
	if endpoint == "" {
		return "", fmt.Errorf("%w: %s", errMissingGRPCServiceEndpoint, key)
	}
	if strings.Contains(endpoint, "://") {
		return "", fmt.Errorf("%w: %s must be host:port", errInvalidGRPCServiceEndpoint, key)
	}
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", fmt.Errorf("%w: parse %s: %w", errInvalidGRPCServiceEndpoint, key, err)
	}
	if strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return "", fmt.Errorf("%w: %s must be host:port", errInvalidGRPCServiceEndpoint, key)
	}
	return endpoint, nil
}

func initWalletLedgerPublicClient(cfg ebs_fields.NoebsConfig) error {
	endpoint, err := grpcServiceDiscoveryEndpoint(cfg, serviceRoleWalletLedger)
	if err != nil {
		return err
	}
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("create wallet-ledger grpc client: %w", err)
	}
	walletLedgerGRPCConn = conn
	walletPublicClient = walletv1.NewWalletPublicServiceClient(conn)
	walletAdminClient = walletv1.NewWalletAdminServiceClient(conn)
	return nil
}

func closeWalletLedgerPublicClient() {
	if walletLedgerGRPCConn != nil {
		_ = walletLedgerGRPCConn.Close()
		walletLedgerGRPCConn = nil
		walletPublicClient = nil
		walletAdminClient = nil
	}
}
