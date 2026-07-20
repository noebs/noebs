package utils

import "testing"

func TestMaskPANHandlesShortValues(t *testing.T) {
	for _, pan := range []string{"", "1234", "123456789"} {
		if got := MaskPAN(pan); got != pan {
			t.Fatalf("MaskPAN(%q) = %q, want original value", pan, got)
		}
	}
}

func TestMaskPANMasksLongValues(t *testing.T) {
	if got := MaskPAN("1234567891234567"); got != "123456*****4567" {
		t.Fatalf("MaskPAN() = %q", got)
	}
}
