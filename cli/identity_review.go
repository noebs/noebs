package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/verification"
	"github.com/adonese/noebs/store"
	walletworker "github.com/adonese/noebs/wallet/worker"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

func isIdentityReviewCommand() bool { return len(os.Args) > 1 && os.Args[1] == "identity-review" }
func identityReviewCommand() error  { return runIdentityReview(os.Args[2:], os.Stdout, nil) }

type identityReviewCommandOptions struct {
	action, secrets, tenant, reviewer, session, output, operation, decision, reasonFile, policy string
	userID, revision                                                                            int64
	limit, offset                                                                               int
	evidenceReviewed                                                                            bool
	kind                                                                                        string
	config, service                                                                             string
}

func parseIdentityReviewOptions(args []string) (identityReviewCommandOptions, error) {
	var options identityReviewCommandOptions
	flags := flag.NewFlagSet("identity-review", flag.ContinueOnError)
	flags.StringVar(&options.action, "action", "", "queue, case, evidence, or decide")
	flags.StringVar(&options.secrets, "secrets", "", "identity-auth runtime secrets YAML")
	flags.StringVar(&options.config, "config", defaultConfigPath, "application YAML for Temporal decisions")
	flags.StringVar(&options.service, "service", defaultServiceConfigPath, "identity-auth service YAML for Temporal decisions")
	flags.StringVar(&options.tenant, "tenant", "", "explicit tenant to review")
	flags.StringVar(&options.reviewer, "reviewer", "", "accountable reviewer identity (recorded with the database login)")
	flags.StringVar(&options.session, "session", "", "canonical case UUID")
	flags.Int64Var(&options.userID, "user-id", 0, "owner's numeric NoEBS user ID")
	flags.Int64Var(&options.revision, "revision", 0, "reviewed case revision")
	flags.IntVar(&options.limit, "limit", 50, "queue page size, 1–100")
	flags.IntVar(&options.offset, "offset", 0, "queue offset, 0–100000")
	flags.StringVar(&options.kind, "kind", "", "document_front, document_back or selfie")
	flags.StringVar(&options.output, "output", "", "new private file beneath .state (required for case/evidence)")
	flags.StringVar(&options.operation, "operation-id", "", "stable decision UUID retained through retries")
	flags.StringVar(&options.decision, "decision", "", "approved, needs_information or rejected")
	flags.StringVar(&options.reasonFile, "reason-file", "", "file containing the customer-facing decision reason")
	flags.StringVar(&options.policy, "policy", "", "version/reference of the applied manual review policy")
	flags.BoolVar(&options.evidenceReviewed, "evidence-reviewed", false, "attest that the supplied claims and every exported evidence image were reviewed")
	if err := flags.Parse(args); err != nil {
		return options, err
	}
	if flags.NArg() != 0 || options.secrets == "" || options.reviewer == "" || options.reviewer != strings.TrimSpace(options.reviewer) || len(options.reviewer) > 256 || strings.ContainsAny(options.reviewer, "\r\n\x00") {
		return options, errors.New("identity-review requires --action, --secrets, --tenant and --reviewer")
	}
	if _, err := store.ValidateTenantID(options.tenant); err != nil {
		return options, err
	}
	switch options.action {
	case "queue":
		if options.limit < 1 || options.limit > 100 || options.offset < 0 || options.offset > 100000 {
			return options, errors.New("invalid review queue page")
		}
	case "case", "evidence", "decide":
		id, err := uuid.Parse(options.session)
		if err != nil || id == uuid.Nil || id.String() != options.session || options.userID < 1 {
			return options, errors.New("case operations require canonical --session and positive --user-id")
		}
		if options.action == "case" || options.action == "evidence" {
			if options.output == "" {
				return options, errors.New("case/evidence requires --output beneath .state; private evidence is never printed to logs")
			}
			if _, err := identityReviewOutputPath(options.output); err != nil {
				return options, err
			}
		}
		if options.action == "evidence" && (options.revision < 1 || !store.ValidIdentityEvidenceKind(options.kind)) {
			return options, errors.New("evidence requires --revision and a supported --kind")
		}
		if options.action == "decide" {
			operation, err := uuid.Parse(options.operation)
			if err != nil || operation == uuid.Nil || operation.String() != options.operation || options.revision < 1 || options.reasonFile == "" || options.policy == "" || !options.evidenceReviewed || (options.decision != "approved" && options.decision != "needs_information" && options.decision != "rejected") {
				return options, errors.New("decide requires --revision, --operation-id, --decision, --reason-file, --policy and --evidence-reviewed")
			}
		}
	default:
		return options, errors.New("identity-review --action must be queue, case, evidence or decide")
	}
	return options, nil
}

// This command is an operator tool, never a public/mobile API. Existing host and
// database access authorize execution. The supplied reviewer name is an explicit
// accountability assertion; PostgreSQL independently records its session login.
func runIdentityReview(args []string, stdout io.Writer, openDB func(string) (*store.DB, error)) error {
	options, err := parseIdentityReviewOptions(args)
	if err != nil {
		return err
	}
	if openDB == nil {
		openDB = openIdentityReviewDatabase
	}
	var output *os.File
	completed := false
	if options.action == "case" || options.action == "evidence" {
		output, err = createIdentityReviewOutput(options.output)
		if err != nil {
			return err
		}
		defer func() {
			_ = output.Close()
			if !completed {
				_ = os.Remove(output.Name())
			}
		}()
	}
	var reason string
	if options.action == "decide" {
		file, err := os.Open(options.reasonFile)
		if err != nil {
			return errors.New("cannot open decision reason file")
		}
		payload, readErr := io.ReadAll(io.LimitReader(file, 2002))
		_ = file.Close()
		if readErr != nil || len(payload) > 2001 {
			return errors.New("decision reason file is invalid or too large")
		}
		reason = strings.TrimSpace(string(payload))
		if reason == "" || len(reason) > 2000 {
			return errors.New("decision reason must contain 1–2000 bytes")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	reviewer := store.IdentityReviewer{TenantID: options.tenant, Actor: options.reviewer}
	owner := store.IdentityOwner{TenantID: options.tenant, UserID: options.userID}
	session, _ := uuid.Parse(options.session)
	if options.action == "decide" {
		operation, _ := uuid.Parse(options.operation)
		decision := store.IdentityReviewDecisionParams{Reviewer: reviewer, Owner: owner, SessionID: session, OperationID: operation, Revision: options.revision, Decision: options.decision, Reason: reason, PolicyReference: options.policy, EvidenceReviewed: options.evidenceReviewed}
		result, err := executeIdentityReviewDecision(ctx, options, decision)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(map[string]any{"session_id": result.ID, "status": result.Status, "revision": result.Revision, "operation_id": operation})
	}
	db, err := openDB(options.secrets)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	identityStore := store.New(db)
	switch options.action {
	case "queue":
		cases, err := identityStore.ListIdentityReviewQueue(ctx, reviewer, options.limit, options.offset)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(cases)
	case "case":
		result, err := identityStore.ReadIdentityReviewCase(ctx, reviewer, owner, session)
		if err != nil {
			return err
		}
		if err = json.NewEncoder(output).Encode(result); err != nil {
			return err
		}
	case "evidence":
		payload, err := identityStore.ReadIdentityReviewEvidence(ctx, reviewer, owner, session, options.revision, options.kind)
		if err != nil {
			return err
		}
		if _, err = output.Write(payload); err != nil {
			return err
		}
	}
	if err = output.Sync(); err != nil {
		return err
	}
	if err = output.Close(); err != nil {
		return err
	}
	completed = true
	_, err = fmt.Fprintln(stdout, "Private review file created.")
	return err
}

func executeIdentityReviewDecision(ctx context.Context, options identityReviewCommandOptions, decision store.IdentityReviewDecisionParams) (store.IdentitySession, error) {
	if err := store.ValidateIdentityReviewDecision(decision); err != nil {
		return store.IdentitySession{}, err
	}
	merged := map[string]interface{}{}
	for _, path := range []string{options.config, options.service, options.secrets} {
		payload, err := os.ReadFile(path)
		if err != nil {
			return store.IdentitySession{}, errors.New("cannot read identity decision runtime configuration")
		}
		var document map[string]interface{}
		if err := yaml.Unmarshal(payload, &document); err != nil {
			return store.IdentitySession{}, errors.New("invalid identity decision runtime configuration")
		}
		merged = mergeConfig(merged, document).(map[string]interface{})
	}
	payload, err := json.Marshal(getMap(merged, "noebs"))
	if err != nil {
		return store.IdentitySession{}, err
	}
	var cfg ebs_fields.NoebsConfig
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return store.IdentitySession{}, err
	}
	if cfg.ServiceRole != string(serviceRoleIdentityAuth) {
		return store.IdentitySession{}, errors.New("identity decisions require identity-auth runtime configuration")
	}
	opts, err := buildTemporalOptions(ctx, cfg, walletworker.TaskQueue(verification.TaskQueue), temporalIdentityClientID)
	if err != nil {
		return store.IdentitySession{}, err
	}
	connection, err := walletworker.NewClient(ctx, opts)
	if err != nil {
		return store.IdentitySession{}, err
	}
	defer connection.Close()
	db, err := openIdentityReviewDatabase(options.secrets)
	if err != nil {
		return store.IdentitySession{}, err
	}
	defer db.Close()
	workflow := &verification.Client{Temporal: connection, Sessions: store.New(db)}
	return workflow.Execute(ctx, verification.Command{Action: "decide", Owner: decision.Owner, SessionID: decision.SessionID, Decision: decision})
}

func identityReviewOutputPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	components := strings.Split(filepath.Clean(absolute), string(filepath.Separator))
	contained := false
	for i, component := range components {
		if component == ".state" && i < len(components)-1 {
			contained = true
			break
		}
	}
	if !contained {
		return "", errors.New("review output must be beneath .state")
	}
	return absolute, nil
}

func createIdentityReviewOutput(path string) (*os.File, error) {
	absolute, err := identityReviewOutputPath(path)
	if err != nil {
		return nil, err
	}
	directory := filepath.Dir(absolute)
	// Reject symbolic-link traversal before creating any directories or files.
	cursor := string(filepath.Separator)
	for _, component := range strings.Split(strings.TrimPrefix(directory, string(filepath.Separator)), string(filepath.Separator)) {
		cursor = filepath.Join(cursor, component)
		info, err := os.Lstat(cursor)
		if os.IsNotExist(err) {
			if err = os.Mkdir(cursor, 0700); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("review output directories must be real directories")
		}
	}
	return os.OpenFile(absolute, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}

func identityReviewDatabaseConfig(path string) (string, string, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", "", errors.New("cannot read identity-auth runtime secrets")
	}
	var secrets struct {
		Noebs struct {
			ServiceDatabases map[string]string `yaml:"service_databases"`
			CACertificate    string            `yaml:"database_ca_certificate"`
		} `yaml:"noebs"`
	}
	if err = yaml.Unmarshal(payload, &secrets); err != nil {
		return "", "", errors.New("invalid identity-auth runtime secrets YAML")
	}
	spec, ok := postgresRoleSpecForService(serviceRoleIdentityAuth)
	if !ok {
		return "", "", errors.New("identity-auth database role missing")
	}
	databaseURL := secrets.Noebs.ServiceDatabases[string(serviceRoleIdentityAuth)]
	if err = validatePostgresDatabaseIdentity(databaseURL, spec); err != nil {
		return "", "", errors.New("review requires the identity-auth runtime database identity")
	}
	if err = store.ValidateDatabaseTLSConfig(databaseURL, secrets.Noebs.CACertificate); err != nil {
		return "", "", errors.New("review requires the configured identity-auth database TLS CA")
	}
	return databaseURL, secrets.Noebs.CACertificate, nil
}

func openIdentityReviewDatabase(path string) (*store.DB, error) {
	databaseURL, ca, err := identityReviewDatabaseConfig(path)
	if err != nil {
		return nil, err
	}
	spec, _ := postgresRoleSpecForService(serviceRoleIdentityAuth)
	db, err := openPostgresDatabaseWithAuthority(databaseURL, store.DriverPostgres, ca, spec)
	if err != nil {
		return nil, errors.New("cannot open authorized identity-auth review database")
	}
	return db, nil
}
