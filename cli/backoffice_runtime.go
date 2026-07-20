package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/backofficeauth"
	"github.com/adonese/noebs/internal/httpclient"
	"github.com/adonese/noebs/internal/oidcauth"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/adonese/noebs/store"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	backofficeClientID      = "noebs-backoffice"
	backofficeFlowCookie    = "__Host-noebs_backoffice_flow"
	backofficeSessionCookie = "__Host-noebs_backoffice_session"
	backofficeFlowTTL       = 10 * time.Minute
	backofficeIdleTTL       = 30 * time.Minute
	backofficeAbsoluteTTL   = 8 * time.Hour
	backofficeRefreshSkew   = time.Minute
	backofficeTouchInterval = time.Minute
	backofficeOAuthTimeout  = 10 * time.Second
	backofficeReturnPrefix  = "/backoffice/"
)

var backofficeAuthHandler *backofficeHTTP

type backofficeRuntimeDependencies struct {
	clock       backofficeauth.SystemClock
	oauth       *backofficeauth.OAuthClient
	keys        *backofficeauth.Keyring
	cookies     *backofficeauth.CookiePolicy
	csrf        *backofficeauth.CSRFProtector
	requestHost string
}

func initBackofficeAuth(role serviceRole, cfg ebs_fields.NoebsConfig, db *store.DB, catalog tenantcatalog.Catalog) error {
	backofficeAuthHandler = nil
	if role != serviceRoleAPIGateway {
		return nil
	}
	if db == nil || db.DB == nil {
		return errors.New("api-gateway back-office session database is not initialized")
	}
	runtime, err := buildBackofficeRuntimeDependencies(cfg)
	if err != nil {
		return err
	}
	repository, err := backofficeauth.NewPostgresStore(db.DB.DB)
	if err != nil {
		return err
	}
	service, err := backofficeauth.NewService(backofficeauth.ServiceConfig{
		Flows:            repository,
		Sessions:         repository,
		OAuth:            runtime.oauth,
		Keys:             runtime.keys,
		Cookies:          runtime.cookies,
		Clock:            runtime.clock,
		Entropy:          rand.Reader,
		FlowTTL:          backofficeFlowTTL,
		IdleTTL:          backofficeIdleTTL,
		AbsoluteTTL:      backofficeAbsoluteTTL,
		RefreshSkew:      backofficeRefreshSkew,
		TouchInterval:    backofficeTouchInterval,
		ReturnPathPrefix: backofficeReturnPrefix,
	})
	if err != nil {
		return err
	}
	backofficeAuthHandler = &backofficeHTTP{
		service: service,
		cookies: runtime.cookies,
		csrf:    runtime.csrf,
		host:    runtime.requestHost,
		issuer:  cfg.OIDC.Issuer,
		catalog: catalog,
	}
	return nil
}

func validateBackofficeRuntimeConfig(cfg ebs_fields.NoebsConfig) error {
	_, err := buildBackofficeRuntimeDependencies(cfg)
	return err
}

func buildBackofficeRuntimeDependencies(cfg ebs_fields.NoebsConfig) (backofficeRuntimeDependencies, error) {
	clock := backofficeauth.SystemClock{}
	if err := requireHTTPSKeycloakEndpoint(cfg.OIDC.JWKSURL); err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	tlsConfig, err := keycloakClientTLSConfig(cfg.KeycloakCACertificate)
	if err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	client := httpclient.New(
		httpclient.WithTimeout(backofficeOAuthTimeout),
		httpclient.WithResponseHeaderTimeout(5*time.Second),
		httpclient.WithTLSConfig(tlsConfig),
	)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	keyContext := context.WithValue(context.Background(), oauth2.HTTPClient, client)
	idTokens, err := backofficeauth.NewIDTokenVerifier(
		cfg.OIDC.Issuer,
		backofficeClientID,
		oidc.NewRemoteKeySet(keyContext, cfg.OIDC.JWKSURL),
		clock,
	)
	if err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	accessConfig := cfg.OIDC
	accessConfig.AllowedClients = []string{backofficeClientID}
	accessTokens, err := oidcauth.NewRemoteVerifier(accessConfig, client, oidcauth.SystemClock{})
	if err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	tokenURL, err := replaceOIDCEndpoint(cfg.OIDC.JWKSURL, "certs", "token")
	if err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	authorizationURL, err := appendOIDCEndpoint(cfg.OIDC.Issuer, "auth")
	if err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	endSessionURL, err := appendOIDCEndpoint(cfg.OIDC.Issuer, "logout")
	if err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	oauthClient, err := backofficeauth.NewOAuthClient(backofficeauth.OAuthClientConfig{
		Issuer:            cfg.OIDC.Issuer,
		ClientID:          backofficeClientID,
		ClientSecret:      cfg.BackofficeClientSecret,
		AuthorizationURL:  authorizationURL,
		TokenURL:          tokenURL,
		RedirectURL:       cfg.BackofficeRedirectURL,
		EndSessionURL:     endSessionURL,
		PostLogoutURL:     cfg.BackofficePostLogoutURL,
		Scopes:            []string{oidc.ScopeOpenID, "organization:*"},
		HTTPClient:        client,
		IDTokens:          idTokens,
		AccessTokens:      accessTokens,
		Clock:             clock,
		MaxFutureIssuedAt: time.Duration(cfg.OIDC.MaxFutureIssuedAtSeconds) * time.Second,
	})
	if err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	keys, err := decodeGatewayAuthEncryptionKeys(cfg.GatewayAuthEncryptionKeys)
	if err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	keyring, err := backofficeauth.NewKeyring(backofficeauth.KeyringConfig{
		ActiveKeyID: cfg.GatewayAuthEncryptionKeyID,
		Keys:        keys,
		Entropy:     rand.Reader,
	})
	if err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	cookies, err := backofficeauth.NewCookiePolicy(backofficeauth.CookiePolicyConfig{
		FlowName:    backofficeFlowCookie,
		SessionName: backofficeSessionCookie,
	})
	if err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	csrfOrigin, err := originOf(cfg.BackofficeRedirectURL)
	if err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	logoutOrigin, err := originOf(cfg.BackofficePostLogoutURL)
	if err != nil || logoutOrigin != csrfOrigin {
		return backofficeRuntimeDependencies{}, backofficeauth.ErrInvalidConfiguration
	}
	if err := requireExactHTTPSCallbackPath(cfg.BackofficeRedirectURL, backofficeCallbackPath); err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	if err := requireExactHTTPSCallbackPath(cfg.BackofficePostLogoutURL, backofficeLoggedOutPath); err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	csrf, err := backofficeauth.NewCSRFProtector(csrfOrigin)
	if err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	origin, _ := url.Parse(csrfOrigin)
	return backofficeRuntimeDependencies{
		clock:       clock,
		oauth:       oauthClient,
		keys:        keyring,
		cookies:     cookies,
		csrf:        csrf,
		requestHost: origin.Host,
	}, nil
}

func appendOIDCEndpoint(issuer, endpoint string) (string, error) {
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("invalid OIDC issuer")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/protocol/openid-connect/" + endpoint
	return parsed.String(), nil
}

func replaceOIDCEndpoint(jwksURL, current, replacement string) (string, error) {
	parsed, err := url.Parse(jwksURL)
	suffix := "/protocol/openid-connect/" + current
	if err != nil || parsed.Host == "" || !strings.HasSuffix(parsed.Path, suffix) || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("invalid OIDC JWKS URL")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, current) + replacement
	return parsed.String(), nil
}

func decodeGatewayAuthEncryptionKeys(encoded map[string]string) (map[string][]byte, error) {
	if len(encoded) == 0 {
		return nil, backofficeauth.ErrInvalidConfiguration
	}
	keys := make(map[string][]byte, len(encoded))
	for keyID, value := range encoded {
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil || base64.StdEncoding.EncodeToString(decoded) != value || len(decoded) != 32 {
			return nil, fmt.Errorf("invalid gateway-auth encryption key %q", keyID)
		}
		keys[keyID] = decoded
	}
	return keys, nil
}

func originOf(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", backofficeauth.ErrInvalidConfiguration
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func requireExactHTTPSCallbackPath(raw, expectedPath string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.Path != expectedPath || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return backofficeauth.ErrInvalidConfiguration
	}
	return nil
}
