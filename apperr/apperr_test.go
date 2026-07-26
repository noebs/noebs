package apperr

import (
	"errors"
	"net/http"
	"reflect"
	"testing"
)

func TestPayloadRedactsInternalCauses(t *testing.T) {
	cause := errors.New("dial postgres://operator:secret@db.internal")
	err := Wrap(cause, ErrDatabase, cause.Error())

	if !errors.Is(err, cause) {
		t.Fatal("wrapped error no longer preserves its cause")
	}
	got := Payload(err)
	want := map[string]any{
		"code":    "database_error",
		"message": "internal server error",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Payload() = %#v, want %#v", got, want)
	}
}

func TestPayloadRedactsUnknownErrors(t *testing.T) {
	got := Payload(errors.New("PAN 1234567890123456"))
	want := map[string]any{
		"code":    "internal_error",
		"message": "internal server error",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Payload() = %#v, want %#v", got, want)
	}
}

func TestPayloadPreservesClientValidationDetails(t *testing.T) {
	err := WithFields(
		New("invalid_currency", http.StatusBadRequest, "currency is not supported"),
		map[string]any{"currency": "XYZ"},
	)
	got := Payload(err)
	want := map[string]any{
		"code":    "invalid_currency",
		"message": "currency is not supported",
		"fields":  map[string]any{"currency": "XYZ"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Payload() = %#v, want %#v", got, want)
	}
}

func TestMessageNamesUnavailableBoundaries(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{err: Wrap(errors.New("connection refused"), ErrUnavailable, ""), want: "service unavailable"},
		{err: Wrap(errors.New("connection refused"), ErrBadGateway, ""), want: "upstream service unavailable"},
	}
	for _, test := range tests {
		if got := Message(test.err); got != test.want {
			t.Fatalf("Message(%v) = %q, want %q", test.err, got, test.want)
		}
	}
}
