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

func TestEBSClientHasNoInProcessCacheCardSideChannel(t *testing.T) {
	data, err := os.ReadFile("ebs_client.go")
	if err != nil {
		t.Fatalf("read ebs_client.go: %v", err)
	}
	source := string(data)
	rejected := []string{
		"var EBSRes",
		"EBSRes <-",
		"make(chan CacheCards",
		"getPan(",
	}
	for _, token := range rejected {
		if strings.Contains(source, token) {
			t.Fatalf("ebs_client.go must not publish cache-card state through in-process side channel %q", token)
		}
	}
}

func TestDynamicFeesHaveNoRuntimeDefaultsHelper(t *testing.T) {
	data, err := os.ReadFile("ebs_urls.go")
	if err != nil {
		t.Fatalf("read ebs_urls.go: %v", err)
	}
	if strings.Contains(string(data), "DynamicFeesWithDefaults") {
		t.Fatalf("EBS dynamic fees must be explicit merged config, not hard-coded runtime defaults")
	}
}

func TestNoebsConfigUsesExplicitResolvedEBSEndpoints(t *testing.T) {
	payload := []byte(`{
		"consumer_endpoint": "https://consumer.ebs.example",
		"merchant_endpoint": "https://merchant.ebs.example",
		"ipin_endpoint": "https://ipin.ebs.example",
		"consumer_app_id": "consumer-app",
		"merchant_app_id": "merchant-app",
		"ebs_dynamic_fees": {
			"p2p_fees": 30,
			"custom_fees": 85,
			"special_payment_fees": 2
		}
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
	if cfg.EBSDynamicFees.CardTransferfees != 30 || cfg.EBSDynamicFees.CustomFees != 85 || cfg.EBSDynamicFees.SpecialPaymentFees != 2 {
		t.Fatalf("EBSDynamicFees = %+v", cfg.EBSDynamicFees)
	}
}
