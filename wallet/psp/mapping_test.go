package psp

import "testing"

func TestMapResponseUsesConfiguredPathsAndDefaultCurrency(t *testing.T) {
	payload := map[string]any{
		"result": map[string]any{
			"provider_id": "psp-123",
			"state":       "SUCCESS",
			"minor_units": float64(2500),
		},
		"meta": map[string]any{"card_last4": "4242"},
	}
	mapped := MapResponse(payload, ResponseMapping{
		TransactionID: []string{"result.provider_id"},
		Status:        []string{"result.state"},
		Amount:        []string{"result.minor_units"},
		Currency:      []string{"result.currency"},
		Metadata:      []string{"meta"},
	}, "AED")

	if mapped.TransactionID != "psp-123" {
		t.Fatalf("expected provider id psp-123, got %q", mapped.TransactionID)
	}
	if mapped.Status != "success" {
		t.Fatalf("expected normalized success status, got %q", mapped.Status)
	}
	if mapped.Amount != 2500 {
		t.Fatalf("expected amount 2500, got %d", mapped.Amount)
	}
	if mapped.Currency != "AED" {
		t.Fatalf("expected default currency AED, got %q", mapped.Currency)
	}
	if mapped.Metadata["card_last4"] != "4242" {
		t.Fatalf("expected mapped metadata, got %v", mapped.Metadata)
	}
}

func TestMapResponseRequiresConfiguredPaths(t *testing.T) {
	payload := map[string]any{
		"client_reference":   "front-ref",
		"psp_transaction_id": "psp-123",
		"status":             "pending",
		"amount":             "1200",
		"currency":           "USD",
	}
	mapped := MapResponse(payload, ResponseMapping{}, "")
	if mapped.ClientReference != "" || mapped.TransactionID != "" || mapped.Status != "" || mapped.Amount != 0 || mapped.Currency != "" {
		t.Fatalf("MapResponse() = %+v, want empty fields without configured paths", mapped)
	}
}

func TestMapRequestUsesStaticAndConfiguredFields(t *testing.T) {
	input := map[string]any{
		"client_reference": "front-ref",
		"amount":           int64(1500),
		"currency":         "AED",
		"destination": map[string]any{
			"iban": "AE070331234567890123456",
		},
	}
	mapped := MapRequest(input, RequestMapping{
		Static: map[string]any{"channel": "bank"},
		Fields: map[string]string{
			"reference":        "client_reference",
			"payment.amount":   "amount",
			"payment.currency": "currency",
			"beneficiary.iban": "destination.iban",
		},
	})

	if mapped["channel"] != "bank" || mapped["reference"] != "front-ref" {
		t.Fatalf("unexpected top-level mapped request: %+v", mapped)
	}
	payment, ok := mapped["payment"].(map[string]any)
	if !ok || payment["amount"] != int64(1500) || payment["currency"] != "AED" {
		t.Fatalf("unexpected payment mapping: %+v", mapped["payment"])
	}
	beneficiary, ok := mapped["beneficiary"].(map[string]any)
	if !ok || beneficiary["iban"] != "AE070331234567890123456" {
		t.Fatalf("unexpected beneficiary mapping: %+v", mapped["beneficiary"])
	}
}
