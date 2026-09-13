package main

import (
	"fmt"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	walletinterop "github.com/adonese/noebs/wallet/interop"
)

func interopTransportConfig(cfg ebs_fields.NoebsConfig) (walletinterop.TransportConfig, error) {
	return walletinterop.NewTransportConfig(cfg.InteropSDKOutboundURL, cfg.InteropSDKInboundURL, cfg.InteropBackendListenAddress, cfg.InteropBackendAllowedPeers)
}

func validateInteropRuntimeConfig(role serviceRole, cfg ebs_fields.NoebsConfig) error {
	transportSet := cfg.InteropSDKOutboundURL != "" || cfg.InteropSDKInboundURL != "" || cfg.InteropBackendListenAddress != "" || len(cfg.InteropBackendAllowedPeers) != 0
	if cfg.InteropDemoSeed {
		if role != serviceRoleWalletLedgerMigrate || cfg.InteropTenant != "tenant-mojaloop" || cfg.InteropFSPID != "noebs" || transportSet {
			return fmt.Errorf("%w: interop demo seed requires the isolated tenant-mojaloop migration role and noebs participant without worker transport", walletinterop.ErrTransportConfig)
		}
		return nil
	}
	if cfg.InteropTenant == "" && cfg.InteropFSPID == "" && !transportSet {
		return nil
	}
	if role != serviceRoleWalletWorker || cfg.InteropTenant == "" || cfg.InteropFSPID == "" || cfg.InteropFSPID != strings.TrimSpace(cfg.InteropFSPID) {
		return fmt.Errorf("%w: interop requires explicit tenant and FSP on wallet-worker only", walletinterop.ErrTransportConfig)
	}
	if _, err := store.ValidateTenantID(cfg.InteropTenant); err != nil {
		return err
	}
	_, err := interopTransportConfig(cfg)
	return err
}
