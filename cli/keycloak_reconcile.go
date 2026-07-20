package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/adonese/noebs/internal/httpclient"
	"github.com/adonese/noebs/internal/keycloakadmin"
	"github.com/adonese/noebs/internal/tenantcatalog"
)

const keycloakReconcileTimeout = 2 * time.Minute

func isReconcileKeycloakCommand() bool {
	return len(os.Args) > 1 && os.Args[1] == "reconcile-keycloak"
}

func isDeleteKeycloakBootstrapCommand() bool {
	return len(os.Args) > 1 && os.Args[1] == "delete-keycloak-bootstrap-client"
}

func reconcileKeycloakCommand() error {
	flags := flag.NewFlagSet("reconcile-keycloak", flag.ContinueOnError)
	desiredStatePath := flags.String("desired-state", "", "path to the repository-owned Keycloak desired state")
	tenantCatalogPath := flags.String("tenant-catalog", "", "path to the repository-owned tenant catalog")
	configPath := flags.String("config", "", "path to the Keycloak reconciler Secret config")
	caPath := flags.String("ca", "", "path to the Keycloak transport CA certificate")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if *desiredStatePath == "" || *tenantCatalogPath == "" || *configPath == "" || *caPath == "" || flags.NArg() != 0 {
		return errors.New("reconcile-keycloak requires --desired-state, --tenant-catalog, --config, and --ca")
	}

	catalog, err := tenantcatalog.LoadFile(*tenantCatalogPath)
	if err != nil {
		return err
	}
	desiredStateFile, err := os.Open(*desiredStatePath)
	if err != nil {
		return fmt.Errorf("open Keycloak desired state: %w", err)
	}
	defer desiredStateFile.Close()
	desiredState, err := keycloakadmin.LoadDesiredState(desiredStateFile, catalog)
	if err != nil {
		return err
	}
	configFile, err := os.Open(*configPath)
	if err != nil {
		return fmt.Errorf("open Keycloak reconciler config: %w", err)
	}
	defer configFile.Close()
	config, err := keycloakadmin.LoadConfig(configFile)
	if err != nil {
		return err
	}

	httpClient, err := keycloakHTTPClient(*caPath, config.BaseURL)
	if err != nil {
		return err
	}
	reconciler, err := keycloakadmin.New(config, httpClient)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), keycloakReconcileTimeout)
	defer cancel()
	result, err := reconciler.Reconcile(ctx, desiredState)
	if err != nil {
		return err
	}
	logrusLogger.WithField("created", result.Created).
		WithField("updated", result.Updated).
		WithField("deleted", result.Deleted).
		Info("Keycloak desired state reconciled")
	return nil
}

func deleteKeycloakBootstrapCommand() error {
	flags := flag.NewFlagSet("delete-keycloak-bootstrap-client", flag.ContinueOnError)
	configPath := flags.String("config", "", "path to the temporary Keycloak bootstrap client config")
	caPath := flags.String("ca", "", "path to the Keycloak transport CA certificate")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if *configPath == "" || *caPath == "" || flags.NArg() != 0 {
		return errors.New("delete-keycloak-bootstrap-client requires --config and --ca")
	}
	configFile, err := os.Open(*configPath)
	if err != nil {
		return fmt.Errorf("open Keycloak bootstrap config: %w", err)
	}
	defer configFile.Close()
	config, err := keycloakadmin.LoadConfig(configFile)
	if err != nil {
		return err
	}
	httpClient, err := keycloakHTTPClient(*caPath, config.BaseURL)
	if err != nil {
		return err
	}
	reconciler, err := keycloakadmin.New(config, httpClient)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), keycloakReconcileTimeout)
	defer cancel()
	if err := reconciler.DeleteBootstrapClient(ctx); err != nil {
		return err
	}
	logrusLogger.Info("temporary Keycloak bootstrap client deleted")
	return nil
}

func keycloakHTTPClient(caPath, endpoint string) (*http.Client, error) {
	if err := requireHTTPSKeycloakEndpoint(endpoint); err != nil {
		return nil, err
	}
	tlsConfig, err := readKeycloakClientTLSConfig(caPath)
	if err != nil {
		return nil, err
	}
	client := httpclient.New(
		httpclient.WithTimeout(30*time.Second),
		httpclient.WithMaxIdleConns(10),
		httpclient.WithIdleConnTimeout(30*time.Second),
		httpclient.WithTLSHandshakeTimeout(5*time.Second),
		httpclient.WithResponseHeaderTimeout(15*time.Second),
		httpclient.WithTLSConfig(tlsConfig),
	)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client, nil
}
