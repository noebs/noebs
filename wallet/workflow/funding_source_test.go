package workflow

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

func TestDepositFundingSourceCapturesMethodMetadata(t *testing.T) {
	walletID := uuid.New()
	now := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	txn := &walletstore.PSPTransaction{
		TenantID:    "tenant",
		PSPProvider: "korapay",
		RawRequest: walletstore.RawJSON(`{
			"metadata": {
				"funding_source": {
					"source_type": "bank_account",
					"external_reference": "bank-ref-1",
					"supports_withdrawal": true,
					"source_details": {"bank_code": "044", "account_last4": "4321"},
					"withdrawal_method": {"account_number": "1234567890", "bank_code": "044"}
				}
			}
		}`),
	}
	providerPayload := map[string]any{
		"metadata": map[string]any{
			"funding_source": map[string]any{
				"external_reference": "bank-ref-1",
			},
		},
	}

	source, err := depositFundingSource(txn, walletID, "AED", 5000, sql.NullString{String: "provider-tx", Valid: true}, false, now, providerPayload)
	if err != nil {
		t.Fatalf("deposit funding source: %v", err)
	}
	if source.SourceType != "bank_account" {
		t.Fatalf("expected bank source type, got %q", source.SourceType)
	}
	if !source.ExternalReference.Valid || source.ExternalReference.String != "bank-ref-1" {
		t.Fatalf("unexpected external reference: %+v", source.ExternalReference)
	}
	if source.VerificationStatus != "verified" || !source.VerifiedAt.Valid {
		t.Fatalf("expected verified source, got status=%q verified_at=%+v", source.VerificationStatus, source.VerifiedAt)
	}
	if !source.SupportsWithdrawal {
		t.Fatal("expected withdrawal support from method metadata")
	}

	var details map[string]any
	if err := json.Unmarshal(source.SourceDetails, &details); err != nil {
		t.Fatalf("source details: %v", err)
	}
	if details["bank_code"] != "044" || details["source_type"] != "bank_account" {
		t.Fatalf("unexpected source details: %+v", details)
	}
	var withdrawal map[string]any
	if err := json.Unmarshal(source.WithdrawalMethod, &withdrawal); err != nil {
		t.Fatalf("withdrawal method: %v", err)
	}
	if withdrawal["account_number"] != "1234567890" {
		t.Fatalf("unexpected withdrawal method: %+v", withdrawal)
	}
}

func TestDepositFundingSourceRequiresVerificationWhenProviderIdentifierMissing(t *testing.T) {
	walletID := uuid.New()
	txn := &walletstore.PSPTransaction{
		TenantID:    "tenant",
		PSPProvider: "pospay",
		RawRequest: walletstore.RawJSON(`{
			"metadata": {
				"funding_source": {
					"source_type": "bank_account",
					"external_reference": "requested-bank-ref",
					"source_details": {"account_last4": "1111"}
				}
			}
		}`),
	}

	source, err := depositFundingSource(txn, walletID, "NGN", 2000, sql.NullString{String: "provider-tx", Valid: true}, true, time.Now().UTC())
	if err != nil {
		t.Fatalf("deposit funding source: %v", err)
	}
	if source.VerificationStatus != "pending" {
		t.Fatalf("expected pending source verification, got %q", source.VerificationStatus)
	}
	if source.VerifiedAt.Valid {
		t.Fatalf("expected no verified_at for pending source, got %+v", source.VerifiedAt)
	}
}

func TestDepositFundingSourcePreservesLegacyVerifiedPSPSource(t *testing.T) {
	walletID := uuid.New()
	txn := &walletstore.PSPTransaction{
		TenantID:    "tenant",
		PSPProvider: "checkout",
		RawRequest:  walletstore.RawJSON(`{"metadata": {"note": "legacy request"}}`),
	}

	source, err := depositFundingSource(txn, walletID, "USD", 3000, sql.NullString{String: "provider-tx", Valid: true}, false, time.Now().UTC())
	if err != nil {
		t.Fatalf("deposit funding source: %v", err)
	}
	if source.SourceType != "psp" {
		t.Fatalf("expected psp source type, got %q", source.SourceType)
	}
	if !source.ExternalReference.Valid || source.ExternalReference.String != "provider-tx" {
		t.Fatalf("unexpected external reference: %+v", source.ExternalReference)
	}
	if source.VerificationStatus != "verified" {
		t.Fatalf("expected legacy source to remain verified, got %q", source.VerificationStatus)
	}
}

func TestSelectReturnToSourceSkipsIneligibleFundingSources(t *testing.T) {
	walletID := uuid.New()
	withdrawalMethod := json.RawMessage(`{"account_number":"1234567890","bank_code":"044"}`)
	sources := []walletstore.FundingSource{
		{
			ID:                 1,
			WalletID:           walletID,
			PSPProvider:        sql.NullString{String: "bankpay", Valid: true},
			Currency:           "AED",
			VerificationStatus: "pending",
			SupportsWithdrawal: true,
			WithdrawalMethod:   withdrawalMethod,
			TotalFunded:        10000,
		},
		{
			ID:                 2,
			WalletID:           walletID,
			PSPProvider:        sql.NullString{String: "bankpay", Valid: true},
			Currency:           "AED",
			VerificationStatus: "verified",
			SupportsWithdrawal: true,
			WithdrawalMethod:   withdrawalMethod,
			TotalFunded:        100,
		},
		{
			ID:                 3,
			WalletID:           walletID,
			PSPProvider:        sql.NullString{String: "bankpay", Valid: true},
			Currency:           "AED",
			VerificationStatus: "verified",
			SupportsWithdrawal: true,
			WithdrawalMethod:   withdrawalMethod,
			TotalFunded:        10000,
		},
	}

	selected, details, err := selectReturnToSource(sources, "AED", 500, "bankpay")
	if err != nil {
		t.Fatalf("select return-to-source: %v", err)
	}
	if selected == nil || selected.ID != 3 {
		t.Fatalf("expected third source, got %+v", selected)
	}
	if details["account_number"] != "1234567890" {
		t.Fatalf("unexpected details: %+v", details)
	}
}
