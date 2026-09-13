# Existing personal-account cutover to Noebs

The canonical tenant is `noebs` (display name `Noebs`). Mojaloop remains its
payment connection with DFSP ID `noebs`; it is not a separate application tenant.
This administrative operation preserves the existing `tenant-mojaloop` account,
wallets, balances and settled payment history. It does not seed new wallets or
demo funding, combine independent tenant accounts, or rewrite signed payloads.

`cmd/tenant-cutover` is intentionally limited to the seven business databases:
`identity_auth`, `wallet_ledger`, `card_vault`, `admin_reporting`,
`notification_chat`, `ebs_adapter`, and `gateway_auth`. `workload_auth`, Keycloak,
and Temporal have separate authority/lifecycle handling.

## Deployment order

1. Stop public admission, every application writer and worker, and scheduled
   jobs. Verify no running Temporal work refers to the retiring tenant. Confirm
   interop transfers are terminal, holds have no remaining amount, all inbox
   events are applied, and there are no usable or leased quotes.
2. Take restorable backups of all business databases and Keycloak. Keep the old
   application configuration and desired state with the backup.
3. Generate a read-only manifest for **each** database using its administrative
   owner connection, and review all seven before applying any. Reports contain
   row counts and hashes, not customer data or connection credentials.
4. Apply each database using its exact saved manifest. Keep every service stopped
   until all seven succeed. A database transaction either commits its entire
   cutover or restores all rows, trigger states and FK definitions. There is no
   distributed transaction across databases: if any fails, keep admission stopped
   and finish the reviewed cutover or restore the coordinated backups.
5. In Keycloak, preserve the global user subject and restore its exact membership
   classes in the new `noebs` organization. Keycloak 26.7 rejects changing an
   existing organization's alias, so a new organization is required. Retire old
   organizations only after verifying the expected membership inventory.
6. Deploy the one-entry tenant catalog, `noebs` enrollment policy and frontend
   tenant configuration. Configure the wallet worker with
   `interop_tenant=noebs`, `interop_fsp_id=noebs`. Do not invoke `SeedInteropDemo`.
7. Require fresh frontend authorization, then verify the same user/profile ID,
   wallet IDs, balances and history under `noebs`. Retain before/after manifests.

## Command interface

Build a static Linux binary suitable for the database administration environment:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/tenant-cutover ./cmd/tenant-cutover
```

Pass a PostgreSQL connection through an environment variable, never a password on
the command line. An existing peer-authenticated database administration shell can
use `NOEBS_CUTOVER_DATABASE_URL='postgres:///identity_auth?host=/var/run/postgresql'`.
Repeat the commands with distinct files and the matching connection for every
business database. Files must not already exist.

```sh
/tmp/tenant-cutover --database-url-env NOEBS_CUTOVER_DATABASE_URL \
  --source tenant-mojaloop --target noebs --report /tmp/identity_auth.before.json

/tmp/tenant-cutover --database-url-env NOEBS_CUTOVER_DATABASE_URL \
  --source tenant-mojaloop --target noebs --apply \
  --expected /tmp/identity_auth.before.json --report /tmp/identity_auth.after.json
```

The apply operation locks all public tables, checks the exact manifest, temporarily
defers foreign keys and disables user triggers within the transaction, updates
only `tenant_id`, retires the source and the two verified-empty demo catalog
entries, and restores every original protection. Internal FK triggers remain
enabled. Before committing, it verifies the same row counts, non-tenant data hashes
and schema protection definitions. JSON wire payloads are hashed as their original
text, preserving whitespace as well as values; byte payloads and stored digests
also remain unchanged. Enrollment completion receipts move with their tenant key,
including receipts for subsequently revoked memberships.

Any target catalog entry, other-tenant data, unreviewed nonempty workflow/evidence
table, encrypted auth session, active funds/work, stale manifest, or changed
non-tenant row content causes refusal. The tool deliberately rejects implicit
reruns and arbitrary tenant merges. It never grants runtime roles administrative
privileges. A connection interruption during commit has an uncertain outcome;
inspect the database and saved manifests before taking further action.

## Validation

`TestTenantCutoverPostgres` exercises all seven real schemas, migrates a synthetic
wallet journal and settled interop history, preserves revoked enrollment receipts,
checks raw JSON preservation and runtime write restrictions, rejects collisions,
active holds and stale manifests, and forces a failure midway through migration
to prove transactional restoration of rows and protections.

Run only against a disposable test cluster: the shared fixture recreates canonical
test database names.

```sh
NOEBS_TEST_POSTGRES_URL=postgres://adonese@127.0.0.1:55432/postgres?sslmode=disable \
  go test ./internal/tenantcutover ./internal/accountenrollment -count=1
```
