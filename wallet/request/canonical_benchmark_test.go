package request

import (
	"testing"

	"github.com/adonese/noebs/internal/transactionauth"
)

var benchmarkCanonical Canonical

func BenchmarkCanonicalRequest(b *testing.B) {
	const tenantID = "alpha"
	p2pBody := []byte(`{
		"currency":"SDG",
		"from_wallet_id":"550e8400-e29b-41d4-a716-446655440000",
		"to_wallet_id":"550e8400-e29b-41d4-a716-446655440001",
		"amount":"1250",
		"description":"lunch",
		"idempotency_key":"transfer-1",
		"reference_id":"transfer-1",
		"to_owner_type":"user",
		"to_owner_id":"44"
	}`)
	withdrawalBody := []byte(`{
		"client_reference":"payout-1",
		"provider_code":"bank",
		"wallet_id":"550e8400-e29b-41d4-a716-446655440000",
		"amount":"100000",
		"currency":"SDG",
		"allow_return_to_source":true,
		"idempotency_key":"payout-1",
		"destination_id":"9",
		"metadata":{"z":1,"a":"value"},
		"region":"khartoum"
	}`)

	p2pCanonical, err := ParsePublic(transactionauth.OperationWalletP2P, tenantID, p2pBody, testDefaults)
	if err != nil {
		b.Fatal(err)
	}
	withdrawalCanonical, err := ParsePublic(transactionauth.OperationWalletWithdrawal, tenantID, withdrawalBody, testDefaults)
	if err != nil {
		b.Fatal(err)
	}

	benchmarks := []struct {
		name      string
		operation transactionauth.Operation
		body      []byte
		parse     func(transactionauth.Operation, string, []byte) (Canonical, error)
	}{
		{
			name:      "P2P/PublicBoundary",
			operation: transactionauth.OperationWalletP2P,
			body:      p2pBody,
			parse: func(operation transactionauth.Operation, tenantID string, body []byte) (Canonical, error) {
				return ParsePublic(operation, tenantID, body, testDefaults)
			},
		},
		{
			name:      "P2P/CanonicalBoundary",
			operation: transactionauth.OperationWalletP2P,
			body:      p2pCanonical.Body,
			parse:     ParseCanonical,
		},
		{
			name:      "Withdrawal/PublicBoundary",
			operation: transactionauth.OperationWalletWithdrawal,
			body:      withdrawalBody,
			parse: func(operation transactionauth.Operation, tenantID string, body []byte) (Canonical, error) {
				return ParsePublic(operation, tenantID, body, testDefaults)
			},
		},
		{
			name:      "Withdrawal/CanonicalBoundary",
			operation: transactionauth.OperationWalletWithdrawal,
			body:      withdrawalCanonical.Body,
			parse:     ParseCanonical,
		},
	}

	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(benchmark.body)))
			for b.Loop() {
				canonical, err := benchmark.parse(benchmark.operation, tenantID, benchmark.body)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkCanonical = canonical
			}
		})
	}
}
