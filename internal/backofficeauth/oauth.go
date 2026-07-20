package backofficeauth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type IDTokenVerifier interface {
	Verify(context.Context, string) (*oidc.IDToken, error)
}

type AccessTokenVerifier interface {
	VerifyAccessToken(context.Context, string) (tenantauth.Claims, error)
}

func NewIDTokenVerifier(issuer, clientID string, keys oidc.KeySet, clock Clock) (IDTokenVerifier, error) {
	if !validAbsoluteURL(issuer, true) || clientID == "" || keys == nil || clock == nil {
		return nil, ErrInvalidConfiguration
	}
	return oidc.NewVerifier(issuer, keys, &oidc.Config{
		ClientID:             clientID,
		SupportedSigningAlgs: []string{oidc.RS256},
		Now:                  clock.Now,
	}), nil
}

type OAuthClientConfig struct {
	Issuer            string
	ClientID          string
	ClientSecret      string
	AuthorizationURL  string
	TokenURL          string
	RedirectURL       string
	EndSessionURL     string
	PostLogoutURL     string
	Scopes            []string
	HTTPClient        *http.Client
	IDTokens          IDTokenVerifier
	AccessTokens      AccessTokenVerifier
	Clock             Clock
	MaxFutureIssuedAt time.Duration
}

type OAuthClient struct {
	issuer            string
	clientID          string
	config            oauth2.Config
	httpClient        *http.Client
	idTokens          IDTokenVerifier
	accessTokens      AccessTokenVerifier
	clock             Clock
	maxFutureIssuedAt time.Duration
	endSessionURL     string
	postLogoutURL     string
}

func NewOAuthClient(config OAuthClientConfig) (*OAuthClient, error) {
	if !validAbsoluteURL(config.Issuer, true) || config.ClientID == "" || config.ClientSecret == "" ||
		!validAbsoluteURL(config.AuthorizationURL, true) || !validAbsoluteURL(config.TokenURL, false) ||
		!validAbsoluteURL(config.RedirectURL, true) || !validAbsoluteURL(config.EndSessionURL, true) ||
		!validAbsoluteURL(config.PostLogoutURL, true) || config.HTTPClient == nil || config.HTTPClient.Timeout <= 0 ||
		config.IDTokens == nil || config.AccessTokens == nil || config.Clock == nil || config.MaxFutureIssuedAt <= 0 ||
		!validScopes(config.Scopes) {
		return nil, ErrInvalidConfiguration
	}
	return &OAuthClient{
		issuer:   config.Issuer,
		clientID: config.ClientID,
		config: oauth2.Config{
			ClientID:     config.ClientID,
			ClientSecret: config.ClientSecret,
			RedirectURL:  config.RedirectURL,
			Scopes:       slices.Clone(config.Scopes),
			Endpoint: oauth2.Endpoint{
				AuthURL:   config.AuthorizationURL,
				TokenURL:  config.TokenURL,
				AuthStyle: oauth2.AuthStyleInHeader,
			},
		},
		httpClient:        config.HTTPClient,
		endSessionURL:     config.EndSessionURL,
		postLogoutURL:     config.PostLogoutURL,
		idTokens:          config.IDTokens,
		accessTokens:      config.AccessTokens,
		clock:             config.Clock,
		maxFutureIssuedAt: config.MaxFutureIssuedAt,
	}, nil
}

func (c *OAuthClient) logoutURL(idToken string) (string, error) {
	if c == nil || idToken == "" {
		return "", ErrInvalidInput
	}
	endpoint, err := url.Parse(c.endSessionURL)
	if err != nil {
		return "", ErrInvalidConfiguration
	}
	query := endpoint.Query()
	query.Set("client_id", c.clientID)
	query.Set("id_token_hint", idToken)
	query.Set("post_logout_redirect_uri", c.postLogoutURL)
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func validAbsoluteURL(raw string, requireHTTPS bool) bool {
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" ||
		parsed.RawQuery != "" || parsed.String() != raw {
		return false
	}
	if requireHTTPS {
		return parsed.Scheme == "https"
	}
	return parsed.Scheme == "https" || parsed.Scheme == "http"
}

func validScopes(scopes []string) bool {
	if len(scopes) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if scope == "" || strings.ContainsAny(scope, " \t\r\n") {
			return false
		}
		if scope == "organization" {
			return false
		}
		if _, duplicate := seen[scope]; duplicate {
			return false
		}
		seen[scope] = struct{}{}
	}
	_, openid := seen[oidc.ScopeOpenID]
	_, organization := seen["organization:*"]
	return openid && organization
}

func (c *OAuthClient) authorizationURL(state, nonce, verifier string) (string, error) {
	if c == nil {
		return "", ErrInvalidConfiguration
	}
	if _, err := digestOpaque(state); err != nil {
		return "", ErrInvalidInput
	}
	if _, err := digestOpaque(nonce); err != nil {
		return "", ErrInvalidInput
	}
	if _, err := digestOpaque(verifier); err != nil {
		return "", ErrInvalidInput
	}
	return c.config.AuthCodeURL(
		state,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("nonce", nonce),
		oauth2.SetAuthURLParam("response_mode", "query"),
	), nil
}

type oauthResult struct {
	accessToken      string
	refreshToken     string
	idToken          string
	accessExpiresAt  time.Time
	refreshExpiresAt time.Time
	claims           tenantauth.Claims
}

func (c *OAuthClient) exchange(ctx context.Context, code, verifier string, expectedNonce Digest) (oauthResult, error) {
	if c == nil || ctx == nil || code == "" {
		return oauthResult{}, ErrInvalidInput
	}
	if _, err := digestOpaque(verifier); err != nil {
		return oauthResult{}, ErrInvalidInput
	}
	token, err := c.config.Exchange(c.clientContext(ctx), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return oauthResult{}, fmt.Errorf("%w: %w", ErrOAuthExchange, err)
	}
	return c.verifyLoginTokens(ctx, token, expectedNonce)
}

func (c *OAuthClient) verifyLoginTokens(ctx context.Context, token *oauth2.Token, expectedNonce Digest) (oauthResult, error) {
	if token == nil || token.AccessToken == "" || token.RefreshToken == "" || !strings.EqualFold(token.TokenType, "Bearer") {
		return oauthResult{}, ErrOAuthExchange
	}
	returnedRefreshToken, ok := token.Extra("refresh_token").(string)
	if !ok || returnedRefreshToken == "" || returnedRefreshToken != token.RefreshToken {
		return oauthResult{}, ErrOAuthExchange
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return oauthResult{}, ErrInvalidIDToken
	}
	idToken, err := c.idTokens.Verify(c.clientContext(ctx), rawIDToken)
	if err != nil {
		return oauthResult{}, fmt.Errorf("%w: %w", ErrInvalidIDToken, err)
	}
	if err := c.validateIDToken(idToken, token.AccessToken, &expectedNonce, ""); err != nil {
		return oauthResult{}, err
	}
	claims, err := c.verifyAccess(ctx, token.AccessToken, idToken.Subject)
	if err != nil {
		return oauthResult{}, err
	}
	refreshExpiresAt, err := c.refreshExpiry(token)
	if err != nil {
		return oauthResult{}, err
	}
	return oauthResult{
		accessToken:      token.AccessToken,
		refreshToken:     token.RefreshToken,
		idToken:          rawIDToken,
		accessExpiresAt:  claims.Identity().ExpiresAt.UTC(),
		refreshExpiresAt: refreshExpiresAt,
		claims:           claims,
	}, nil
}

func (c *OAuthClient) refresh(ctx context.Context, accessToken, refreshToken, rawIDToken, subject string) (oauthResult, error) {
	if c == nil || ctx == nil || accessToken == "" || refreshToken == "" || rawIDToken == "" || subject == "" {
		return oauthResult{}, ErrInvalidInput
	}
	previous := &oauth2.Token{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		RefreshToken: refreshToken,
		Expiry:       time.Unix(1, 0),
	}
	token, err := c.config.TokenSource(c.clientContext(ctx), previous).Token()
	if err != nil {
		var retrieveError *oauth2.RetrieveError
		if errors.As(err, &retrieveError) && retrieveError.ErrorCode == "invalid_grant" {
			return oauthResult{}, fmt.Errorf("%w: %w", ErrSessionRevoked, err)
		}
		return oauthResult{}, fmt.Errorf("%w: %w", ErrOAuthExchange, err)
	}
	if token == nil || token.AccessToken == "" || token.RefreshToken == "" || !strings.EqualFold(token.TokenType, "Bearer") {
		return oauthResult{}, ErrOAuthExchange
	}
	returnedRefreshToken, ok := token.Extra("refresh_token").(string)
	if !ok || returnedRefreshToken == "" || returnedRefreshToken != token.RefreshToken || returnedRefreshToken == refreshToken {
		return oauthResult{}, ErrOAuthExchange
	}
	claims, err := c.verifyAccess(ctx, token.AccessToken, subject)
	if err != nil {
		return oauthResult{}, err
	}
	refreshExpiresAt, err := c.refreshExpiry(token)
	if err != nil {
		return oauthResult{}, err
	}
	newRawIDToken := rawIDToken
	if candidate, ok := token.Extra("id_token").(string); ok && candidate != "" {
		verified, err := c.idTokens.Verify(c.clientContext(ctx), candidate)
		if err != nil {
			return oauthResult{}, fmt.Errorf("%w: %w", ErrInvalidIDToken, err)
		}
		if err := c.validateIDToken(verified, token.AccessToken, nil, subject); err != nil {
			return oauthResult{}, err
		}
		newRawIDToken = candidate
	}
	return oauthResult{
		accessToken:      token.AccessToken,
		refreshToken:     token.RefreshToken,
		idToken:          newRawIDToken,
		accessExpiresAt:  claims.Identity().ExpiresAt.UTC(),
		refreshExpiresAt: refreshExpiresAt,
		claims:           claims,
	}, nil
}

func (c *OAuthClient) verifyAccessToken(ctx context.Context, raw, expectedSubject string) (tenantauth.Claims, error) {
	if c == nil || ctx == nil || raw == "" || expectedSubject == "" {
		return tenantauth.Claims{}, ErrInvalidInput
	}
	return c.verifyAccess(ctx, raw, expectedSubject)
}

func (c *OAuthClient) verifyAccess(ctx context.Context, raw, expectedSubject string) (tenantauth.Claims, error) {
	claims, err := c.accessTokens.VerifyAccessToken(ctx, raw)
	if err != nil {
		return tenantauth.Claims{}, fmt.Errorf("%w: %w", ErrInvalidAccessToken, err)
	}
	identity := claims.Identity()
	if identity.Issuer != c.issuer || identity.Subject != expectedSubject || identity.AuthorizedParty != c.clientID ||
		!identity.ExpiresAt.After(c.clock.Now()) {
		return tenantauth.Claims{}, ErrInvalidAccessToken
	}
	return claims, nil
}

func (c *OAuthClient) validateIDToken(token *oidc.IDToken, accessToken string, expectedNonce *Digest, expectedSubject string) error {
	now := c.clock.Now()
	if token == nil || token.Issuer != c.issuer || len(token.Audience) != 1 || token.Audience[0] != c.clientID ||
		token.Subject == "" || !token.Expiry.After(now) || token.IssuedAt.IsZero() ||
		token.IssuedAt.After(now.Add(c.maxFutureIssuedAt)) || !token.Expiry.After(token.IssuedAt) ||
		(expectedSubject != "" && token.Subject != expectedSubject) {
		return ErrInvalidIDToken
	}
	if expectedNonce != nil {
		actual := digestString(token.Nonce)
		if token.Nonce == "" || subtle.ConstantTimeCompare(actual[:], expectedNonce[:]) != 1 {
			return ErrInvalidIDToken
		}
	}
	if token.AccessTokenHash != "" {
		if err := token.VerifyAccessToken(accessToken); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidIDToken, err)
		}
	}
	return nil
}

func (c *OAuthClient) refreshExpiry(token *oauth2.Token) (time.Time, error) {
	maxSeconds := float64(int64(^uint64(0)>>1) / int64(time.Second))
	var seconds int64
	switch value := token.Extra("refresh_expires_in").(type) {
	case float64:
		if value <= 0 || value > maxSeconds || math.Trunc(value) != value {
			return time.Time{}, ErrOAuthExchange
		}
		seconds = int64(value)
	case int64:
		if value <= 0 || float64(value) > maxSeconds {
			return time.Time{}, ErrOAuthExchange
		}
		seconds = value
	default:
		return time.Time{}, ErrOAuthExchange
	}
	return c.clock.Now().Add(time.Duration(seconds) * time.Second).UTC(), nil
}

func (c *OAuthClient) clientContext(ctx context.Context) context.Context {
	return oidc.ClientContext(ctx, c.httpClient)
}
