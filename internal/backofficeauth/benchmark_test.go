package backofficeauth

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/coreos/go-oidc/v3/oidc"
)

func BenchmarkKeyringSealOpen(b *testing.B) {
	key := make([]byte, aes256KeyBytes)
	if _, err := rand.Read(key); err != nil {
		b.Fatal(err)
	}
	ring, err := NewKeyring(KeyringConfig{ActiveKeyID: "active", Keys: map[string][]byte{"active": key}, Entropy: rand.Reader})
	if err != nil {
		b.Fatal(err)
	}
	record := digestString("session")
	plaintext := make([]byte, 4096)
	if _, err := rand.Read(plaintext); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		envelope, err := ring.Seal(sessionTokenPurpose, record, plaintext)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := ring.Open(sessionTokenPurpose, record, envelope); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCSRFValidation(b *testing.B) {
	protector, err := NewCSRFProtector("https://dsa.adonese.sd")
	if err != nil {
		b.Fatal(err)
	}
	_, token, err := GenerateCSRFSecret(rand.Reader)
	if err != nil {
		b.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, "https://dsa.adonese.sd/backoffice/t/acme/wallet", nil)
	request.Header.Set("Origin", "https://dsa.adonese.sd")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if err := protector.ValidateMutation(request, token, token); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAuthenticateMemoryStore(b *testing.B) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	clock := &testClock{now: now}
	claims := claimsForTest(b, now.Add(-time.Minute), now.Add(10*time.Minute))
	oauthClient := oauthClientForTest(b, clock, "http://127.0.0.1:1/token",
		idTokenVerifierFunc(func(context.Context, string) (*oidc.IDToken, error) {
			return nil, errors.New("unused")
		}),
		accessTokenVerifierFunc(func(_ context.Context, raw string) (tenantauth.Claims, error) {
			if raw != "access-token" {
				return tenantauth.Claims{}, errors.New("unexpected token")
			}
			return claims, nil
		}),
	)
	repository := newMemoryRepository()
	service := serviceForTest(b, clock, repository, oauthClient)
	rawSessionID := opaqueForTest(9)
	sessionHash, err := digestOpaque(rawSessionID)
	if err != nil {
		b.Fatal(err)
	}
	csrfSecret := make([]byte, opaqueTokenBytes)
	if _, err := rand.Read(csrfSecret); err != nil {
		b.Fatal(err)
	}
	sealed, err := service.sealTokenMaterial(sessionHash, tokenMaterial{
		Version: tokenMaterialVersion, AccessToken: "access-token", RefreshToken: "refresh-token", IDToken: "id-token", CSRFSecret: csrfSecret,
	})
	if err != nil {
		b.Fatal(err)
	}
	if err := repository.CreateSession(context.Background(), SessionRecord{
		SessionHash: sessionHash, Issuer: claims.Identity().Issuer, Subject: claims.Identity().Subject, Tokens: sealed,
		AccessExpiresAt: claims.Identity().ExpiresAt, RefreshExpiresAt: now.Add(30 * time.Minute), IdleExpiresAt: now.Add(30 * time.Minute),
		AbsoluteExpiresAt: now.Add(8 * time.Hour), LastSeenAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, err := service.Authenticate(context.Background(), rawSessionID); err != nil {
			b.Fatal(err)
		}
	}
}
