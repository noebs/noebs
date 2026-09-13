package tenantcutover

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/adonese/noebs/internal/testdb"
	basestore "github.com/adonese/noebs/store"
	walletstore "github.com/adonese/noebs/wallet/store"
)

func TestTenantCutoverPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	pg, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("PostgreSQL fixture unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })
	for _, scope := range []struct{ name, scope string }{
		{"identity_auth", basestore.MigrationScopeIdentityAuth},
		{"wallet_ledger", basestore.MigrationScopeWalletLedger},
		{"card_vault", basestore.MigrationScopeCardVault},
		{"admin_reporting", basestore.MigrationScopeAdminReporting},
		{"notification_chat", basestore.MigrationScopeNotificationChat},
		{"ebs_adapter", basestore.MigrationScopeEBSAdapter},
		{"gateway_auth", basestore.MigrationScopeGatewayAuth},
	} {
		t.Run(scope.name, func(t *testing.T) {
			url, err := pg.CreateDatabaseForRole(ctx, scope.name, scope.name+"_migrate")
			must(t, err)
			t.Cleanup(func() { _ = pg.DropDatabase(context.Background(), scope.name) })
			db, err := basestore.OpenFromConfig(url, basestore.DriverPostgres)
			must(t, err)
			t.Cleanup(func() { _ = db.Close() })
			must(t, basestore.MigrateScope(ctx, db, scope.scope))
			_, err = db.ExecContext(ctx, `INSERT INTO tenants(id,name,created_at) VALUES ('tenant-mojaloop','Old Mojaloop',clock_timestamp()),('tenant-cutover','Cutover',clock_timestamp()),('tenant-sandbox','Sandbox',clock_timestamp())`)
			must(t, err)
			if scope.name == "identity_auth" {
				// A previously revoked account still has a complete receipt. The move
				// must preserve it; new-tenant signup must never become a fresh grant.
				_, err = db.ExecContext(ctx, `INSERT INTO users(tenant_id,issuer,subject,fullname,created_at,updated_at) VALUES ('tenant-mojaloop','https://example.test/auth/realms/noebs','owner','Existing Owner',clock_timestamp(),clock_timestamp()); INSERT INTO account_enrollments(issuer,subject,tenant_id,progress) VALUES ('https://example.test/auth/realms/noebs','revoked-owner','tenant-mojaloop','{"complete":true}')`)
				must(t, err)
				// New synthetic users generate a notification; existing live inventory
				// has no outbox records. Keep this fixture at that exact boundary.
				_, err = db.ExecContext(ctx, `DELETE FROM identity_status_events`)
				must(t, err)
			}
			if scope.name == "wallet_ledger" {
				must(t, walletstore.New(db).SeedInteropDemo(ctx, Source, "noebs"))
				_, err = db.ExecContext(ctx, `INSERT INTO balance_holds(tenant_id,wallet_id,amount,amount_remaining,reason,reference_type,reference_id,idempotency_key,expires_at) SELECT 'tenant-mojaloop',id,1,1,'fixture','fixture','fixture','fixture',clock_timestamp()+interval '1 hour' FROM wallets WHERE owner_id='900000001'`)
				must(t, err)
				_, err = Run(ctx, db.DB.DB, false, nil)
				if !errors.Is(err, ErrUnsafe) {
					t.Fatalf("active hold accepted: %v", err)
				}
				_, err = db.ExecContext(ctx, `DELETE FROM balance_holds WHERE reference_type='fixture'`)
				must(t, err)
				_, err = db.ExecContext(ctx, `INSERT INTO interop_quotes(id,transfer_id,tenant_id,owner_id,wallet_id,idempotency_key,amount,currency,currency_unit_version_id,request,status) SELECT 'e2b725af-90bc-4dcf-b5d5-3f1e0e1ac78f','1a82744c-32b1-4028-957d-0f280067ca0b','tenant-mojaloop',owner_id,id,'fixture-failed',1,currency,currency_unit_version_id,'{ "tenant": "tenant-mojaloop", "raw": true }','FAILED' FROM wallets WHERE owner_id='900000001';
INSERT INTO interop_transfers(id,tenant_id,quote_id,owner_id,idempotency_key,status,hub_state,original_prepare) VALUES ('1a82744c-32b1-4028-957d-0f280067ca0b','tenant-mojaloop','e2b725af-90bc-4dcf-b5d5-3f1e0e1ac78f','900000001','fixture-failed','FAILED','ABORTED','{ "raw":  true }'); DELETE FROM transaction_status_events`)
				must(t, err)
			}
			before, err := Run(ctx, db.DB.DB, false, nil)
			must(t, err)
			// JSON is the operator interface; empty maps and nil values must survive
			// round-trip without making an unchanged manifest look different.
			encoded, err := json.Marshal(before)
			must(t, err)
			var manifest Snapshot
			must(t, json.Unmarshal(encoded, &manifest))
			_, err = Run(ctx, db.DB.DB, true, nil)
			if !errors.Is(err, ErrUnsafe) {
				t.Fatalf("missing manifest accepted: %v", err)
			}
			_, err = db.ExecContext(ctx, `INSERT INTO tenants(id,name,created_at) VALUES ('noebs','Conflicting target',clock_timestamp())`)
			must(t, err)
			_, err = Run(ctx, db.DB.DB, true, &manifest)
			if !errors.Is(err, ErrUnsafe) {
				t.Fatalf("target collision accepted: %v", err)
			}
			_, err = db.ExecContext(ctx, `DELETE FROM tenants WHERE id='noebs'`)
			must(t, err)
			if scope.name == "identity_auth" {
				_, err = db.ExecContext(ctx, `INSERT INTO account_enrollments(issuer,subject,tenant_id,progress) VALUES ('https://example.test/auth/realms/noebs','other-owner','tenant-cutover','{"complete":true}')`)
				must(t, err)
				_, err = Run(ctx, db.DB.DB, true, &manifest)
				if !errors.Is(err, ErrUnsafe) {
					t.Fatalf("other-tenant data accepted: %v", err)
				}
				_, err = db.ExecContext(ctx, `DELETE FROM account_enrollments WHERE tenant_id='tenant-cutover'`)
				must(t, err)
				_, err = db.ExecContext(ctx, `UPDATE account_enrollments SET progress='{"complete":false}' WHERE subject='revoked-owner'`)
				must(t, err)
				_, err = Run(ctx, db.DB.DB, true, &manifest)
				if !errors.Is(err, ErrUnsafe) {
					t.Fatalf("stale manifest accepted: %v", err)
				}
				_, err = db.ExecContext(ctx, `UPDATE account_enrollments SET progress='{"complete":true}' WHERE subject='revoked-owner'`)
				must(t, err)
				// Force a failure after protections are changed and earlier tables
				// have moved. PostgreSQL must roll back both DDL and all row writes.
				_, err = db.ExecContext(ctx, `ALTER TABLE users ADD CONSTRAINT cutover_failure_fixture CHECK (tenant_id <> 'noebs')`)
				must(t, err)
				failureManifest, err := Run(ctx, db.DB.DB, false, nil)
				must(t, err)
				_, err = Run(ctx, db.DB.DB, true, &failureManifest)
				if err == nil {
					t.Fatal("expected mid-cutover constraint failure")
				}
				restored, err := Run(ctx, db.DB.DB, false, nil)
				must(t, err)
				if !reflect.DeepEqual(restored, failureManifest) {
					t.Fatal("failed cutover did not restore all data and protections")
				}
				_, err = db.ExecContext(ctx, `ALTER TABLE users DROP CONSTRAINT cutover_failure_fixture`)
				must(t, err)
			}
			after, err := Run(ctx, db.DB.DB, true, &manifest)
			must(t, err)
			if len(after.Catalog) != 1 || after.Catalog[0] != Target {
				t.Fatalf("legacy catalog survived: %v", after.Catalog)
			}
			for i, table := range before.Tables {
				if after.Tables[i].SHA256 != table.SHA256 || after.Tables[i].Rows != table.Rows {
					t.Fatalf("non-tenant data changed in %s", table.Name)
				}
			}
			if scope.name == "wallet_ledger" {
				_, err = db.ExecContext(ctx, `UPDATE interop_bindings SET tenant_id='tenant-cutover' WHERE tenant_id='noebs'`)
				if err == nil {
					t.Fatal("immutable binding trigger was not restored")
				}
				runtimeURL, err := pg.DatabaseURLForRole(scope.name, "wallet_ledger_runtime")
				must(t, err)
				runtimeDB, err := basestore.OpenFromConfig(runtimeURL, basestore.DriverPostgres)
				must(t, err)
				defer runtimeDB.Close()
				_, err = runtimeDB.ExecContext(ctx, `UPDATE ledger_entries SET amount=amount+1 WHERE tenant_id='noebs'`)
				if err == nil {
					t.Fatal("runtime ledger write protection was not preserved")
				}
			}
			if scope.name == "identity_auth" {
				var complete bool
				must(t, db.QueryRowContext(ctx, `SELECT (progress->>'complete')::boolean FROM account_enrollments WHERE tenant_id='noebs' AND subject='revoked-owner'`).Scan(&complete))
				if !complete {
					t.Fatal("revocation receipt lost")
				}
			}
			_, err = Run(ctx, db.DB.DB, true, &manifest)
			if !errors.Is(err, ErrUnsafe) {
				t.Fatalf("implicit rerun accepted: %v", err)
			}
		})
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
