package request

import (
	"bytes"
	"errors"
	"testing"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/internal/transactionauth"
)

var testDefaults = Defaults{
	HoldExpirySeconds:      3600,
	ApprovalTimeoutSeconds: 7200,
	ApprovalThreshold:      100000,
}

func TestP2PCanonicalizationBindsEveryBusinessField(t *testing.T) {
	body := []byte(`{
		"currency":" SDG ",
		"from_wallet_id":"550e8400-e29b-41d4-a716-446655440000",
		"to_wallet_id":"550e8400-e29b-41d4-a716-446655440001",
		"amount":"1250",
		"description":" lunch ",
		"idempotency_key":" transfer-1 ",
		"reference_id":" transfer-1 ",
		"to_owner_type":" user ",
		"to_owner_id":" 44 "
	}`)
	canonical, err := ParsePublic(transactionauth.OperationWalletP2P, "alpha", body, testDefaults)
	if err != nil {
		t.Fatal(err)
	}
	request, ok := canonical.Message.(*walletv1.RequestP2PTransferRequest)
	if !ok {
		t.Fatalf("message = %T", canonical.Message)
	}
	if canonical.IdempotencyKey != "transfer-1" || request.GetIdempotencyKey() != "transfer-1" ||
		request.GetReferenceId() != "transfer-1" || request.GetTenantId() != "alpha" ||
		request.GetCurrency() != "SDG" || request.GetDescription() != "lunch" ||
		request.GetFromOwnerType() != "" || request.GetFromOwnerId() != "" {
		t.Fatalf("canonical request = %+v", request)
	}
	if bytes.Contains(canonical.Body, []byte("tenant_id")) || bytes.Contains(canonical.Body, []byte("from_owner")) {
		t.Fatalf("public canonical body exposes gateway-owned identity: %s", canonical.Body)
	}
	reparsed, err := ParseCanonical(transactionauth.OperationWalletP2P, "alpha", canonical.Body)
	if err != nil {
		t.Fatal(err)
	}
	if reparsed.Digest != canonical.Digest || !bytes.Equal(reparsed.Body, canonical.Body) {
		t.Fatalf("canonicalization is not idempotent: %s != %s", reparsed.Body, canonical.Body)
	}

	mutations := map[string][]byte{
		"tenant":         body,
		"currency":       bytes.Replace(body, []byte("SDG"), []byte("USD"), 1),
		"from wallet":    bytes.Replace(body, []byte("440000"), []byte("440002"), 1),
		"to wallet":      bytes.Replace(body, []byte("440001"), []byte("440003"), 1),
		"amount":         bytes.Replace(body, []byte("1250"), []byte("1251"), 1),
		"description":    bytes.Replace(body, []byte("lunch"), []byte("dinner"), 1),
		"reference":      bytes.Replace(body, []byte("transfer-1"), []byte("transfer-2"), 1),
		"recipient type": bytes.Replace(body, []byte(" user "), []byte(" merchant "), 1),
		"recipient":      bytes.Replace(body, []byte(" 44 "), []byte(" 45 "), 1),
	}
	for name, mutatedBody := range mutations {
		t.Run(name, func(t *testing.T) {
			tenant := "alpha"
			if name == "tenant" {
				tenant = "beta"
			}
			mutated, err := ParsePublic(transactionauth.OperationWalletP2P, tenant, mutatedBody, testDefaults)
			if err != nil {
				t.Fatal(err)
			}
			if mutated.Digest == canonical.Digest {
				t.Fatal("business-field mutation did not change digest")
			}
		})
	}
}

func TestWithdrawalCanonicalizationAppliesBoundaryDefaultsOnce(t *testing.T) {
	body := []byte(`{
		"client_reference":" payout-1 ",
		"provider_code":" bank ",
		"wallet_id":"550e8400-e29b-41d4-a716-446655440000",
		"amount":"100000",
		"currency":" SDG ",
		"allow_return_to_source":true,
		"idempotency_key":" payout-1 ",
		"destination_id":"9",
		"metadata":{"z":1,"a":"value"},
		"region":" khartoum "
	}`)
	canonical, err := ParsePublic(transactionauth.OperationWalletWithdrawal, "alpha", body, testDefaults)
	if err != nil {
		t.Fatal(err)
	}
	request, ok := canonical.Message.(*walletv1.RequestWithdrawalRequest)
	if !ok {
		t.Fatalf("message = %T", canonical.Message)
	}
	if canonical.IdempotencyKey != "payout-1" || request.GetIdempotencyKey() != "payout-1" ||
		request.GetHoldExpirySeconds() != testDefaults.HoldExpirySeconds ||
		request.GetApprovalTimeoutSeconds() != testDefaults.ApprovalTimeoutSeconds ||
		!request.GetAllowReturnToSource() || request.ApprovalRequired == nil || !request.GetApprovalRequired() ||
		request.GetOwnerType() != "" || request.GetOwnerId() != "" {
		t.Fatalf("canonical request = %+v", request)
	}
	reparsed, err := ParseCanonical(transactionauth.OperationWalletWithdrawal, "alpha", canonical.Body)
	if err != nil {
		t.Fatal(err)
	}
	if reparsed.Digest != canonical.Digest || !bytes.Equal(reparsed.Body, canonical.Body) {
		t.Fatalf("canonicalization is not idempotent: %s != %s", reparsed.Body, canonical.Body)
	}
	reordered := []byte(`{
		"region":"khartoum","metadata":{"a":"value","z":1},"destination_id":9,
		"currency":"SDG","amount":100000,"allow_return_to_source":true,"idempotency_key":"payout-1","wallet_id":"550e8400-e29b-41d4-a716-446655440000",
		"provider_code":"bank","client_reference":"payout-1"
	}`)
	equivalent, err := ParsePublic(transactionauth.OperationWalletWithdrawal, "alpha", reordered, testDefaults)
	if err != nil {
		t.Fatal(err)
	}
	if equivalent.Digest != canonical.Digest {
		t.Fatal("equivalent JSON produced a different digest")
	}
}

func TestCanonicalizationRejectsCallerOwnedIdentityAndUnknownShape(t *testing.T) {
	validP2P := `{"idempotency_key":"key","currency":"SDG","from_wallet_id":"550e8400-e29b-41d4-a716-446655440000","to_wallet_id":"550e8400-e29b-41d4-a716-446655440001","amount":1,"to_owner_type":"user","to_owner_id":"2"}`
	for _, field := range []string{"tenant_id", "tenantId", "from_owner_type", "fromOwnerId"} {
		body := []byte(validP2P[:len(validP2P)-1] + `,"` + field + `":"attacker"}`)
		if _, err := ParsePublic(transactionauth.OperationWalletP2P, "alpha", body, testDefaults); !errors.Is(err, ErrForbiddenIdentityField) {
			t.Fatalf("field %s error = %v", field, err)
		}
	}
	validWithdrawal := `{"client_reference":"ref","provider_code":"bank","wallet_id":"550e8400-e29b-41d4-a716-446655440000","amount":1,"currency":"SDG"}`
	for _, field := range []string{"tenant_id", "owner_type", "ownerId", "approval_required", "approvalRequired"} {
		body := []byte(validWithdrawal[:len(validWithdrawal)-1] + `,"` + field + `":"attacker"}`)
		if _, err := ParsePublic(transactionauth.OperationWalletWithdrawal, "alpha", body, testDefaults); !errors.Is(err, ErrForbiddenIdentityField) {
			t.Fatalf("field %s error = %v", field, err)
		}
	}
	unknown := []byte(validP2P[:len(validP2P)-1] + `,"unexpected_field":"value"}`)
	if _, err := ParsePublic(transactionauth.OperationWalletP2P, "alpha", unknown, testDefaults); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestParseDepositRejectsEveryCallerOwnedAuthorityField(t *testing.T) {
	const valid = `{"provider_code":"bank","wallet_id":"550e8400-e29b-41d4-a716-446655440000","amount":2500,"currency":"AED","idempotency_key":"deposit-1"}`
	for _, field := range []string{
		"tenant_id", "tenantId",
		"client_reference", "clientReference",
		"owner_type", "ownerType", "owner_id", "ownerId",
		"psp_transaction_id", "pspTransactionId",
		"fee_amount", "feeAmount", "net_amount", "netAmount",
	} {
		t.Run(field, func(t *testing.T) {
			body := []byte(valid[:len(valid)-1] + `,"` + field + `":"attacker"}`)
			if _, err := ParseDeposit("alpha", body); !errors.Is(err, ErrForbiddenDepositField) {
				t.Fatalf("ParseDeposit() error = %v, want %v", err, ErrForbiddenDepositField)
			}
		})
	}
}

func TestCanonicalizationRejectsMissingBusinessInputs(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation transactionauth.Operation
		body      string
		want      error
	}{
		{name: "p2p idempotency", operation: transactionauth.OperationWalletP2P, body: `{"currency":"SDG","from_wallet_id":"550e8400-e29b-41d4-a716-446655440000","to_wallet_id":"550e8400-e29b-41d4-a716-446655440001","amount":1,"to_owner_type":"user","to_owner_id":"2"}`, want: ErrMissingIdempotencyKey},
		{name: "p2p pair", operation: transactionauth.OperationWalletP2P, body: `{"idempotency_key":"key","currency":"SDG","from_wallet_id":"550e8400-e29b-41d4-a716-446655440000","to_wallet_id":"550e8400-e29b-41d4-a716-446655440000","amount":1,"to_owner_type":"user","to_owner_id":"2"}`, want: ErrInvalidWalletPair},
		{name: "withdrawal amount", operation: transactionauth.OperationWalletWithdrawal, body: `{"client_reference":"ref","provider_code":"bank","wallet_id":"550e8400-e29b-41d4-a716-446655440000","amount":0,"currency":"SDG","allow_return_to_source":true}`, want: ErrInvalidAmount},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParsePublic(test.operation, "alpha", []byte(test.body), testDefaults); !errors.Is(err, test.want) {
				t.Fatalf("Parse() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestWithdrawalRequiresExplicitPoliciesAtEveryBoundary(t *testing.T) {
	body := []byte(`{
		"client_reference":"payout-1",
		"idempotency_key":"payout-1",
		"provider_code":"bank",
		"wallet_id":"550e8400-e29b-41d4-a716-446655440000",
		"amount":100000,
		"currency":"SDG",
		"allow_return_to_source":true,
		"destination_id":9
	}`)
	canonical, err := ParsePublic(transactionauth.OperationWalletWithdrawal, "alpha", body, testDefaults)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCanonical(transactionauth.OperationWalletWithdrawal, "alpha", canonical.Body); err != nil {
		t.Fatalf("canonical internal request rejected: %v", err)
	}
	missingReturnPolicy := []byte(`{
		"client_reference":"payout-1","idempotency_key":"payout-1","provider_code":"bank",
		"wallet_id":"550e8400-e29b-41d4-a716-446655440000","amount":100000,"currency":"SDG",
		"destination_id":9
	}`)
	if _, err := ParsePublic(transactionauth.OperationWalletWithdrawal, "alpha", missingReturnPolicy, testDefaults); !errors.Is(err, ErrMissingReturnToSourcePolicy) {
		t.Fatalf("missing public return policy error = %v, want %v", err, ErrMissingReturnToSourcePolicy)
	}
	missingApprovalPolicy := []byte(`{
		"client_reference":"payout-1","idempotency_key":"payout-1","provider_code":"bank",
		"wallet_id":"550e8400-e29b-41d4-a716-446655440000","amount":100000,"currency":"SDG",
		"allow_return_to_source":true,"destination_id":9,"hold_expiry_seconds":3600,
		"approval_timeout_seconds":7200
	}`)
	if _, err := ParseCanonical(transactionauth.OperationWalletWithdrawal, "alpha", missingApprovalPolicy); !errors.Is(err, ErrMissingApprovalPolicy) {
		t.Fatalf("missing internal approval policy error = %v, want %v", err, ErrMissingApprovalPolicy)
	}
}

func TestWithdrawalCanonicalizationPreservesFalseApprovalPolicy(t *testing.T) {
	body := []byte(`{
		"client_reference":"payout-low","idempotency_key":"payout-low","provider_code":"bank",
		"wallet_id":"550e8400-e29b-41d4-a716-446655440000","amount":1,"currency":"SDG",
		"allow_return_to_source":true
	}`)
	canonical, err := ParsePublic(transactionauth.OperationWalletWithdrawal, "alpha", body, testDefaults)
	if err != nil {
		t.Fatal(err)
	}
	request := canonical.Message.(*walletv1.RequestWithdrawalRequest)
	if request.ApprovalRequired == nil || request.GetApprovalRequired() {
		t.Fatalf("approval policy = %v, want present false", request.ApprovalRequired)
	}
	if !bytes.Contains(canonical.Body, []byte(`"approval_required":false`)) {
		t.Fatalf("canonical body omitted false approval policy: %s", canonical.Body)
	}
	reparsed, err := ParseCanonical(transactionauth.OperationWalletWithdrawal, "alpha", canonical.Body)
	if err != nil {
		t.Fatal(err)
	}
	if reparsed.Digest != canonical.Digest || !bytes.Equal(reparsed.Body, canonical.Body) {
		t.Fatalf("false approval policy is not stable: %s != %s", reparsed.Body, canonical.Body)
	}
}

func TestCanonicalizationRejectsNegativeDestinationAndZeroWalletIDs(t *testing.T) {
	tests := []struct {
		name      string
		operation transactionauth.Operation
		body      string
		want      error
	}{
		{
			name:      "negative withdrawal destination",
			operation: transactionauth.OperationWalletWithdrawal,
			body:      `{"client_reference":"ref","idempotency_key":"ref","provider_code":"bank","wallet_id":"550e8400-e29b-41d4-a716-446655440000","amount":1,"currency":"SDG","allow_return_to_source":true,"destination_id":-1}`,
			want:      ErrInvalidDestinationID,
		},
		{
			name:      "zero withdrawal wallet",
			operation: transactionauth.OperationWalletWithdrawal,
			body:      `{"client_reference":"ref","idempotency_key":"ref","provider_code":"bank","wallet_id":"00000000-0000-0000-0000-000000000000","amount":1,"currency":"SDG","allow_return_to_source":true}`,
			want:      ErrInvalidWalletID,
		},
		{
			name:      "zero p2p source wallet",
			operation: transactionauth.OperationWalletP2P,
			body:      `{"idempotency_key":"key","reference_id":"key","currency":"SDG","from_wallet_id":"00000000-0000-0000-0000-000000000000","to_wallet_id":"550e8400-e29b-41d4-a716-446655440001","amount":1,"to_owner_type":"user","to_owner_id":"2"}`,
			want:      ErrInvalidWalletID,
		},
		{
			name:      "zero p2p destination wallet",
			operation: transactionauth.OperationWalletP2P,
			body:      `{"idempotency_key":"key","reference_id":"key","currency":"SDG","from_wallet_id":"550e8400-e29b-41d4-a716-446655440000","to_wallet_id":"00000000-0000-0000-0000-000000000000","amount":1,"to_owner_type":"user","to_owner_id":"2"}`,
			want:      ErrInvalidWalletID,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParsePublic(test.operation, "alpha", []byte(test.body), testDefaults); !errors.Is(err, test.want) {
				t.Fatalf("ParsePublic() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestWithdrawalRejectsInvalidOrInapplicableTimeouts(t *testing.T) {
	base := []byte(`{
		"client_reference":"payout-1",
		"idempotency_key":"payout-1",
		"provider_code":"bank",
		"wallet_id":"550e8400-e29b-41d4-a716-446655440000",
		"amount":1,
		"currency":"SDG",
		"allow_return_to_source":true
	}`)
	for name, test := range map[string]struct {
		field string
		want  error
	}{
		"negative hold": {
			field: `"hold_expiry_seconds":-1`,
			want:  ErrInvalidTimeout,
		},
		"removed verification policy": {
			field: `"verification_timeout_seconds":1`,
			want:  ErrInvalidRequest,
		},
		"unrequired approval": {
			field: `"approval_timeout_seconds":1`,
			want:  ErrInvalidTimeout,
		},
	} {
		t.Run(name, func(t *testing.T) {
			body := append(bytes.TrimSuffix(bytes.Clone(base), []byte("}")), []byte(","+test.field+"}")...)
			if _, err := ParsePublic(transactionauth.OperationWalletWithdrawal, "alpha", body, testDefaults); !errors.Is(err, test.want) {
				t.Fatalf("ParsePublic() error = %v, want %v", err, test.want)
			}
		})
	}
}
