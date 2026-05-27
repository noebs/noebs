package consumer

import (
	"errors"
	"testing"
)

func TestTelecomPaymentPhoneRequiresExplicitPaymentInfoFormat(t *testing.T) {
	got, err := telecomPaymentPhone("MPHONE=249912345678")
	if err != nil {
		t.Fatalf("telecomPaymentPhone() error = %v", err)
	}
	if got != "0249912345678" {
		t.Fatalf("phone = %q, want %q", got, "0249912345678")
	}

	if _, err := telecomPaymentPhone("249912345678"); !errors.Is(err, ErrInvalidPaymentInfo) {
		t.Fatalf("telecomPaymentPhone(invalid) error = %v, want %v", err, ErrInvalidPaymentInfo)
	}
}

func TestMoheArabicPaymentPhoneRequiresExplicitPaymentInfoFormat(t *testing.T) {
	got, err := moheArabicPaymentPhone("ignored/abcdefghij0912345678")
	if err != nil {
		t.Fatalf("moheArabicPaymentPhone() error = %v", err)
	}
	if got != "0912345678" {
		t.Fatalf("phone = %q, want %q", got, "0912345678")
	}

	if _, err := moheArabicPaymentPhone("ignored"); !errors.Is(err, ErrInvalidPaymentInfo) {
		t.Fatalf("moheArabicPaymentPhone(invalid) error = %v, want %v", err, ErrInvalidPaymentInfo)
	}
}
