package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	walletworker "github.com/adonese/noebs/wallet/worker"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

const temporalNamespaceBootstrapTimeout = 2 * time.Minute

func isEnsureTemporalNamespaceCommand() bool {
	return len(os.Args) > 1 && os.Args[1] == "ensure-temporal-namespace"
}

func ensureTemporalNamespaceCommand() error {
	flags := flag.NewFlagSet("ensure-temporal-namespace", flag.ContinueOnError)
	address := flags.String("address", "", "Temporal frontend host and port")
	tokenURL := flags.String("token-url", "", "Keycloak client-credentials token endpoint")
	temporalCAPath := flags.String("temporal-ca", "", "Temporal transport CA path")
	keycloakCAPath := flags.String("keycloak-ca", "", "Keycloak transport CA path")
	clientSecretPath := flags.String("client-secret", "", "Temporal namespace bootstrap client secret path")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if *address == "" || *tokenURL == "" || *temporalCAPath == "" || *keycloakCAPath == "" || *clientSecretPath == "" || flags.NArg() != 0 {
		return errors.New("ensure-temporal-namespace requires --address, --token-url, --temporal-ca, --keycloak-ca, and --client-secret")
	}
	host, port, err := net.SplitHostPort(*address)
	if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return errors.New("ensure-temporal-namespace address must contain a host and port")
	}
	temporalCA, err := os.ReadFile(*temporalCAPath)
	if err != nil {
		return fmt.Errorf("read Temporal transport CA: %w", err)
	}
	keycloakCA, err := os.ReadFile(*keycloakCAPath)
	if err != nil {
		return fmt.Errorf("read Keycloak transport CA: %w", err)
	}
	clientSecret, err := readRequiredSecretValue("Temporal namespace bootstrap client secret", *clientSecretPath)
	if err != nil {
		return err
	}
	cfg := ebs_fields.NoebsConfig{
		TemporalHost:          host,
		TemporalPort:          port,
		TemporalNamespace:     "default",
		TemporalServerName:    "temporal-frontend",
		TemporalCACertificate: string(temporalCA),
		TemporalTokenURL:      *tokenURL,
		TemporalClientID:      temporalBootstrapClientID,
		TemporalClientSecret:  clientSecret,
		KeycloakCACertificate: string(keycloakCA),
	}
	ctx, cancel := context.WithTimeout(context.Background(), temporalNamespaceBootstrapTimeout)
	defer cancel()
	opts, err := buildTemporalConnectionOptions(ctx, cfg, temporalBootstrapClientID)
	if err != nil {
		return err
	}
	client, err := walletworker.NewNamespaceClient(opts)
	if err != nil {
		return fmt.Errorf("connect to Temporal namespace service: %w", err)
	}
	defer client.Close()
	if _, err := client.Describe(ctx, "default"); err == nil {
		return nil
	} else {
		var notFound *serviceerror.NamespaceNotFound
		if !errors.As(err, &notFound) {
			return fmt.Errorf("describe Temporal namespace: %w", err)
		}
	}
	if err := client.Register(ctx, &workflowservice.RegisterNamespaceRequest{
		Namespace:                        "default",
		Description:                      "Noebs wallet workflow namespace",
		WorkflowExecutionRetentionPeriod: durationpb.New(72 * time.Hour),
	}); err != nil {
		return fmt.Errorf("create Temporal namespace: %w", err)
	}
	return nil
}
