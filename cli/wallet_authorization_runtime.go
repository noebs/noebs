package main

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/httpclient"
	"github.com/adonese/noebs/internal/transactionauth"
	"github.com/adonese/noebs/store"
	walletrequest "github.com/adonese/noebs/wallet/request"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	walletAuthorizerClientID                = "noebs-wallet-authorizer"
	walletAuthorizerRequiredACR             = "urn:noebs:acr:google-totp"
	walletAuthorizationFlowCookieName       = "__Host-noebs_wallet_authorization_flow"
	walletAuthorizationBrowserStartPath     = "/wallet/authorizations/browser"
	walletAuthorizationCallbackPath         = "/wallet/authorizations/oauth/callback"
	walletAuthorizationBrowserStartTTL      = 10 * time.Minute
	walletAuthorizationFlowTTL              = 5 * time.Minute
	walletAuthorizationTTL                  = 2 * time.Minute
	walletAuthorizationMaxAuthenticationAge = 2 * time.Minute
	walletAuthorizationOAuthTimeout         = 10 * time.Second
)

var walletAuthorizationHandler *walletAuthorizationHTTP

type walletAuthorizationRuntime struct {
	clock        transactionauth.SystemClock
	oauth        *transactionauth.OAuthClient
	keys         *transactionauth.Keyring
	requestHost  string
	publicOrigin string
}

func initWalletAuthorization(role serviceRole, cfg ebs_fields.NoebsConfig, db *store.DB) error {
	walletAuthorizationHandler = nil
	if role != serviceRoleAPIGateway {
		return nil
	}
	if db == nil || db.DB == nil {
		return errors.New("api-gateway transaction authorization database is not initialized")
	}
	runtime, err := buildWalletAuthorizationRuntime(cfg)
	if err != nil {
		return err
	}
	repository, err := transactionauth.NewPostgresStore(db.DB.DB)
	if err != nil {
		return err
	}
	service, err := transactionauth.NewService(transactionauth.ServiceConfig{
		Repository:       repository,
		OAuth:            runtime.oauth,
		Keys:             runtime.keys,
		Clock:            runtime.clock,
		Entropy:          rand.Reader,
		RequiredACR:      walletAuthorizerRequiredACR,
		BrowserStartTTL:  walletAuthorizationBrowserStartTTL,
		FlowTTL:          walletAuthorizationFlowTTL,
		AuthorizationTTL: walletAuthorizationTTL,
	})
	if err != nil {
		return err
	}
	walletAuthorizationHandler = &walletAuthorizationHTTP{
		service:      service,
		issuer:       cfg.OIDC.Issuer,
		host:         runtime.requestHost,
		publicOrigin: runtime.publicOrigin,
		defaults:     gatewayWalletRequestDefaults(cfg),
	}
	return nil
}

func validateWalletAuthorizationRuntimeConfig(cfg ebs_fields.NoebsConfig) error {
	_, err := buildWalletAuthorizationRuntime(cfg)
	return err
}

func buildWalletAuthorizationRuntime(cfg ebs_fields.NoebsConfig) (walletAuthorizationRuntime, error) {
	clock := transactionauth.SystemClock{}
	if err := requireHTTPSKeycloakEndpoint(cfg.OIDC.JWKSURL); err != nil {
		return walletAuthorizationRuntime{}, err
	}
	tlsConfig, err := keycloakClientTLSConfig(cfg.KeycloakCACertificate)
	if err != nil {
		return walletAuthorizationRuntime{}, err
	}
	client := httpclient.New(
		httpclient.WithTimeout(walletAuthorizationOAuthTimeout),
		httpclient.WithResponseHeaderTimeout(5*time.Second),
		httpclient.WithTLSConfig(tlsConfig),
	)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	keyContext := context.WithValue(context.Background(), oauth2.HTTPClient, client)
	idTokens, err := transactionauth.NewIDTokenVerifier(
		cfg.OIDC.Issuer,
		walletAuthorizerClientID,
		oidc.NewRemoteKeySet(keyContext, cfg.OIDC.JWKSURL),
		clock,
	)
	if err != nil {
		return walletAuthorizationRuntime{}, err
	}
	tokenURL, err := replaceOIDCEndpoint(cfg.OIDC.JWKSURL, "certs", "token")
	if err != nil {
		return walletAuthorizationRuntime{}, err
	}
	authorizationURL, err := appendOIDCEndpoint(cfg.OIDC.Issuer, "auth")
	if err != nil {
		return walletAuthorizationRuntime{}, err
	}
	oauthClient, err := transactionauth.NewOAuthClient(transactionauth.OAuthClientConfig{
		Issuer:               cfg.OIDC.Issuer,
		ClientID:             walletAuthorizerClientID,
		ClientSecret:         cfg.WalletAuthorizerClientSecret,
		AuthorizationURL:     authorizationURL,
		TokenURL:             tokenURL,
		RedirectURL:          cfg.WalletAuthorizerRedirectURL,
		HTTPClient:           client,
		IDTokens:             idTokens,
		Clock:                clock,
		RequiredACR:          walletAuthorizerRequiredACR,
		MaxAuthenticationAge: walletAuthorizationMaxAuthenticationAge,
		MaxFutureIssuedAt:    time.Duration(cfg.OIDC.MaxFutureIssuedAtSeconds) * time.Second,
	})
	if err != nil {
		return walletAuthorizationRuntime{}, err
	}
	keys, err := decodeGatewayAuthEncryptionKeys(cfg.GatewayAuthEncryptionKeys)
	if err != nil {
		return walletAuthorizationRuntime{}, err
	}
	keyring, err := transactionauth.NewKeyring(transactionauth.KeyringConfig{
		ActiveKeyID: cfg.GatewayAuthEncryptionKeyID,
		Keys:        keys,
		Entropy:     rand.Reader,
	})
	if err != nil {
		return walletAuthorizationRuntime{}, err
	}
	if err := requireExactHTTPSCallbackPath(cfg.WalletAuthorizerRedirectURL, walletAuthorizationCallbackPath); err != nil {
		return walletAuthorizationRuntime{}, err
	}
	publicOrigin, err := originOf(cfg.WalletAuthorizerRedirectURL)
	if err != nil {
		return walletAuthorizationRuntime{}, err
	}
	parsedOrigin, err := url.Parse(publicOrigin)
	if err != nil || parsedOrigin.Scheme != "https" || parsedOrigin.Host == "" {
		return walletAuthorizationRuntime{}, transactionauth.ErrInvalidConfiguration
	}
	return walletAuthorizationRuntime{
		clock:        clock,
		oauth:        oauthClient,
		keys:         keyring,
		requestHost:  parsedOrigin.Host,
		publicOrigin: publicOrigin,
	}, nil
}

func gatewayWalletRequestDefaults(cfg ebs_fields.NoebsConfig) walletrequest.Defaults {
	return walletrequest.Defaults{
		HoldExpirySeconds:      int32(cfg.WalletHoldExpirySeconds),
		ApprovalTimeoutSeconds: int32(cfg.WalletApprovalTimeoutSeconds),
		ApprovalThreshold:      cfg.WalletApprovalThreshold,
	}
}
