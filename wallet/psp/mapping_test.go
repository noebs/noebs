package psp

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestMapResponseUsesConfiguredPaths(t *testing.T) {
	payload := map[string]any{
		"result": map[string]any{
			"provider_id": "psp-123",
			"state":       "SUCCESS",
			"minor_units": float64(2500),
			"currency":    "AED",
			"direction":   "inbound",
			"message":     "accepted",
		},
		"meta": map[string]any{"card_last4": "4242"},
	}
	mapped, err := MapResponse(payload, ResponseMapping{
		TransactionID: []string{"result.provider_id"},
		Status:        []string{"result.state"},
		Amount:        []string{"result.minor_units"},
		Currency:      []string{"result.currency"},
		Direction:     []string{"result.direction"},
		Message:       []string{"result.message"},
		Metadata:      []string{"meta"},
	})
	if err != nil {
		t.Fatalf("MapResponse() error = %v", err)
	}

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
		t.Fatalf("expected mapped currency AED, got %q", mapped.Currency)
	}
	if mapped.Direction != "inbound" {
		t.Fatalf("expected mapped direction inbound, got %q", mapped.Direction)
	}
	if mapped.Message != "accepted" {
		t.Fatalf("expected mapped message accepted, got %q", mapped.Message)
	}
	if mapped.Metadata["card_last4"] != "4242" {
		t.Fatalf("expected mapped metadata, got %v", mapped.Metadata)
	}
}

func TestMapResponseDoesNotDefaultMissingCurrency(t *testing.T) {
	payload := map[string]any{
		"result": map[string]any{
			"minor_units": float64(2500),
		},
	}
	mapped, err := MapResponse(payload, ResponseMapping{
		Amount:   []string{"result.minor_units"},
		Currency: []string{"result.currency"},
	})
	if err != nil {
		t.Fatalf("MapResponse() error = %v", err)
	}

	if mapped.Amount != 2500 {
		t.Fatalf("expected amount 2500, got %d", mapped.Amount)
	}
	if mapped.Currency != "" {
		t.Fatalf("expected empty currency without mapped provider value, got %q", mapped.Currency)
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
	mapped, err := MapResponse(payload, ResponseMapping{})
	if err != nil {
		t.Fatalf("MapResponse() error = %v", err)
	}
	if mapped.ClientReference != "" || mapped.TransactionID != "" || mapped.Status != "" || mapped.Amount != 0 || mapped.Currency != "" || mapped.Direction != "" || mapped.Message != "" {
		t.Fatalf("MapResponse() = %+v, want empty fields without configured paths", mapped)
	}
}

func TestMapResponsePreservesNumericStringFields(t *testing.T) {
	payload := map[string]any{
		"client_reference": float64(1000),
		"provider_id":      float64(2500),
		"message":          json.Number("9000"),
	}
	mapped, err := MapResponse(payload, ResponseMapping{
		ClientReference: []string{"client_reference"},
		TransactionID:   []string{"provider_id"},
		Message:         []string{"message"},
	})
	if err != nil {
		t.Fatalf("MapResponse() error = %v", err)
	}
	if mapped.ClientReference != "1000" {
		t.Fatalf("client reference = %q, want 1000", mapped.ClientReference)
	}
	if mapped.TransactionID != "2500" {
		t.Fatalf("transaction id = %q, want 2500", mapped.TransactionID)
	}
	if mapped.Message != "9000" {
		t.Fatalf("message = %q, want 9000", mapped.Message)
	}
}

func TestMapResponseRejectsInvalidConfiguredAmount(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{name: "missing", value: nil},
		{name: "fractional string", value: "12.34"},
		{name: "fractional float", value: float64(12.5)},
		{name: "json decimal", value: json.Number("12.5")},
		{name: "object", value: map[string]any{"amount": 1200}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := map[string]any{"result": map[string]any{}}
			if tt.value != nil {
				payload["result"].(map[string]any)["minor_units"] = tt.value
			}
			_, err := MapResponse(payload, ResponseMapping{Amount: []string{"result.minor_units"}})
			if !errors.Is(err, ErrPSPResponseInvalid) {
				t.Fatalf("MapResponse() error = %v, want %v", err, ErrPSPResponseInvalid)
			}
		})
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
	mapped, err := MapRequest(input, RequestMapping{
		Static: map[string]any{"channel": "bank"},
		Fields: map[string]string{
			"reference":        "client_reference",
			"payment.amount":   "amount",
			"payment.currency": "currency",
			"beneficiary.iban": "destination.iban",
		},
	})
	if err != nil {
		t.Fatalf("MapRequest() error = %v", err)
	}

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

func TestMapRequestRejectsMissingConfiguredSource(t *testing.T) {
	_, err := MapRequest(map[string]any{"amount": int64(1500)}, RequestMapping{
		Fields: map[string]string{"payment.amount": "missing.amount"},
	})
	if !errors.Is(err, ErrPSPRequestInvalid) {
		t.Fatalf("MapRequest() error = %v, want %v", err, ErrPSPRequestInvalid)
	}
}

func TestMapRequestRejectsInvalidConfiguredPaths(t *testing.T) {
	tests := []struct {
		name    string
		mapping RequestMapping
	}{
		{
			name:    "empty target",
			mapping: RequestMapping{Fields: map[string]string{" ": "amount"}},
		},
		{
			name:    "empty source",
			mapping: RequestMapping{Fields: map[string]string{"payment.amount": " "}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := MapRequest(map[string]any{"amount": int64(1500)}, tt.mapping)
			if !errors.Is(err, ErrPSPConfigInvalid) {
				t.Fatalf("MapRequest() error = %v, want %v", err, ErrPSPConfigInvalid)
			}
		})
	}
}
