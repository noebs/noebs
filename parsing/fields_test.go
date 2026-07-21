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

func TestTextParsing(t *testing.T) {
	if got, ok := Text(" noebs "); got != "noebs" || !ok {
		t.Fatalf("Text() = %q, %v; want noebs, true", got, ok)
	}
	if got, ok := Text(" \t "); got != "" || ok {
		t.Fatalf("Text(blank) = %q, %v; want empty, false", got, ok)
	}
	if !MissingText(" \t ") {
		t.Fatal("MissingText(blank) = false, want true")
	}
	if MissingText(" value ") {
		t.Fatal("MissingText(value) = true, want false")
	}
	if got := TextOrDefault(" explicit ", "fallback"); got != "explicit" {
		t.Fatalf("TextOrDefault(value) = %q, want explicit", got)
	}
	if got := TextOrDefault(" \t ", "fallback"); got != "fallback" {
		t.Fatalf("TextOrDefault(blank) = %q, want fallback", got)
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

func TestStringParam(t *testing.T) {
	if got := StringParam(map[string]string{"name": " noebs "}, "name"); got != "noebs" {
		t.Fatalf("StringParam() = %q, want noebs", got)
	}
	if got := StringParam(nil, "name"); got != "" {
		t.Fatalf("StringParam(nil) = %q, want empty", got)
	}
}

func TestBoolParam(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "missing", want: false},
		{name: "checkbox on", raw: "on", want: true},
		{name: "checkbox off", raw: "off", want: false},
		{name: "true", raw: "true", want: true},
		{name: "false", raw: "false", want: false},
		{name: "one", raw: "1", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := map[string]string{}
			if tt.raw != "" {
				values["active"] = tt.raw
			}
			got, err := BoolParam(values, "active")
			if err != nil {
				t.Fatalf("BoolParam() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("BoolParam() = %v, want %v", got, tt.want)
			}
		})
	}
	if _, err := BoolParam(map[string]string{"active": "maybe"}, "active"); !errors.Is(err, ErrInvalidField) {
		t.Fatalf("BoolParam(invalid) error = %v, want %v", err, ErrInvalidField)
	}
}

func TestIntParams(t *testing.T) {
	if got, err := PositiveIntOrDefaultParam(map[string]string{}, "limit", 50); err != nil || got != 50 {
		t.Fatalf("PositiveIntOrDefaultParam(default) = %d, %v; want 50, nil", got, err)
	}
	if got, err := PositiveIntOrDefaultParam(map[string]string{"limit": "25"}, "limit", 50); err != nil || got != 25 {
		t.Fatalf("PositiveIntOrDefaultParam(value) = %d, %v; want 25, nil", got, err)
	}
	if _, err := PositiveIntOrDefaultParam(map[string]string{"limit": "0"}, "limit", 50); !errors.Is(err, ErrInvalidField) {
		t.Fatalf("PositiveIntOrDefaultParam(invalid) error = %v, want %v", err, ErrInvalidField)
	}
	if got, err := NonNegativeIntOrDefaultParam(map[string]string{"offset": "0"}, "offset", 5); err != nil || got != 0 {
		t.Fatalf("NonNegativeIntOrDefaultParam(value) = %d, %v; want 0, nil", got, err)
	}
	if _, err := NonNegativeIntOrDefaultParam(map[string]string{"offset": "-1"}, "offset", 5); !errors.Is(err, ErrInvalidField) {
		t.Fatalf("NonNegativeIntOrDefaultParam(invalid) error = %v, want %v", err, ErrInvalidField)
	}
}

func TestInt64Params(t *testing.T) {
	if got, ok, err := PositiveInt64Param(map[string]string{"amount": "10"}, "amount"); err != nil || !ok || got != 10 {
		t.Fatalf("PositiveInt64Param() = %d, %v, %v; want 10, true, nil", got, ok, err)
	}
	if _, ok, err := PositiveInt64Param(map[string]string{}, "amount"); err != nil || ok {
		t.Fatalf("PositiveInt64Param(missing) ok=%v err=%v; want false, nil", ok, err)
	}
	if _, _, err := PositiveInt64Param(map[string]string{"amount": "0"}, "amount"); !errors.Is(err, ErrInvalidField) {
		t.Fatalf("PositiveInt64Param(invalid) error = %v, want %v", err, ErrInvalidField)
	}
	if got, ok, err := NonNegativeInt64Param(map[string]string{"amount": "0"}, "amount"); err != nil || !ok || got != 0 {
		t.Fatalf("NonNegativeInt64Param() = %d, %v, %v; want 0, true, nil", got, ok, err)
	}
	if _, _, err := NonNegativeInt64Param(map[string]string{"amount": "-1"}, "amount"); !errors.Is(err, ErrInvalidField) {
		t.Fatalf("NonNegativeInt64Param(invalid) error = %v, want %v", err, ErrInvalidField)
	}
}

func TestRFC3339Param(t *testing.T) {
	got, ok, err := RFC3339Param(map[string]string{"start": "2026-05-31T12:00:00Z"}, "start")
	if err != nil || !ok || got.Year() != 2026 {
		t.Fatalf("RFC3339Param() = %v, %v, %v; want 2026, true, nil", got, ok, err)
	}
	if _, ok, err := RFC3339Param(map[string]string{}, "start"); err != nil || ok {
		t.Fatalf("RFC3339Param(missing) ok=%v err=%v; want false, nil", ok, err)
	}
	if _, _, err := RFC3339Param(map[string]string{"start": "bad"}, "start"); !errors.Is(err, ErrInvalidField) {
		t.Fatalf("RFC3339Param(invalid) error = %v, want %v", err, ErrInvalidField)
	}
}
