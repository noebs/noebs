package main

import (
	"errors"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/oidcauth"
)

func TestInitOIDCVerifierRequiresExplicitGatewayContract(t *testing.T) {
	previous := oidcVerifier
	t.Cleanup(func() { oidcVerifier = previous })

	config := ebs_fields.NoebsConfig{OIDC: validOIDCRuntimeConfig(), KeycloakCACertificate: testKeycloakCACertificate}
	if err := initOIDCVerifier(serviceRoleAPIGateway, config); err != nil {
		t.Fatal(err)
	}
	if oidcVerifier == nil {
		t.Fatal("gateway OIDC verifier is nil")
	}

	config.OIDC.Audience = ""
	if err := initOIDCVerifier(serviceRoleAPIGateway, config); !errors.Is(err, oidcauth.ErrInvalidConfiguration) {
		t.Fatalf("missing audience error = %v", err)
	}
	if oidcVerifier != nil {
		t.Fatal("invalid gateway config retained an OIDC verifier")
	}
}

func TestInitOIDCVerifierDoesNotInitializeOtherRoles(t *testing.T) {
	previous := oidcVerifier
	t.Cleanup(func() { oidcVerifier = previous })

	if err := initOIDCVerifier(serviceRoleIdentityAuth, ebs_fields.NoebsConfig{}); err != nil {
		t.Fatal(err)
	}
	if oidcVerifier != nil {
		t.Fatal("identity-auth initialized a gateway OIDC verifier")
	}
}

func validOIDCRuntimeConfig() oidcauth.RuntimeConfig {
	return oidcauth.RuntimeConfig{
		Issuer:                           "https://api.noebs.sd/auth/realms/noebs",
		JWKSURL:                          "https://keycloak.noebs.svc.cluster.local:8443/auth/realms/noebs/protocol/openid-connect/certs",
		Audience:                         "noebs-api",
		AllowedClients:                   []string{"noebs-mobile", "noebs-backoffice"},
		AccessTokenType:                  "Bearer",
		MaxFutureIssuedAtSeconds:         5,
		JWKSRefreshSeconds:               300,
		UnknownKeyRefreshIntervalSeconds: 30,
	}
}
