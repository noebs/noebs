package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	walletworker "github.com/adonese/noebs/wallet/worker"
)

func TestBuildTemporalOptionsPinsRoleIdentityAndTLSName(t *testing.T) {
	cfg := validWalletRuntimeConfig(serviceRoleWalletLedger)
	opts, err := buildTemporalOptions(context.Background(), cfg, walletworker.TaskQueueMain, temporalLedgerClientID)
	if err != nil {
		t.Fatalf("buildTemporalOptions() error = %v", err)
	}
	if opts.TLS == nil || opts.TLS.ServerName != "temporal-frontend" || opts.TLS.RootCAs == nil {
		t.Fatalf("Temporal TLS options = %#v", opts.TLS)
	}
	if opts.Credentials == nil {
		t.Fatal("Temporal credentials are missing")
	}
	cfg.TemporalClientID = temporalBootstrapClientID
	if _, err := buildTemporalOptions(context.Background(), cfg, walletworker.TaskQueueMain, temporalLedgerClientID); err == nil {
		t.Fatal("wallet-ledger accepted namespace-bootstrap credentials")
	}
}

func TestTemporalTokenSourceRefreshesExpiringClientCredential(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		clientID, clientSecret, ok := request.BasicAuth()
		if !ok || clientID != temporalWorkerClientID || clientSecret != "worker-secret" {
			t.Errorf("token endpoint authorization = %q", request.Header.Get("Authorization"))
		}
		if err := request.ParseForm(); err != nil || request.Form.Get("grant_type") != "client_credentials" {
			t.Errorf("token endpoint form = %v, error = %v", request.Form, err)
		}
		sequence := requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(writer, `{"access_token":"token-%d","token_type":"Bearer","expires_in":1}`, sequence)
	}))
	defer server.Close()

	tokens := temporalTokenSource(context.Background(), server.Client(), server.URL, temporalWorkerClientID, "worker-secret")
	first, err := tokens.Token()
	if err != nil {
		t.Fatalf("first Token() error = %v", err)
	}
	second, err := tokens.Token()
	if err != nil {
		t.Fatalf("second Token() error = %v", err)
	}
	if first.AccessToken != "token-1" || second.AccessToken != "token-2" || requests.Load() != 2 {
		t.Fatalf("renewed tokens = %q, %q; requests = %d", first.AccessToken, second.AccessToken, requests.Load())
	}
}
