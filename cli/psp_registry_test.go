package main

import (
	"errors"
	"testing"

	walletpsp "github.com/adonese/noebs/wallet/psp"
	walletstore "github.com/adonese/noebs/wallet/store"
)

func TestRuntimePSPRegistryHasOnlyConcreteProviders(t *testing.T) {
	registry, _, err := buildPSPDeps(&walletstore.Store{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve(&walletpsp.Config{ProviderCode: "unregistered"}); !errors.Is(err, walletpsp.ErrPSPNotRegistered) {
		t.Fatalf("unregistered provider error = %v, want %v", err, walletpsp.ErrPSPNotRegistered)
	}
	provider, err := registry.Resolve(&walletpsp.Config{
		ProviderCode:          "httpjson",
		APIBaseURL:            "https://psp.example",
		IdempotencyHeaderName: "Idempotency-Key",
		DepositRequestMethod:  "POST",
		DepositRequestPath:    "/deposits",
		PayoutRequestMethod:   "POST",
		PayoutRequestPath:     "/payouts",
		StatusRequestMethod:   "GET",
		StatusRequestPath:     "/transactions/{transaction_id}",
	})
	if err != nil {
		t.Fatalf("resolve httpjson provider: %v", err)
	}
	if provider.Code() != "httpjson" {
		t.Fatalf("provider code = %q, want httpjson", provider.Code())
	}
}
