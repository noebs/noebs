package store

import (
	"os"
	"strings"
	"testing"
)

func TestStoreDoesNotExposeIdentityCardJoinHelper(t *testing.T) {
	data, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatalf("read store.go: %v", err)
	}
	if strings.Contains(string(data), "GetCardsOrFail") {
		t.Fatalf("store must not expose GetCardsOrFail; card-vault callers must use explicit user IDs")
	}
}
