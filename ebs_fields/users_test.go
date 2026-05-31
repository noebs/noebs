package ebs_fields

import (
	"errors"
	"testing"
)

func TestExpandCardMatchesByFirstAndLastFourDigits(t *testing.T) {
	got, err := ExpandCard("922208*****0000", []Card{
		{Pan: "9222081700000000"},
	})
	if err != nil {
		t.Fatalf("ExpandCard() error = %v", err)
	}
	if got != "9222081700000000" {
		t.Fatalf("ExpandCard() = %q, want full PAN", got)
	}
}

func TestExpandCardRejectsMalformedSelectorWithoutPanic(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("ExpandCard() panicked: %v", recovered)
		}
	}()

	_, err := ExpandCard("****0000", []Card{{Pan: "9222081700000000"}})
	if !errors.Is(err, ErrInvalidCardQuery) {
		t.Fatalf("ExpandCard() error = %v, want %v", err, ErrInvalidCardQuery)
	}
}

func TestExpandCardRejectsAmbiguousMatches(t *testing.T) {
	_, err := ExpandCard("9222*****0000", []Card{
		{Pan: "9222081700000000"},
		{Pan: "9222999900000000"},
	})
	if !errors.Is(err, ErrAmbiguousCardQuery) {
		t.Fatalf("ExpandCard() error = %v, want %v", err, ErrAmbiguousCardQuery)
	}
}
