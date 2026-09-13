package main

import (
	"errors"
	"reflect"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	walletinterop "github.com/adonese/noebs/wallet/interop"
)

func TestInteropRuntimeRequiresExplicitTransportAtBoundary(t *testing.T) {
	valid := ebs_fields.NoebsConfig{
		InteropTenant: "tenant-test", InteropFSPID: "noebs",
		InteropSDKOutboundURL:       "http://100.100.1.5:4001",
		InteropSDKInboundURL:        "http://100.100.1.5:4000",
		InteropBackendListenAddress: "0.0.0.0:4002",
		InteropBackendAllowedPeers:  []string{"100.100.1.5"},
	}
	if err := validateInteropRuntimeConfig(serviceRoleWalletWorker, valid); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ebs_fields.NoebsConfig){
		func(c *ebs_fields.NoebsConfig) { c.InteropTenant = "" },
		func(c *ebs_fields.NoebsConfig) { c.InteropFSPID = "" },
		func(c *ebs_fields.NoebsConfig) { c.InteropSDKOutboundURL = "" },
		func(c *ebs_fields.NoebsConfig) { c.InteropSDKInboundURL = "" },
		func(c *ebs_fields.NoebsConfig) { c.InteropBackendListenAddress = "" },
		func(c *ebs_fields.NoebsConfig) { c.InteropBackendAllowedPeers = nil },
	} {
		cfg := valid
		mutate(&cfg)
		before := cfg
		if err := validateInteropRuntimeConfig(serviceRoleWalletWorker, cfg); !errors.Is(err, walletinterop.ErrTransportConfig) {
			t.Errorf("missing configuration accepted: %v", err)
		}
		if !reflect.DeepEqual(cfg, before) {
			t.Fatal("validation mutated configuration")
		}
	}
	for _, role := range serviceRoleCatalog {
		if role != serviceRoleWalletWorker {
			if err := validateInteropRuntimeConfig(role, valid); !errors.Is(err, walletinterop.ErrTransportConfig) {
				t.Errorf("%s accepted worker transport: %v", role, err)
			}
		}
	}
}

func TestInteropRuntimeKeepsDisabledAndSeedRolesExplicit(t *testing.T) {
	if err := validateInteropRuntimeConfig(serviceRoleWalletWorker, ebs_fields.NoebsConfig{}); err != nil {
		t.Fatal(err)
	}
	seed := ebs_fields.NoebsConfig{InteropDemoSeed: true, InteropTenant: "tenant-mojaloop", InteropFSPID: "noebs"}
	if err := validateInteropRuntimeConfig(serviceRoleWalletLedgerMigrate, seed); err != nil {
		t.Fatal(err)
	}
	seed.InteropSDKOutboundURL = "http://100.100.1.5:4001"
	if err := validateInteropRuntimeConfig(serviceRoleWalletLedgerMigrate, seed); !errors.Is(err, walletinterop.ErrTransportConfig) {
		t.Fatalf("migration accepted runtime transport: %v", err)
	}
}
