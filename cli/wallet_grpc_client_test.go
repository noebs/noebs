package main

import (
	"errors"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
)

func TestGRPCServiceDiscoveryEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ebs_fields.NoebsConfig
		want    string
		wantErr error
	}{
		{
			name:    "missing discovery map",
			cfg:     ebs_fields.NoebsConfig{},
			wantErr: errMissingGRPCServiceDiscovery,
		},
		{
			name: "missing wallet ledger entry",
			cfg: ebs_fields.NoebsConfig{
				GRPCServiceDiscovery: map[string]string{},
			},
			wantErr: errMissingGRPCServiceEndpoint,
		},
		{
			name: "rejects URL endpoint",
			cfg: ebs_fields.NoebsConfig{
				GRPCServiceDiscovery: map[string]string{
					string(serviceRoleWalletLedger): "http://wallet-ledger:9090",
				},
			},
			wantErr: errInvalidGRPCServiceEndpoint,
		},
		{
			name: "rejects missing port",
			cfg: ebs_fields.NoebsConfig{
				GRPCServiceDiscovery: map[string]string{
					string(serviceRoleWalletLedger): "wallet-ledger",
				},
			},
			wantErr: errInvalidGRPCServiceEndpoint,
		},
		{
			name: "accepts host port",
			cfg: ebs_fields.NoebsConfig{
				GRPCServiceDiscovery: map[string]string{
					string(serviceRoleWalletLedger): "wallet-ledger:9090",
				},
			},
			want: "wallet-ledger:9090",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := grpcServiceDiscoveryEndpoint(tt.cfg, serviceRoleWalletLedger)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("endpoint = %q, want %q", got, tt.want)
			}
		})
	}
}
