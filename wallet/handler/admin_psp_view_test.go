package handler

import (
	"bytes"
	"context"
	"strings"
	"testing"

	walletstore "github.com/adonese/noebs/wallet/store"
)

func TestPSPAdminViewsNameProviderStateAndHideRawPayloads(t *testing.T) {
	transaction := walletstore.PSPTransaction{
		TenantID:        "tenant-a",
		PSPProvider:     "provider-a",
		ClientReference: "reference-a",
		Direction:       "outbound",
		Status:          "success",
		Currency:        "SDG",
		CurrencyUnitID:  4,
		RawRequest:      walletstore.RawJSON(`{"account":"request-secret"}`),
		RawResponse:     walletstore.RawJSON(`{"token":"response-secret"}`),
	}

	var listOutput bytes.Buffer
	if err := PSPTransactionsPage(PSPTransactionsView{
		TenantID:     "tenant-a",
		Transactions: []walletstore.PSPTransaction{transaction},
	}).Render(context.Background(), &listOutput); err != nil {
		t.Fatalf("render PSP list: %v", err)
	}
	for _, want := range []string{"Provider status", "Amount (minor units)", "Currency / unit version", "SDG / 4"} {
		if !strings.Contains(listOutput.String(), want) {
			t.Fatalf("PSP list missing %q: %s", want, listOutput.String())
		}
	}

	var detailOutput bytes.Buffer
	if err := PSPTransactionDetailPage(PSPTransactionDetailView{
		TenantID:    "tenant-a",
		Transaction: transaction,
	}).Render(context.Background(), &detailOutput); err != nil {
		t.Fatalf("render PSP detail: %v", err)
	}
	detail := detailOutput.String()
	for _, want := range []string{
		"Provider status:",
		"It does not prove ledger completion or external settlement.",
		"Amount (minor units):",
		"Currency unit version: 4",
		"Raw provider payloads are not shown in this view.",
	} {
		if !strings.Contains(detail, want) {
			t.Fatalf("PSP detail missing %q: %s", want, detail)
		}
	}
	for _, secret := range []string{"request-secret", "response-secret", "Raw Request", "Raw Response"} {
		if strings.Contains(detail, secret) {
			t.Fatalf("PSP detail exposed %q", secret)
		}
	}
}
