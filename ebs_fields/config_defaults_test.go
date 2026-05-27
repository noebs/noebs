package ebs_fields

import "testing"

func TestNoebsConfigDefaultsDoNotInventRoleRuntimeConfig(t *testing.T) {
	cfg := NoebsConfig{
		WalletEnabled:      true,
		TemporalEnabled:    true,
		GRPCEnabled:        true,
		GRPCGatewayEnabled: true,
	}
	cfg.Defaults()

	if cfg.GRPCPort != "" {
		t.Fatalf("GRPCPort = %q, want empty without explicit config", cfg.GRPCPort)
	}
	if cfg.GRPCGatewayPort != "" {
		t.Fatalf("GRPCGatewayPort = %q, want empty without explicit config", cfg.GRPCGatewayPort)
	}
	if cfg.WalletHoldExpirySeconds != 0 {
		t.Fatalf("WalletHoldExpirySeconds = %d, want 0 without explicit config", cfg.WalletHoldExpirySeconds)
	}
	if cfg.WalletApprovalTimeoutSeconds != 0 {
		t.Fatalf("WalletApprovalTimeoutSeconds = %d, want 0 without explicit config", cfg.WalletApprovalTimeoutSeconds)
	}
	if cfg.WalletVerificationTimeoutSeconds != 0 {
		t.Fatalf("WalletVerificationTimeoutSeconds = %d, want 0 without explicit config", cfg.WalletVerificationTimeoutSeconds)
	}
	if cfg.WalletManualTransferApprovalTimeoutSeconds != 0 {
		t.Fatalf("WalletManualTransferApprovalTimeoutSeconds = %d, want 0 without explicit config", cfg.WalletManualTransferApprovalTimeoutSeconds)
	}
	if cfg.WalletPSPPollerCron != "" {
		t.Fatalf("WalletPSPPollerCron = %q, want empty without explicit config", cfg.WalletPSPPollerCron)
	}
	if cfg.WalletPSPPollerBatchSize != 0 {
		t.Fatalf("WalletPSPPollerBatchSize = %d, want 0 without explicit config", cfg.WalletPSPPollerBatchSize)
	}
	if cfg.WalletPSPPollerIntervalSeconds != 0 {
		t.Fatalf("WalletPSPPollerIntervalSeconds = %d, want 0 without explicit config", cfg.WalletPSPPollerIntervalSeconds)
	}
	if cfg.WalletReconciliationCron != "" {
		t.Fatalf("WalletReconciliationCron = %q, want empty without explicit config", cfg.WalletReconciliationCron)
	}
	if cfg.WalletReconciliationBatchSize != 0 {
		t.Fatalf("WalletReconciliationBatchSize = %d, want 0 without explicit config", cfg.WalletReconciliationBatchSize)
	}
	if cfg.WalletReconciliationLookbackHours != 0 {
		t.Fatalf("WalletReconciliationLookbackHours = %d, want 0 without explicit config", cfg.WalletReconciliationLookbackHours)
	}
	if cfg.OtelServiceName != "" {
		t.Fatalf("OtelServiceName = %q, want empty without explicit service config", cfg.OtelServiceName)
	}
	if cfg.OtelSampleRate != 0 {
		t.Fatalf("OtelSampleRate = %f, want 0 without explicit config", cfg.OtelSampleRate)
	}
}
