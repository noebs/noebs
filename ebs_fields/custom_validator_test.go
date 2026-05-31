package ebs_fields

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
)

func TestValidateStructReturnsValidationErrors(t *testing.T) {
	type request struct {
		Name string `json:"name" binding:"required"`
	}

	err := ValidateStruct(request{})
	var validationErrs validator.ValidationErrors
	if !errors.As(err, &validationErrs) {
		t.Fatalf("ValidateStruct() error = %v, want validation errors", err)
	}
	if len(validationErrs) != 1 {
		t.Fatalf("validation errors = %d, want 1", len(validationErrs))
	}
	if validationErrs[0].Field() != "name" {
		t.Fatalf("field = %q, want json field name", validationErrs[0].Field())
	}
}

func TestValidatorSetupDoesNotTerminateProcess(t *testing.T) {
	source, err := os.ReadFile("custom_validator.go")
	if err != nil {
		t.Fatalf("read custom_validator.go: %v", err)
	}
	for _, token := range []string{"log.Fatal", "Fatalf(", "panic("} {
		if strings.Contains(string(source), token) {
			t.Fatalf("custom_validator.go must return initialization errors, not terminate with %q", token)
		}
	}
}
