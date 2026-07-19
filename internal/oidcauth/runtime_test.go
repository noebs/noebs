package oidcauth

import (
	"errors"
	"net/http"
	"testing"
)

func TestNewRemoteVerifierRequiresCompleteRuntimeConfig(t *testing.T) {
	valid := RuntimeConfig{
		Issuer:                           testIssuer,
		JWKSURL:                          "https://identity.example/realms/noebs/protocol/openid-connect/certs",
		Audience:                         testAudience,
		AllowedClients:                   []string{testClient},
		AccessTokenType:                  "Bearer",
		MaxFutureIssuedAtSeconds:         30,
		JWKSRefreshSeconds:               3600,
		UnknownKeyRefreshIntervalSeconds: 30,
	}
	tests := []struct {
		name   string
		mutate func(*RuntimeConfig)
	}{
		{name: "issuer", mutate: func(c *RuntimeConfig) { c.Issuer = "" }},
		{name: "JWKS URL", mutate: func(c *RuntimeConfig) { c.JWKSURL = "" }},
		{name: "audience", mutate: func(c *RuntimeConfig) { c.Audience = "" }},
		{name: "allowed clients", mutate: func(c *RuntimeConfig) { c.AllowedClients = nil }},
		{name: "access token type", mutate: func(c *RuntimeConfig) { c.AccessTokenType = "" }},
		{name: "future issued at", mutate: func(c *RuntimeConfig) { c.MaxFutureIssuedAtSeconds = -1 }},
		{name: "JWKS refresh", mutate: func(c *RuntimeConfig) { c.JWKSRefreshSeconds = 0 }},
		{name: "unknown key refresh", mutate: func(c *RuntimeConfig) { c.UnknownKeyRefreshIntervalSeconds = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			_, err := NewRemoteVerifier(config, http.DefaultClient, &fakeClock{now: oidcTestNow})
			if !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("error = %v, want invalid configuration", err)
			}
		})
	}
}
