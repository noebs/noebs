package main

import (
	"os"
	"testing"
)

func Test_verifyToken(t *testing.T) {
	token := os.Getenv("NOEBS_FIREBASE_TEST_TOKEN")
	if token == "" {
		t.Skip("NOEBS_FIREBASE_TEST_TOKEN not set")
	}
	fb, err := getFirebase()
	if err != nil {
		t.Skipf("firebase credentials not configured: %v", err)
	}
	got, err := verifyToken(fb, token)
	if err != nil {
		t.Fatalf("verifyToken() error = %v", err)
	}
	if got == "" {
		t.Fatalf("verifyToken() returned empty audience")
	}
	if want := os.Getenv("NOEBS_FIREBASE_TEST_AUD"); want != "" && got != want {
		t.Fatalf("verifyToken() = %v, want %v", got, want)
	}
}
