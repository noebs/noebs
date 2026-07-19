package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/adonese/noebs/internal/keycloakadmin"
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
	configPath := flags.String("config", "", "path to the Keycloak reconciler Secret config")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if *desiredStatePath == "" || *configPath == "" || flags.NArg() != 0 {
		return errors.New("reconcile-keycloak requires --desired-state and --config")
	}

	desiredStateFile, err := os.Open(*desiredStatePath)
	if err != nil {
		return fmt.Errorf("open Keycloak desired state: %w", err)
	}
	defer desiredStateFile.Close()
	desiredState, err := keycloakadmin.LoadDesiredState(desiredStateFile)
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

	reconciler, err := keycloakadmin.New(config, keycloakHTTPClient())
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
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if *configPath == "" || flags.NArg() != 0 {
		return errors.New("delete-keycloak-bootstrap-client requires --config")
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
	reconciler, err := keycloakadmin.New(config, keycloakHTTPClient())
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

func keycloakHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
}
