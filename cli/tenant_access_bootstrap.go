package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/backofficeauth"
	"github.com/adonese/noebs/internal/tenantaccess"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/adonese/noebs/store"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

func isTenantAccessBootstrapCommand() bool {
	return len(os.Args) > 1 && os.Args[1] == "bootstrap-tenant-admin"
}
func tenantAccessBootstrapCommand() error { return runTenantAccessBootstrap(os.Args[2:], os.Stdout) }

type accessBootstrapOptions struct{ config, service, secrets, databaseSecrets, catalog, tenant, operation, expectedSubject, reasonFile string }

func parseAccessBootstrapOptions(args []string) (accessBootstrapOptions, error) {
	var o accessBootstrapOptions
	f := flag.NewFlagSet("bootstrap-tenant-admin", flag.ContinueOnError)
	f.StringVar(&o.config, "config", "", "application configuration YAML")
	f.StringVar(&o.service, "service", "", "identity-auth service YAML")
	f.StringVar(&o.secrets, "secrets", "", "mounted identity-auth runtime secrets YAML")
	f.StringVar(&o.databaseSecrets, "database-secrets", "", "separate identity-auth migration-owner secrets YAML")
	f.StringVar(&o.catalog, "tenant-catalog", "", "authoritative tenant catalog YAML")
	f.StringVar(&o.tenant, "tenant", "", "explicit selected tenant")
	f.StringVar(&o.operation, "operation-id", "", "stable bootstrap UUID retained on retries")
	f.StringVar(&o.expectedSubject, "expected-subject", "", "reviewed existing immutable operator subject; omit only for operation-owned creation")
	f.StringVar(&o.reasonFile, "reason-file", "", "file containing the accountable deployment reason")
	if err := f.Parse(args); err != nil {
		return o, err
	}
	if f.NArg() != 0 || o.config == "" || o.service == "" || o.secrets == "" || o.databaseSecrets == "" || o.catalog == "" || o.tenant == "" || o.reasonFile == "" || o.operation == "" {
		return o, errors.New("bootstrap requires explicit config, service, secrets, database-secrets, tenant-catalog, tenant, operation-id and reason-file")
	}
	for _, id := range []string{o.operation, o.expectedSubject} {
		if id == "" {
			continue
		}
		v, err := uuid.Parse(id)
		if err != nil || v == uuid.Nil || v.String() != id {
			return o, errors.New("bootstrap requires canonical UUIDs")
		}
	}
	return o, nil
}
func runTenantAccessBootstrap(args []string, output io.Writer) error {
	o, err := parseAccessBootstrapOptions(args)
	if err != nil {
		return err
	}
	// Read configuration into memory; credentials and action links are never output.
	merged := map[string]interface{}{}
	for _, path := range []string{o.config, o.service, o.secrets} {
		payload, err := os.ReadFile(path)
		if err != nil {
			return errors.New("cannot read bootstrap configuration")
		}
		document := map[string]interface{}{}
		if err = yaml.Unmarshal(payload, &document); err != nil || document["sops"] != nil {
			return errors.New("bootstrap requires mounted plaintext runtime configuration")
		}
		merged = mergeConfig(merged, document).(map[string]interface{})
	}
	payload, err := json.Marshal(getMap(merged, "noebs"))
	if err != nil {
		return err
	}
	var cfg ebs_fields.NoebsConfig
	if err = json.Unmarshal(payload, &cfg); err != nil || cfg.ServiceRole != string(serviceRoleIdentityAuth) {
		return errors.New("bootstrap requires identity-auth configuration")
	}
	issuerURL, err := url.Parse(cfg.OIDC.Issuer)
	if err != nil {
		return errors.New("invalid bootstrap issuer")
	}
	if err = backofficeauth.ValidateSeparateOrigin(cfg.BackofficeOrigin, issuerURL.Scheme+"://"+issuerURL.Host); err != nil {
		return errors.New("bootstrap requires the validated private backoffice origin")
	}
	catalogFile, err := os.Open(o.catalog)
	if err != nil {
		return err
	}
	catalog, err := tenantcatalog.Load(catalogFile)
	catalogFile.Close()
	if err != nil {
		return err
	}
	if _, err = catalog.Require(o.tenant); err != nil {
		return err
	}
	reasonFile, err := os.Open(o.reasonFile)
	if err != nil {
		return errors.New("cannot read bootstrap reason")
	}
	reasonBytes, err := io.ReadAll(io.LimitReader(reasonFile, 2002))
	reasonFile.Close()
	if err != nil || len(reasonBytes) > 2001 {
		return errors.New("invalid bootstrap reason")
	}
	reason := strings.TrimSpace(string(reasonBytes))
	if reason == "" || len(reason) > 2000 {
		return errors.New("bootstrap reason must contain 1–2000 bytes")
	}
	databasePayload, err := os.ReadFile(o.databaseSecrets)
	if err != nil {
		return errors.New("cannot read bootstrap migration authority")
	}
	var database struct {
		Noebs struct {
			Databases map[string]string `yaml:"service_databases"`
			CA        string            `yaml:"database_ca_certificate"`
		} `yaml:"noebs"`
		Sops any `yaml:"sops"`
	}
	if err = yaml.Unmarshal(databasePayload, &database); err != nil || database.Sops != nil {
		return errors.New("invalid mounted bootstrap database authority")
	}
	dsn := database.Noebs.Databases[string(serviceRoleIdentityAuth)]
	spec, _ := postgresRoleSpecForService(serviceRoleIdentityAuthMigrate)
	if validatePostgresDatabaseIdentity(dsn, spec) != nil || store.ValidateDatabaseTLSConfig(dsn, database.Noebs.CA) != nil {
		return errors.New("bootstrap requires the dedicated identity-auth migration owner and configured TLS CA")
	}
	db, err := openPostgresDatabaseWithAuthority(dsn, store.DriverPostgres, database.Noebs.CA, spec)
	if err != nil {
		return errors.New("cannot open bootstrap migration authority")
	}
	defer db.Close()
	service, authority, err := newTenantAccessService(cfg, db, catalog)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	subject, err := authority.ServiceAccountSubject(ctx)
	if err != nil {
		return errors.New("cannot verify dedicated deployment authority identity")
	}
	actor := tenantaccess.Actor{Kind: "deployment-bootstrap", Issuer: cfg.OIDC.Issuer, Subject: subject, TenantID: o.tenant, SourceIP: "127.0.0.1", RequestID: uuid.NewString()}
	request := tenantaccess.BootstrapRequest{OperationID: o.operation, Reason: reason, ExpectedSubject: o.expectedSubject, BackofficeOrigin: cfg.BackofficeOrigin}
	result, err := service.BootstrapOperator(ctx, actor, request, authority)
	if err != nil {
		return err
	}
	journal, err := tenantaccess.NewPostgresJournal(db.DB.DB)
	if err != nil {
		return err
	}
	receipt, err := journal.BootstrapReceipt(ctx, actor.Issuer, actor.TenantID)
	if err != nil || receipt == nil || !receipt.Complete {
		return errors.New("cannot read completed bootstrap receipt; retain the same operation ID")
	}
	return json.NewEncoder(output).Encode(struct {
		Bootstrap *tenantaccess.BootstrapReceipt `json:"bootstrap"`
		Account   tenantaccess.Account           `json:"account"`
		Operation tenantaccess.Audit             `json:"operation"`
	}{receipt, result.Account, result.Operation})
}
