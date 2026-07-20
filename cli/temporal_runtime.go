package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	httpclient "github.com/adonese/noebs/internal/httpclient"
	walletworker "github.com/adonese/noebs/wallet/worker"
	"go.temporal.io/sdk/client"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

var errInvalidTemporalRuntime = errors.New("invalid Temporal runtime configuration")

const (
	temporalLedgerClientID    = "noebs-temporal-wallet-ledger"
	temporalWorkerClientID    = "noebs-temporal-wallet-worker"
	temporalBootstrapClientID = "noebs-temporal-namespace-bootstrap"
)

func buildTemporalOptions(ctx context.Context, cfg ebs_fields.NoebsConfig, taskQueue walletworker.TaskQueue, expectedClientID string) (walletworker.Options, error) {
	opts, err := buildTemporalConnectionOptions(ctx, cfg, expectedClientID)
	if err != nil {
		return walletworker.Options{}, err
	}
	opts.TaskQueue = taskQueue
	if err := opts.Validate(); err != nil {
		return walletworker.Options{}, fmt.Errorf("%w: %w", errInvalidTemporalRuntime, err)
	}
	return opts, nil
}

func buildTemporalConnectionOptions(ctx context.Context, cfg ebs_fields.NoebsConfig, expectedClientID string) (walletworker.Options, error) {
	serverName := strings.TrimSpace(cfg.TemporalServerName)
	if serverName == "" {
		return walletworker.Options{}, fmt.Errorf("%w: temporal_server_name is required", errInvalidTemporalRuntime)
	}
	temporalTLS, err := authorityClientTLSConfig(cfg.TemporalCACertificate)
	if err != nil {
		return walletworker.Options{}, fmt.Errorf("%w: temporal_ca_certificate: %v", errInvalidTemporalRuntime, err)
	}
	temporalTLS.ServerName = serverName

	tokenURL := strings.TrimSpace(cfg.TemporalTokenURL)
	if err := requireHTTPSKeycloakEndpoint(tokenURL); err != nil {
		return walletworker.Options{}, fmt.Errorf("%w: temporal_token_url: %v", errInvalidTemporalRuntime, err)
	}
	clientID := strings.TrimSpace(cfg.TemporalClientID)
	clientSecret := strings.TrimSpace(cfg.TemporalClientSecret)
	if clientID != expectedClientID {
		return walletworker.Options{}, fmt.Errorf("%w: temporal_client_id must be %q", errInvalidTemporalRuntime, expectedClientID)
	}
	if clientSecret == "" {
		return walletworker.Options{}, fmt.Errorf("%w: temporal_client_secret is required", errInvalidTemporalRuntime)
	}
	keycloakTLS, err := keycloakClientTLSConfig(cfg.KeycloakCACertificate)
	if err != nil {
		return walletworker.Options{}, fmt.Errorf("%w: keycloak_ca_certificate: %v", errInvalidTemporalRuntime, err)
	}
	tokenClient := httpclient.New(
		httpclient.WithTimeout(10*time.Second),
		httpclient.WithResponseHeaderTimeout(5*time.Second),
		httpclient.WithTLSConfig(keycloakTLS),
	)
	tokens := temporalTokenSource(ctx, tokenClient, tokenURL, clientID, clientSecret)

	return walletworker.Options{
		Host:      strings.TrimSpace(cfg.TemporalHost),
		Port:      strings.TrimSpace(cfg.TemporalPort),
		Namespace: strings.TrimSpace(cfg.TemporalNamespace),
		TLS:       temporalTLS,
		Credentials: client.NewAPIKeyDynamicCredentials(func(context.Context) (string, error) {
			token, err := tokens.Token()
			if err != nil {
				return "", fmt.Errorf("acquire Temporal access token: %w", err)
			}
			return token.AccessToken, nil
		}),
	}, nil
}

func temporalTokenSource(ctx context.Context, tokenClient *http.Client, tokenURL, clientID, clientSecret string) oauth2.TokenSource {
	tokenContext := context.WithValue(ctx, oauth2.HTTPClient, tokenClient)
	return (&clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     tokenURL,
		AuthStyle:    oauth2.AuthStyleInHeader,
	}).TokenSource(tokenContext)
}
