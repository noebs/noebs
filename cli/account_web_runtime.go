package main

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/url"
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

const accountWebClientID = "noebs-web"

var accountWebAuthHandler *accountWebHTTP

func initAccountWebAuth(role serviceRole, cfg ebs_fields.NoebsConfig, db *store.DB, catalog tenantcatalog.Catalog) error {
	accountWebAuthHandler = nil
	if role != serviceRoleAPIGateway {
		return nil
	}
	if db == nil || db.DB == nil {
		return errors.New("api-gateway account session database is not initialized")
	}
	tenant, err := catalog.Require(cfg.WebTenantID)
	if err != nil {
		return err
	}
	runtime, err := buildAccountWebRuntimeDependencies(cfg)
	if err != nil {
		return err
	}
	repository, err := backofficeauth.NewPostgresStore(db.DB.DB)
	if err != nil {
		return err
	}
	service, err := backofficeauth.NewService(backofficeauth.ServiceConfig{
		Flows: repository, Sessions: repository, OAuth: runtime.oauth, Keys: runtime.keys,
		Cookies: runtime.cookies, Clock: runtime.clock, Entropy: rand.Reader,
		FlowTTL: backofficeFlowTTL, IdleTTL: backofficeIdleTTL, AbsoluteTTL: backofficeAbsoluteTTL,
		RefreshSkew: backofficeRefreshSkew, TouchInterval: backofficeTouchInterval,
		ReturnPathPrefix: "/account/", AllowUnenrolledAccounts: true,
	})
	if err != nil {
		return err
	}
	resolver, err := newIdentityProfileProjectionResolver(cfg, workloadSigners)
	if err != nil {
		return err
	}
	accountWebAuthHandler = &accountWebHTTP{
		service: service, cookies: runtime.cookies, csrf: runtime.csrf, host: runtime.requestHost,
		issuer: cfg.OIDC.Issuer, tenantID: string(tenant.ID), tenantName: tenant.Name, enrollment: requestAccountEnrollment,
		profiles: &accountWebProfileClient{resolver},
	}
	return nil
}

func validateAccountWebRuntimeConfig(cfg ebs_fields.NoebsConfig) error {
	_, err := buildAccountWebRuntimeDependencies(cfg)
	return err
}

func buildAccountWebRuntimeDependencies(cfg ebs_fields.NoebsConfig) (backofficeRuntimeDependencies, error) {
	if _, err := tenantcatalog.ParseID(cfg.WebTenantID); err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	clock := backofficeauth.SystemClock{}
	if err := requireHTTPSKeycloakEndpoint(cfg.OIDC.JWKSURL); err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	if err := requireExactHTTPSCallbackPath(cfg.WebRedirectURL, accountWebCallbackPath); err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	if err := requireExactHTTPSCallbackPath(cfg.WebPostLogoutURL, accountWebLoggedOutPath); err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	origin, err := originOf(cfg.WebRedirectURL)
	logoutOrigin, logoutErr := originOf(cfg.WebPostLogoutURL)
	if err != nil || logoutErr != nil || origin != logoutOrigin {
		return backofficeRuntimeDependencies{}, backofficeauth.ErrInvalidConfiguration
	}
	tlsConfig, err := keycloakClientTLSConfig(cfg.KeycloakCACertificate)
	if err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	client := httpclient.New(httpclient.WithTimeout(backofficeOAuthTimeout), httpclient.WithResponseHeaderTimeout(5*time.Second), httpclient.WithTLSConfig(tlsConfig))
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	keyContext := context.WithValue(context.Background(), oauth2.HTTPClient, client)
	idTokens, err := backofficeauth.NewIDTokenVerifier(cfg.OIDC.Issuer, accountWebClientID, oidc.NewRemoteKeySet(keyContext, cfg.OIDC.JWKSURL), clock)
	if err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	accessConfig := cfg.OIDC
	accessConfig.AllowedClients = []string{accountWebClientID}
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
		Issuer: cfg.OIDC.Issuer, ClientID: accountWebClientID, ClientSecret: cfg.WebClientSecret,
		AuthorizationURL: authorizationURL, TokenURL: tokenURL, RedirectURL: cfg.WebRedirectURL,
		EndSessionURL: endSessionURL, PostLogoutURL: cfg.WebPostLogoutURL,
		Scopes: []string{oidc.ScopeOpenID, "profile", "email", "organization:*"}, HTTPClient: client,
		IDTokens: idTokens, AccessTokens: accessTokens, Clock: clock,
		MaxFutureIssuedAt: time.Duration(cfg.OIDC.MaxFutureIssuedAtSeconds) * time.Second,
	})
	if err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	keys, err := decodeGatewayAuthEncryptionKeys(cfg.GatewayAuthEncryptionKeys)
	if err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	keyring, err := backofficeauth.NewKeyring(backofficeauth.KeyringConfig{ActiveKeyID: cfg.GatewayAuthEncryptionKeyID, Keys: keys, Entropy: rand.Reader})
	if err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	cookies, err := backofficeauth.NewCookiePolicy(backofficeauth.CookiePolicyConfig{FlowName: "__Host-noebs_account_flow", SessionName: "__Host-noebs_account_session"})
	if err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	csrf, err := backofficeauth.NewCSRFProtector(origin)
	if err != nil {
		return backofficeRuntimeDependencies{}, err
	}
	parsed, _ := url.Parse(origin)
	return backofficeRuntimeDependencies{clock: clock, oauth: oauthClient, keys: keyring, cookies: cookies, csrf: csrf, requestHost: parsed.Host}, nil
}
