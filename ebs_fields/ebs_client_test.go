package ebs_fields

import (
	"crypto/tls"
	"testing"
)

func TestConfigureEBSHTTPClient_SecureByDefault(t *testing.T) {
	old := ebsTransport.TLSClientConfig
	t.Cleanup(func() { ebsTransport.TLSClientConfig = old })

	ConfigureEBSHTTPClient(NoebsConfig{})
	if ebsTransport.TLSClientConfig == nil {
		t.Fatalf("expected TLSClientConfig to be set")
	}
	if ebsTransport.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("expected InsecureSkipVerify=false by default")
	}
	if ebsTransport.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.Fatalf("expected MinVersion >= TLS1.2, got %v", ebsTransport.TLSClientConfig.MinVersion)
	}
}

func TestConfigureEBSHTTPClient_AllowsInsecureWhenExplicit(t *testing.T) {
	old := ebsTransport.TLSClientConfig
	t.Cleanup(func() { ebsTransport.TLSClientConfig = old })

	ConfigureEBSHTTPClient(NoebsConfig{EBSInsecureSkipVerify: true})
	if ebsTransport.TLSClientConfig == nil {
		t.Fatalf("expected TLSClientConfig to be set")
	}
	if !ebsTransport.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("expected InsecureSkipVerify=true when explicitly configured")
	}
}
