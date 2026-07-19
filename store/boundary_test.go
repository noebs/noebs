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

func TestOpaqueCardStoreHasNoGenericRailSecretResolver(t *testing.T) {
	data, err := os.ReadFile("cards_opaque.go")
	if err != nil {
		t.Fatalf("read cards_opaque.go: %v", err)
	}
	source := string(data)
	for _, token := range []string{
		"type CardRailMaterial",
		"ResolveOwnedCardForRail",
		"ResolveMainCardForRail",
		"func (m CardRailMaterial) PAN()",
	} {
		if strings.Contains(source, token) {
			t.Fatalf("cards_opaque.go contains %q; funded secrets require a durable operation claim", token)
		}
	}
}

func TestLegacyPANAndIPINPersistenceHelpersStayRemoved(t *testing.T) {
	data, err := os.ReadFile("token_sensitive.go")
	if err != nil {
		t.Fatalf("read token_sensitive.go: %v", err)
	}
	source := string(data)
	for _, token := range []string{
		"panLookupClause",
		"panLookupArgs",
		"encryptCardFields",
		"hydrateCardFields",
		"updateCardIPIN",
		"encryptCacheCardFields",
	} {
		if strings.Contains(source, token) {
			t.Fatalf("token_sensitive.go contains retired helper %q", token)
		}
	}
}
