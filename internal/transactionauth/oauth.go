package transactionauth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type IDTokenVerifier interface {
	Verify(context.Context, string) (*oidc.IDToken, error)
}

func NewIDTokenVerifier(issuer, clientID string, keys oidc.KeySet, clock Clock) (IDTokenVerifier, error) {
	if !validHTTPSURL(issuer) || clientID == "" || keys == nil || clock == nil {
		return nil, ErrInvalidConfiguration
	}
	return oidc.NewVerifier(issuer, keys, &oidc.Config{
		ClientID:             clientID,
		SupportedSigningAlgs: []string{oidc.RS256},
		Now:                  clock.Now,
	}), nil
}

type OAuthClientConfig struct {
	Issuer               string
	ClientID             string
	ClientSecret         string
	AuthorizationURL     string
	TokenURL             string
	RedirectURL          string
	HTTPClient           *http.Client
	IDTokens             IDTokenVerifier
	Clock                Clock
	RequiredACR          string
	MaxAuthenticationAge time.Duration
	MaxFutureIssuedAt    time.Duration
}

type OAuthClient struct {
	issuer               string
	clientID             string
	config               oauth2.Config
	httpClient           *http.Client
	idTokens             IDTokenVerifier
	clock                Clock
	requiredACR          string
	maxAuthenticationAge time.Duration
	maxFutureIssuedAt    time.Duration
}

func NewOAuthClient(config OAuthClientConfig) (*OAuthClient, error) {
	if !validHTTPSURL(config.Issuer) || config.ClientID == "" || config.ClientSecret == "" ||
		!validHTTPSURL(config.AuthorizationURL) || !validHTTPSURL(config.TokenURL) ||
		!validHTTPSURL(config.RedirectURL) || config.HTTPClient == nil || config.HTTPClient.Timeout <= 0 ||
		config.IDTokens == nil || config.Clock == nil || config.RequiredACR == "" ||
		!wholeSecond(config.MaxAuthenticationAge) || !wholeSecond(config.MaxFutureIssuedAt) {
		return nil, ErrInvalidConfiguration
	}
	return &OAuthClient{
		issuer:   config.Issuer,
		clientID: config.ClientID,
		config: oauth2.Config{
			ClientID:     config.ClientID,
			ClientSecret: config.ClientSecret,
			RedirectURL:  config.RedirectURL,
			Scopes:       []string{oidc.ScopeOpenID},
			Endpoint: oauth2.Endpoint{
				AuthURL:   config.AuthorizationURL,
				TokenURL:  config.TokenURL,
				AuthStyle: oauth2.AuthStyleInHeader,
			},
		},
		httpClient:           config.HTTPClient,
		idTokens:             config.IDTokens,
		clock:                config.Clock,
		requiredACR:          config.RequiredACR,
		maxAuthenticationAge: config.MaxAuthenticationAge,
		maxFutureIssuedAt:    config.MaxFutureIssuedAt,
	}, nil
}

func (c *OAuthClient) AuthorizationURL(state, nonce, verifier string) (string, error) {
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
		oauth2.SetAuthURLParam("acr_values", c.requiredACR),
		oauth2.SetAuthURLParam("max_age", "0"),
	), nil
}

func (c *OAuthClient) Exchange(
	ctx context.Context,
	code string,
	verifier string,
	expectedNonce Digest,
) (VerifiedIdentity, error) {
	if c == nil || ctx == nil || code == "" {
		return VerifiedIdentity{}, ErrInvalidInput
	}
	if _, err := digestOpaque(verifier); err != nil {
		return VerifiedIdentity{}, ErrInvalidInput
	}
	token, err := c.config.Exchange(c.clientContext(ctx), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return VerifiedIdentity{}, fmt.Errorf("%w: %w", ErrOAuthExchange, err)
	}
	if token == nil || token.AccessToken == "" || !strings.EqualFold(token.TokenType, "Bearer") {
		return VerifiedIdentity{}, ErrOAuthExchange
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return VerifiedIdentity{}, ErrInvalidIDToken
	}
	idToken, err := c.idTokens.Verify(c.clientContext(ctx), rawIDToken)
	if err != nil {
		return VerifiedIdentity{}, fmt.Errorf("%w: %w", ErrInvalidIDToken, err)
	}
	return c.validateIDToken(idToken, token.AccessToken, expectedNonce)
}

func (c *OAuthClient) validateIDToken(
	token *oidc.IDToken,
	accessToken string,
	expectedNonce Digest,
) (VerifiedIdentity, error) {
	now := c.clock.Now().UTC()
	if token == nil || token.Issuer != c.issuer || len(token.Audience) != 1 || token.Audience[0] != c.clientID ||
		token.Subject == "" || len(token.Subject) > 512 || !token.Expiry.After(now) || token.IssuedAt.IsZero() ||
		token.IssuedAt.After(now.Add(c.maxFutureIssuedAt)) || !token.Expiry.After(token.IssuedAt) {
		return VerifiedIdentity{}, ErrInvalidIDToken
	}
	actualNonce := digestString(token.Nonce)
	if token.Nonce == "" || subtle.ConstantTimeCompare(actualNonce[:], expectedNonce[:]) != 1 {
		return VerifiedIdentity{}, ErrInvalidIDToken
	}
	if token.AccessTokenHash != "" {
		if err := token.VerifyAccessToken(accessToken); err != nil {
			return VerifiedIdentity{}, fmt.Errorf("%w: %w", ErrInvalidIDToken, err)
		}
	}
	var claims struct {
		AuthorizedParty    string `json:"azp"`
		ACR                string `json:"acr"`
		AuthenticationTime int64  `json:"auth_time"`
	}
	if err := token.Claims(&claims); err != nil {
		return VerifiedIdentity{}, fmt.Errorf("%w: %w", ErrInvalidIDToken, err)
	}
	authenticationTime := time.Unix(claims.AuthenticationTime, 0).UTC()
	if claims.AuthorizedParty != c.clientID || claims.ACR != c.requiredACR || claims.AuthenticationTime <= 0 ||
		authenticationTime.After(now.Add(c.maxFutureIssuedAt)) || now.Sub(authenticationTime) > c.maxAuthenticationAge ||
		authenticationTime.After(token.IssuedAt.Add(c.maxFutureIssuedAt)) {
		return VerifiedIdentity{}, ErrInvalidIDToken
	}
	return VerifiedIdentity{
		Issuer:             token.Issuer,
		Subject:            token.Subject,
		ACR:                claims.ACR,
		AuthenticationTime: authenticationTime,
	}, nil
}

func (c *OAuthClient) clientContext(ctx context.Context) context.Context {
	return oidc.ClientContext(ctx, c.httpClient)
}

func validHTTPSURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" ||
		parsed.RawQuery != "" || parsed.String() != raw {
		return false
	}
	return parsed.Scheme == "https"
}
