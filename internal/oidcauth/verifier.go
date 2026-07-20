package oidcauth

import (
	"context"
	"crypto/rsa"
	"fmt"
	"strings"
	"time"

	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/golang-jwt/jwt/v5"
)

const joseTokenType = "JWT"

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type KeySet interface {
	Key(context.Context, string) (*rsa.PublicKey, error)
}

type Config struct {
	Issuer            string
	Audience          string
	AllowedClients    []string
	AccessTokenType   string
	MaxFutureIssuedAt time.Duration
	Clock             Clock
	Keys              KeySet
}

type Verifier struct {
	issuer            string
	audience          string
	allowedClients    map[string]struct{}
	accessTokenType   string
	maxFutureIssuedAt time.Duration
	clock             Clock
	keys              KeySet
}

func NewVerifier(config Config) (*Verifier, error) {
	if config.Issuer == "" || config.Audience == "" || config.AccessTokenType == "" ||
		config.MaxFutureIssuedAt < 0 || config.Clock == nil || config.Keys == nil || len(config.AllowedClients) == 0 {
		return nil, ErrInvalidConfiguration
	}
	allowedClients := make(map[string]struct{}, len(config.AllowedClients))
	for _, client := range config.AllowedClients {
		if client == "" {
			return nil, ErrInvalidConfiguration
		}
		if _, duplicate := allowedClients[client]; duplicate {
			return nil, ErrInvalidConfiguration
		}
		allowedClients[client] = struct{}{}
	}
	return &Verifier{
		issuer:            config.Issuer,
		audience:          config.Audience,
		allowedClients:    allowedClients,
		accessTokenType:   config.AccessTokenType,
		maxFutureIssuedAt: config.MaxFutureIssuedAt,
		clock:             config.Clock,
		keys:              config.Keys,
	}, nil
}

// VerifyBearer verifies one exact "Bearer <token>" Authorization value and
// returns immutable claims. Raw tokens and alternate authentication schemes
// are rejected at this boundary.
func (v *Verifier) VerifyBearer(ctx context.Context, authorization string) (tenantauth.Claims, error) {
	raw, err := parseBearer(authorization)
	if err != nil {
		return tenantauth.Claims{}, err
	}
	return v.verify(ctx, raw)
}

// VerifyAccessToken verifies an access token already extracted from trusted
// server-side storage. It never accepts an authorization scheme or whitespace.
func (v *Verifier) VerifyAccessToken(ctx context.Context, raw string) (tenantauth.Claims, error) {
	if raw == "" || strings.IndexAny(raw, " \t\r\n") >= 0 {
		return tenantauth.Claims{}, ErrInvalidToken
	}
	return v.verify(ctx, raw)
}

func parseBearer(authorization string) (string, error) {
	if authorization == "" {
		return "", ErrMissingAuthorization
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) {
		return "", ErrInvalidAuthorization
	}
	raw := strings.TrimPrefix(authorization, prefix)
	if raw == "" || strings.IndexAny(raw, " \t\r\n") >= 0 {
		return "", ErrInvalidAuthorization
	}
	return raw, nil
}

type wireClaims struct {
	jwt.RegisteredClaims
	AuthorizedParty string                      `json:"azp"`
	TokenType       string                      `json:"typ"`
	Organization    map[string]wireOrganization `json:"organization"`
}

type wireOrganization struct {
	ID             string                    `json:"id"`
	ResourceAccess map[string]wireRoleAccess `json:"resource_access"`
}

type wireRoleAccess struct {
	Roles []string `json:"roles"`
}

func (v *Verifier) verify(ctx context.Context, raw string) (tenantauth.Claims, error) {
	claims := &wireClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Header["typ"] != joseTokenType {
			return nil, ErrInvalidToken
		}
		keyID, ok := token.Header["kid"].(string)
		if !ok || keyID == "" {
			return nil, ErrUnknownKey
		}
		return v.keys.Key(ctx, keyID)
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(v.clock.Now),
		jwt.WithStrictDecoding(),
	)
	if err != nil {
		return tenantauth.Claims{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	if token == nil || !token.Valid {
		return tenantauth.Claims{}, ErrInvalidToken
	}
	if err := v.validateClaims(claims); err != nil {
		return tenantauth.Claims{}, err
	}
	return v.domainClaims(claims)
}

func (v *Verifier) validateClaims(claims *wireClaims) error {
	if claims.Subject == "" || claims.ExpiresAt == nil || claims.IssuedAt == nil {
		return ErrInvalidToken
	}
	if len(claims.Audience) != 1 || claims.Audience[0] != v.audience {
		return ErrInvalidToken
	}
	if _, allowed := v.allowedClients[claims.AuthorizedParty]; !allowed {
		return ErrInvalidToken
	}
	if claims.TokenType != v.accessTokenType {
		return ErrInvalidToken
	}
	if claims.IssuedAt.Time.After(v.clock.Now().Add(v.maxFutureIssuedAt)) {
		return ErrInvalidToken
	}
	if !claims.ExpiresAt.Time.After(claims.IssuedAt.Time) {
		return ErrInvalidToken
	}
	return nil
}

func (v *Verifier) domainClaims(claims *wireClaims) (tenantauth.Claims, error) {
	organizations := make(map[string]tenantauth.Organization, len(claims.Organization))
	for tenant, wireOrganization := range claims.Organization {
		organization, err := tenantauth.NewOrganizationClaim(
			wireOrganization.ID,
			wireOrganization.ResourceAccess[v.audience].Roles,
		)
		if err != nil || tenant == "" {
			return tenantauth.Claims{}, ErrInvalidToken
		}
		organizations[tenant] = organization
	}
	verified, err := tenantauth.NewClaims(tenantauth.Identity{
		Issuer:          claims.Issuer,
		Subject:         claims.Subject,
		AuthorizedParty: claims.AuthorizedParty,
		IssuedAt:        claims.IssuedAt.Time,
		ExpiresAt:       claims.ExpiresAt.Time,
	}, organizations)
	if err != nil {
		return tenantauth.Claims{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	return verified, nil
}
