package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/adonese/noebs/internal/keycloakadmin"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/sirupsen/logrus"
)

func isAssignKeycloakMembershipsCommand() bool {
	return len(os.Args) > 1 && os.Args[1] == "assign-keycloak-memberships"
}

func isLookupKeycloakSubjectCommand() bool {
	return len(os.Args) > 1 && os.Args[1] == "lookup-keycloak-subject"
}

func assignKeycloakMembershipsCommand() error {
	memberships, actions, dryRun, err := runAssignKeycloakMemberships(os.Args[2:], nil)
	if err != nil {
		return err
	}
	for _, action := range actions {
		fields := logrus.Fields{
			"subject": action.Subject,
			"tenant":  action.Tenant,
			"action":  action.Action,
			"dry_run": dryRun,
		}
		if action.Class != "" {
			fields["class"] = action.Class
		}
		logrusLogger.WithFields(fields).Info("Keycloak membership action")
	}
	logrusLogger.WithFields(logrus.Fields{
		"subject": memberships.Subject,
		"actions": len(actions),
		"dry_run": dryRun,
	}).Info("Keycloak memberships reconciled")
	return nil
}

func lookupKeycloakSubjectCommand() error {
	subject, err := runLookupKeycloakSubject(os.Args[2:], nil)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, subject)
	return err
}

func runAssignKeycloakMemberships(args []string, httpClient *http.Client) (keycloakadmin.Memberships, []keycloakadmin.PlannedMembershipAction, bool, error) {
	flags := flag.NewFlagSet("assign-keycloak-memberships", flag.ContinueOnError)
	membershipsPath := flags.String("memberships", "", "path to the exact Keycloak membership assignment")
	desiredStatePath := flags.String("desired-state", "", "path to the repository-owned Keycloak desired state")
	tenantCatalogPath := flags.String("tenant-catalog", "", "path to the repository-owned tenant catalog")
	configPath := flags.String("config", "", "path to the realm-local Keycloak reconciler Secret config")
	caPath := flags.String("ca", "", "path to the Keycloak transport CA certificate")
	dryRun := flags.Bool("dry-run", false, "print the stable action plan without changing Keycloak")
	if err := flags.Parse(args); err != nil {
		return keycloakadmin.Memberships{}, nil, false, err
	}
	if *membershipsPath == "" || *desiredStatePath == "" || *tenantCatalogPath == "" || *configPath == "" || *caPath == "" || flags.NArg() != 0 {
		return keycloakadmin.Memberships{}, nil, false, errors.New("assign-keycloak-memberships requires --memberships, --desired-state, --tenant-catalog, --config, and --ca")
	}

	catalog, state, config, err := loadKeycloakMembershipAuthority(*tenantCatalogPath, *desiredStatePath, *configPath)
	if err != nil {
		return keycloakadmin.Memberships{}, nil, *dryRun, err
	}
	membershipFile, err := os.Open(*membershipsPath)
	if err != nil {
		return keycloakadmin.Memberships{}, nil, *dryRun, fmt.Errorf("open Keycloak memberships: %w", err)
	}
	memberships, err := keycloakadmin.LoadMemberships(membershipFile, catalog, state)
	if closeErr := membershipFile.Close(); closeErr != nil && err == nil {
		return keycloakadmin.Memberships{}, nil, *dryRun, fmt.Errorf("close Keycloak memberships: %w", closeErr)
	}
	if err != nil {
		return keycloakadmin.Memberships{}, nil, *dryRun, err
	}
	if httpClient == nil {
		httpClient, err = keycloakHTTPClient(*caPath, config.BaseURL)
		if err != nil {
			return keycloakadmin.Memberships{}, nil, *dryRun, err
		}
	}
	reconciler, err := keycloakadmin.New(config, httpClient)
	if err != nil {
		return keycloakadmin.Memberships{}, nil, *dryRun, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), keycloakReconcileTimeout)
	defer cancel()
	actions, err := reconciler.AssignMemberships(ctx, state, memberships, *dryRun)
	return memberships, actions, *dryRun, err
}

func runLookupKeycloakSubject(args []string, httpClient *http.Client) (string, error) {
	flags := flag.NewFlagSet("lookup-keycloak-subject", flag.ContinueOnError)
	email := flags.String("email", "", "exact realm user email")
	emailFile := flags.String("email-file", "", "path containing the exact realm user email")
	configPath := flags.String("config", "", "path to the realm-local Keycloak reconciler Secret config")
	caPath := flags.String("ca", "", "path to the Keycloak transport CA certificate")
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	if (*email == "") == (*emailFile == "") || *configPath == "" || *caPath == "" || flags.NArg() != 0 {
		return "", errors.New("lookup-keycloak-subject requires exactly one of --email or --email-file, and --config and --ca")
	}
	lookupEmail := *email
	if *emailFile != "" {
		payload, err := os.ReadFile(*emailFile)
		if err != nil {
			return "", fmt.Errorf("read Keycloak lookup email: %w", err)
		}
		lookupEmail = strings.TrimSuffix(string(payload), "\n")
		if strings.ContainsAny(lookupEmail, "\r\n") {
			return "", keycloakadmin.ErrInvalidLookupEmail
		}
	}
	configFile, err := os.Open(*configPath)
	if err != nil {
		return "", fmt.Errorf("open Keycloak reconciler config: %w", err)
	}
	config, err := keycloakadmin.LoadConfig(configFile)
	if closeErr := configFile.Close(); closeErr != nil && err == nil {
		return "", fmt.Errorf("close Keycloak reconciler config: %w", closeErr)
	}
	if err != nil {
		return "", err
	}
	if httpClient == nil {
		httpClient, err = keycloakHTTPClient(*caPath, config.BaseURL)
		if err != nil {
			return "", err
		}
	}
	reconciler, err := keycloakadmin.New(config, httpClient)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), keycloakReconcileTimeout)
	defer cancel()
	return reconciler.LookupSubjectByEmail(ctx, lookupEmail)
}

func loadKeycloakMembershipAuthority(catalogPath, statePath, configPath string) (tenantcatalog.Catalog, keycloakadmin.DesiredState, keycloakadmin.Config, error) {
	catalog, err := tenantcatalog.LoadFile(catalogPath)
	if err != nil {
		return tenantcatalog.Catalog{}, keycloakadmin.DesiredState{}, keycloakadmin.Config{}, err
	}
	desiredStateFile, err := os.Open(statePath)
	if err != nil {
		return tenantcatalog.Catalog{}, keycloakadmin.DesiredState{}, keycloakadmin.Config{}, fmt.Errorf("open Keycloak desired state: %w", err)
	}
	state, err := keycloakadmin.LoadDesiredState(desiredStateFile, catalog)
	if closeErr := desiredStateFile.Close(); closeErr != nil && err == nil {
		return tenantcatalog.Catalog{}, keycloakadmin.DesiredState{}, keycloakadmin.Config{}, fmt.Errorf("close Keycloak desired state: %w", closeErr)
	}
	if err != nil {
		return tenantcatalog.Catalog{}, keycloakadmin.DesiredState{}, keycloakadmin.Config{}, err
	}
	configFile, err := os.Open(configPath)
	if err != nil {
		return tenantcatalog.Catalog{}, keycloakadmin.DesiredState{}, keycloakadmin.Config{}, fmt.Errorf("open Keycloak reconciler config: %w", err)
	}
	config, err := keycloakadmin.LoadConfig(configFile)
	if closeErr := configFile.Close(); closeErr != nil && err == nil {
		return tenantcatalog.Catalog{}, keycloakadmin.DesiredState{}, keycloakadmin.Config{}, fmt.Errorf("close Keycloak reconciler config: %w", closeErr)
	}
	if err != nil {
		return tenantcatalog.Catalog{}, keycloakadmin.DesiredState{}, keycloakadmin.Config{}, err
	}
	return catalog, state, config, nil
}
