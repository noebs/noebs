package main

import (
	"errors"
	"testing"
)

func TestValidateEBSEndpointRequiresCanonicalHTTPS(t *testing.T) {
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "host", value: "https://consumer.ebs.example", valid: true},
		{name: "port and path", value: "https://consumer.ebs.example:8443/api/", valid: true},
		{name: "plaintext", value: "http://consumer.ebs.example"},
		{name: "relative", value: "/consumer/api"},
		{name: "missing host", value: "https:///api"},
		{name: "userinfo", value: "https://user:secret@consumer.ebs.example"},
		{name: "query", value: "https://consumer.ebs.example/api?tenant=a"},
		{name: "empty query", value: "https://consumer.ebs.example/api?"},
		{name: "fragment", value: "https://consumer.ebs.example/api#rail"},
		{name: "surrounding whitespace", value: " https://consumer.ebs.example "},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateEBSEndpoint("noebs.consumer_endpoint", test.value)
			if test.valid {
				if err != nil {
					t.Fatalf("validateEBSEndpoint() error = %v", err)
				}
				return
			}
			if !errors.Is(err, errInvalidEBSEndpoint) {
				t.Fatalf("validateEBSEndpoint() error = %v, want %v", err, errInvalidEBSEndpoint)
			}
		})
	}
}

func TestEBSRuntimeValidationRejectsUnsafeEndpoint(t *testing.T) {
	cfg := explicitEBSRuntimeConfig()
	cfg.ConsumerIP = "http://consumer.ebs.example"
	if err := validateEBSRuntimeConfig(serviceRoleEBSAdapter, cfg); !errors.Is(err, errInvalidEBSEndpoint) {
		t.Fatalf("validateEBSRuntimeConfig() error = %v, want %v", err, errInvalidEBSEndpoint)
	}

	cfg = explicitEBSRuntimeConfig()
	cfg.ConsumerIP = "https://consumer.ebs.example/api/"
	if err := validateEBSRuntimeConfig(serviceRoleEBSAdapter, cfg); err != nil {
		t.Fatalf("validateEBSRuntimeConfig() error = %v", err)
	}
}
