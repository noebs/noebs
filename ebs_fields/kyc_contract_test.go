package ebs_fields

import (
	"encoding/json"
	"testing"
)

func TestKYCPassportUsesFlatJSONContract(t *testing.T) {
	var request KYCPassport
	if err := json.Unmarshal([]byte(`{
		"mobile":"0990000001",
		"passport_number":"P123",
		"national_number":"N123",
		"selfie":"selfie-base64",
		"passport_image":"passport-base64"
	}`), &request); err != nil {
		t.Fatalf("unmarshal flat KYC request: %v", err)
	}
	if request.Mobile != "0990000001" || request.PassportNumber != "P123" || request.NationalNumber != "N123" {
		t.Fatalf("passport fields = %+v", request.Passport)
	}
	if request.Selfie != "selfie-base64" || request.PassportImg != "passport-base64" {
		t.Fatalf("image fields = (%q, %q)", request.Selfie, request.PassportImg)
	}

	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal flat KYC request: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("decode marshaled KYC request: %v", err)
	}
	if _, nested := fields["passport"]; nested {
		t.Fatalf("KYC payload must remain flat: %s", payload)
	}
	for _, field := range []string{"mobile", "passport_number", "national_number", "selfie", "passport_image"} {
		if _, ok := fields[field]; !ok {
			t.Fatalf("flat KYC payload missing %q: %s", field, payload)
		}
	}
}
