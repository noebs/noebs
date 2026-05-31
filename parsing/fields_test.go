package parsing

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestStringOrDefault(t *testing.T) {
	fields := map[string]any{
		"name":  "noebs",
		"count": 3,
	}
	if got, ok := StringOrDefault(fields, "name", "fallback"); got != "noebs" || !ok {
		t.Fatalf("StringOrDefault(name) = %q, %v", got, ok)
	}
	if got, ok := StringOrDefault(fields, "missing", "fallback"); got != "fallback" || ok {
		t.Fatalf("StringOrDefault(missing) = %q, %v", got, ok)
	}
	if got, ok := StringOrDefault(fields, "count", "fallback"); got != "fallback" || ok {
		t.Fatalf("StringOrDefault(non-string) = %q, %v", got, ok)
	}
}

func TestRequiredString(t *testing.T) {
	got, err := RequiredString(map[string]any{"name": "noebs"}, "name")
	if err != nil {
		t.Fatalf("RequiredString() error = %v", err)
	}
	if got != "noebs" {
		t.Fatalf("RequiredString() = %q", got)
	}

	for _, fields := range []map[string]any{nil, map[string]any{"other": "value"}} {
		if _, err := RequiredString(fields, "name"); !errors.Is(err, ErrMissingField) {
			t.Fatalf("RequiredString(missing) error = %v, want %v", err, ErrMissingField)
		}
	}
	for _, fields := range []map[string]any{map[string]any{"name": ""}, map[string]any{"name": 10}} {
		if _, err := RequiredString(fields, "name"); !errors.Is(err, ErrInvalidField) {
			t.Fatalf("RequiredString(invalid) error = %v, want %v", err, ErrInvalidField)
		}
	}
}

func TestRequiredFloat64(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		wanted float64
	}{
		{"string", "10.5", 10.5},
		{"json number", json.Number("11.25"), 11.25},
		{"float64", 12.5, 12.5},
		{"int", 13, 13},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RequiredFloat64(map[string]any{"amount": tt.value}, "amount")
			if err != nil {
				t.Fatalf("RequiredFloat64() error = %v", err)
			}
			if got != tt.wanted {
				t.Fatalf("RequiredFloat64() = %v, want %v", got, tt.wanted)
			}
		})
	}

	if _, err := RequiredFloat64(map[string]any{}, "amount"); !errors.Is(err, ErrMissingField) {
		t.Fatalf("RequiredFloat64(missing) error = %v, want %v", err, ErrMissingField)
	}
	if _, err := RequiredFloat64(map[string]any{"amount": "bad"}, "amount"); !errors.Is(err, ErrInvalidField) {
		t.Fatalf("RequiredFloat64(invalid) error = %v, want %v", err, ErrInvalidField)
	}
}

func TestRequiredFloat64RejectsNonFiniteValues(t *testing.T) {
	cases := []struct {
		name  string
		value any
	}{
		{"string nan", "NaN"},
		{"string infinity", "+Inf"},
		{"json number nan", json.Number("NaN")},
		{"float64 nan", math.NaN()},
		{"float64 infinity", math.Inf(1)},
		{"float32 infinity", float32(math.Inf(-1))},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := RequiredFloat64(map[string]any{"amount": tt.value}, "amount")
			if !errors.Is(err, ErrInvalidField) {
				t.Fatalf("RequiredFloat64(non-finite) error = %v, want %v", err, ErrInvalidField)
			}
		})
	}
}
