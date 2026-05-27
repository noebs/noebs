package ebs_fields

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestNoebsConfigHasNoRuntimeDefaultsMutator(t *testing.T) {
	data, err := os.ReadFile("fields.go")
	if err != nil {
		t.Fatalf("read fields.go: %v", err)
	}
	if strings.Contains(string(data), "func (n *NoebsConfig) Defaults(") {
		t.Fatalf("NoebsConfig must not grow a runtime Defaults mutator; apply defaults at config boundaries")
	}
}

func TestNoebsConfigUsesExplicitResolvedEBSEndpoints(t *testing.T) {
	payload := []byte(`{
		"consumer_endpoint": "https://consumer.ebs.example",
		"merchant_endpoint": "https://merchant.ebs.example",
		"ipin_endpoint": "https://ipin.ebs.example",
		"consumer_app_id": "consumer-app",
		"merchant_app_id": "merchant-app"
	}`)

	var cfg NoebsConfig
	if err := json.Unmarshal(payload, &cfg); err != nil {
		t.Fatalf("unmarshal noebs config: %v", err)
	}
	if cfg.ConsumerIP != "https://consumer.ebs.example" {
		t.Fatalf("ConsumerIP = %q", cfg.ConsumerIP)
	}
	if cfg.MerchantIP != "https://merchant.ebs.example" {
		t.Fatalf("MerchantIP = %q", cfg.MerchantIP)
	}
	if cfg.IPINIp != "https://ipin.ebs.example" {
		t.Fatalf("IPINIp = %q", cfg.IPINIp)
	}
	if cfg.ConsumerID != "consumer-app" {
		t.Fatalf("ConsumerID = %q", cfg.ConsumerID)
	}
	if cfg.MerchantID != "merchant-app" {
		t.Fatalf("MerchantID = %q", cfg.MerchantID)
	}
}
