package transactionauth_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/testdb"
	"github.com/adonese/noebs/internal/transactionauth"
	"github.com/adonese/noebs/store"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// This exercises the actual service and PostgreSQL runtime role. The OIDC
// exchanger is a synthetic test double; this does not claim Google/TOTP E2E.
func TestInteropReviewGatewayAuthorizationPostgres(t *testing.T) {
	if os.Getenv("NOEBS_TEST_POSTGRES_URL") == "" {
		t.Skip("requires the dedicated disposable NOEBS_TEST_POSTGRES_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	postgres, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgres.Terminate(context.Background()) })
	const databaseName = "gateway_auth"
	migrationURL, err := postgres.CreateDatabaseForRole(ctx, databaseName, "gateway_auth_migrate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgres.DropDatabase(context.Background(), databaseName) })
	migrationDB, err := store.OpenFromConfig(migrationURL, store.DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateScope(ctx, migrationDB, store.MigrationScopeGatewayAuth); err != nil {
		_ = migrationDB.Close()
		t.Fatal(err)
	}
	if _, err := migrationDB.ExecContext(ctx, `INSERT INTO tenants(id,name) VALUES ('synthetic','Synthetic review')`); err != nil {
		_ = migrationDB.Close()
		t.Fatal(err)
	}
	if err := migrationDB.Close(); err != nil {
		t.Fatal(err)
	}
	runtimeURL, err := postgres.DatabaseURLForRole(databaseName, "gateway_auth_runtime")
	if err != nil {
		t.Fatal(err)
	}
	runtimeDB, err := sql.Open("pgx", runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeDB.Close() })
	var runtimeRole string
	if err := runtimeDB.QueryRowContext(ctx, `SELECT current_user`).Scan(&runtimeRole); err != nil || runtimeRole != "gateway_auth_runtime" {
		t.Fatalf("expected gateway_auth_runtime connection; role=%q error=%v", runtimeRole, err)
	}
	repository, err := transactionauth.NewPostgresStore(runtimeDB)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	bound := transactionauth.Binding{TenantID: "synthetic", Issuer: "https://identity.synthetic.invalid/realms/noebs", Subject: "synthetic-subject", Operation: transactionauth.OperationWalletInterop, RequestDigest: sha256.Sum256([]byte("immutable synthetic quote and transfer key")), IdempotencyKey: "synthetic-interop-1"}
	oauth := &interopReviewOAuth{identity: transactionauth.VerifiedIdentity{Issuer: bound.Issuer, Subject: bound.Subject, ACR: "urn:noebs:acr:mfa", AuthenticationTime: now}}
	keys, err := transactionauth.NewKeyring(transactionauth.KeyringConfig{ActiveKeyID: "synthetic-key", Keys: map[string][]byte{"synthetic-key": make([]byte, 32)}, Entropy: rand.Reader})
	if err != nil {
		t.Fatal(err)
	}
	service, err := transactionauth.NewService(transactionauth.ServiceConfig{Repository: repository, OAuth: oauth, Keys: keys, Clock: interopReviewClock{now}, Entropy: rand.Reader, RequiredACR: "urn:noebs:acr:mfa", BrowserStartTTL: 10 * time.Minute, FlowTTL: 5 * time.Minute, AuthorizationTTL: 2 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := service.Begin(ctx, bound)
	if err != nil {
		t.Fatalf("interop Begin through actual gateway role: %v", err)
	}
	// Prove the database constraint still rejects an operation outside the
	// public catalog, independently of the Go boundary's Operation.Valid check.
	badIntent, badBrowser := sha256.Sum256([]byte("invalid-operation-intent")), sha256.Sum256([]byte("invalid-operation-browser"))
	_, err = runtimeDB.ExecContext(ctx, `INSERT INTO wallet_transaction_authorization_intents
 (intent_hash,browser_start_hash,tenant_id,issuer,subject,operation,request_digest,idempotency_key,created_at,expires_at)
 SELECT $1,$2,tenant_id,issuer,subject,'wallet.interop.unchecked',request_digest,'invalid-operation',created_at,expires_at
 FROM wallet_transaction_authorization_intents WHERE idempotency_key=$3`, badIntent[:], badBrowser[:], bound.IdempotencyKey)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "23514" {
		t.Fatalf("arbitrary operation did not fail a database check constraint: %v", err)
	}
	if err := service.Claim(ctx, intent.IntentToken, bound); !errors.Is(err, transactionauth.ErrAuthorizationDenied) {
		t.Fatalf("claim without step-up: %v", err)
	}
	challenge, err := service.StartBrowser(ctx, intent.BrowserStartToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Complete(ctx, oauth.state, challenge.BrowserBinding, "synthetic-verified-code"); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*transactionauth.Binding){
		"other tenant":            func(b *transactionauth.Binding) { b.TenantID = "other-synthetic" },
		"other subject":           func(b *transactionauth.Binding) { b.Subject = "other-subject" },
		"other issuer":            func(b *transactionauth.Binding) { b.Issuer = "https://other.synthetic.invalid/realms/noebs" },
		"other operation":         func(b *transactionauth.Binding) { b.Operation = transactionauth.OperationWalletP2P },
		"changed quote":           func(b *transactionauth.Binding) { b.RequestDigest = sha256.Sum256([]byte("different quote")) },
		"changed idempotency key": func(b *transactionauth.Binding) { b.IdempotencyKey = "other-transfer" },
	} {
		wrong := bound
		mutate(&wrong)
		if err := service.Claim(ctx, intent.IntentToken, wrong); !errors.Is(err, transactionauth.ErrAuthorizationDenied) {
			t.Errorf("%s claim: %v", name, err)
		}
	}
	if err := service.Claim(ctx, intent.IntentToken, bound); err != nil {
		t.Fatalf("correct claim after rejected mismatches: %v", err)
	}
	if err := service.Claim(ctx, intent.IntentToken, bound); !errors.Is(err, transactionauth.ErrAuthorizationDenied) {
		t.Fatalf("repeated claim: %v", err)
	}
	var count int
	if err := runtimeDB.QueryRowContext(ctx, `SELECT count(*) FROM wallet_transaction_authorization_flows`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("claimed authorization left a reusable browser flow; count=%d error=%v", count, err)
	}
}

type interopReviewClock struct{ now time.Time }

func (c interopReviewClock) Now() time.Time { return c.now }

type interopReviewOAuth struct {
	state, verifier string
	nonce           transactionauth.Digest
	identity        transactionauth.VerifiedIdentity
}

func (o *interopReviewOAuth) AuthorizationURL(state, nonce, verifier string) (string, error) {
	o.state, o.verifier, o.nonce = state, verifier, sha256.Sum256([]byte(nonce))
	return "https://identity.synthetic.invalid/authorize", nil
}

func (o *interopReviewOAuth) Exchange(_ context.Context, code, verifier string, nonce transactionauth.Digest) (transactionauth.VerifiedIdentity, error) {
	if code != "synthetic-verified-code" || verifier != o.verifier || nonce != o.nonce {
		return transactionauth.VerifiedIdentity{}, transactionauth.ErrAuthorizationDenied
	}
	return o.identity, nil
}
