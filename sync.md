# Keycloak authority cutover

Last updated: 2026-07-20

Repository code and locally reproduced evidence are authoritative. GitHub
checks, pull-request state, and prose in this file are not release evidence.

## Main-agent verdict

**GO for the committed Keycloak browser/session, tenancy, transport, and IaC
baseline at `c162ad1`. NO-GO for complete human-auth authority and for the live
cutover until the remaining release gates below are closed.**

The local/server feedback loop found and fixed two defects that Docker-less
local validation had missed: PostgreSQL fee fixtures without their mandatory
operator attribution, and fresh Keycloak clients that required a second
mutation pass after Keycloak normalized their create representation. Exact
commit `c162ad11102fb7728e042b5405d5c818780ce4b4` now passes the full runnable
server matrix, with the expected skips listed below, and a genuinely empty
Keycloak 26.7 reconciliation. Independent auth, transport, IaC, release, and
fix reviewers found no surviving P0/P1/P2 defect in that baseline. The live
cluster has not been mutated for this cutover. It still runs the old release,
K3s Secret encryption is disabled, and no token from the new `noebs` realm has
crossed the deployed gateway.

The principal code blocker is repository-owned wallet PIN and TOTP authority.
Removing both is required to make Keycloak the only human credential authority.
`ResetWalletPIN`, `SetWalletPIN`, `ConfirmUser2FA`, and `DisableUser2FA` contain
pre-existing user edits in protected `wallet/grpc/funding.go`;
`EnrollUser2FA` is adjacent but does not contain a user-edited hunk. The main
agent asked for approval to remove the protected local-credential slice while
preserving every unrelated hunk and has not received it yet. Removal alone is
not enough: the current wallet flow is an amount-triggered per-operation gate,
while the committed Keycloak flow proves session MFA. The reviewed replacement
candidate is a Keycloak LoA2 ceremony with Max Age zero that authorizes one
immutable, expiring, one-use transaction intent. That candidate is not
approved, implemented, or release-proved yet.

## Authority model proved by code

- Browser/session authentication is Google federation into one Keycloak
  `noebs` realm.
  There is no local username/password execution, direct grant, implicit grant,
  password reset, local recovery, static API key, Basic auth, or HS256 human
  token path.
- Keycloak owns browser-session TOTP enrollment and challenge.
  `CONFIGURE_TOTP` is the only enabled/default required action. The exact
  policy is TOTP, HMAC-SHA256, six digits, 30 seconds, look-ahead one, and
  non-reusable codes.
- The browser flow is an SSO cookie plus an exact Google default-provider
  redirector. First broker login creates only a unique subject; collisions
  fail closed. The required Google post-broker flow is OTP only.
- `noebs-mobile` is public and `noebs-backoffice` is confidential. Both use
  Authorization Code with PKCE S256. Direct grants, service accounts, implicit
  flow, full scope, and authorization services are disabled for interactive
  clients.
- `noebs-api` is the resource client. Tenant membership classes are exactly
  `user`, `backoffice`, and `tenant-admin`; route permissions are explicit
  client roles inherited through organization groups.
- Tenants are exact Keycloak Organizations backed by the repository tenant
  catalog. One subject may have a different membership class in each tenant.
  A request selects exactly one tenant; roles are never unioned across
  organizations.
- Human identity is `(issuer, subject)`. Numeric user IDs are local profile
  projections, never credential authority.
- The API gateway is the sole bearer-token verifier. It verifies RS256,
  issuer, one exact audience, authorized party, token type, key ID, lifetime,
  and the selected organization. It signs the complete derived principal for
  downstream services.
- Downstream workloads accept only the signed V2 workload envelope. Public
  tenant, role, permission, user, actor, source-IP, and legacy credential
  headers are stripped and cannot become authority.
- Back-office sessions are opaque PostgreSQL records with encrypted token
  material, refresh locking, CSRF, origin checks, nonce, PKCE, and RP logout.
- Membership changes use the realm-local repository command. It audits the
  complete client/role/group topology before reading or changing a subject,
  plans exact add/change/remove operations, applies them, and rereads the
  result.
- Wallet P2P/withdrawal still has local wallet PIN and TOTP enrollment,
  hashing/secret storage, RPC, workflow, and database paths. They are surviving
  application-owned human credential authorities and are why this document
  does not yet declare complete Keycloak authority.

## Exact Keycloak topology

- Desired clients: `noebs-api`, `noebs-mobile`, `noebs-backoffice`, and the
  realm-local `noebs-keycloak-reconciler`.
- Keycloak 26.7 refuses deletion of fixed `account`, `account-console`,
  `admin-cli`, and `security-admin-console` clients with HTTP 400. The
  reconciler therefore owns them as exact disabled shells with no flows,
  redirects, origins, scopes, mappers, grants, service accounts, or role
  mappings. This is platform topology, not a fallback authority.
- `broker` and `realm-management` remain the only active Keycloak built-ins.
- Unmanaged clients, IdPs, mappers, scopes, realm roles, flows, organizations,
  groups, composites, and mappings are pruned. A second reconcile must report
  zero changes.

## Transport and deployment authority

- Keycloak application traffic is HTTPS-only on 8443 with TLS 1.3. Its
  management health port is separate, unexposed by a Service, and restricted
  to the node probe path.
- Every gateway, OIDC, back-office, reconciliation, lookup, membership, and
  bootstrap client verifies the internal CA and exact Keycloak service name.
  There is no plaintext or skip-verification path.
- Keycloak Postgres has a distinct leaf identity. Keycloak uses
  `verify-server`; Postgres accepts only `hostssl ... scram-sha-256` and
  explicitly rejects `hostnossl`, including after first initdb.
- Caddy is the only public edge. It exposes an exact `noebs` discovery/JWKS,
  authorization, token, logout, Google broker, and required-action allowlist;
  blanket `/auth` access returns 404. Sensitive OAuth query fields are
  redacted from logs.
- Kubernetes bootstrap and steady overlays are separate. The temporary master
  client is deleted before steady realm-local reconciliation.
- OpenTofu owns namespaces, Argo wiring, and immutable Git revisions. It does
  not read or persist Kubernetes Secret values. Runtime Secret rendering is a
  separate strict release boundary.

## Reproduced evidence

Independent fresh Keycloak 26.7 probe using the pinned image at exact commit
`c162ad11102fb7728e042b5405d5c818780ce4b4`:

- first empty-state reconcile: `created=38 updated=119 deleted=2`;
- immediate second reconcile: `created=0 updated=0 deleted=0`;
- hostile auth drift repair: `created=0 updated=8 deleted=2`;
- final reconcile: `created=0 updated=0 deleted=0`;
- PKCE authorization: 303 to the local Google broker, then 303 to
  `accounts.google.com`, with no username/password form marker;
- exact flows, bindings, required actions, OTP policy, clients, mappings, and
  stock-client inertness verified through the Admin REST API.

Independent transport probes using the pinned production images:

- Keycloak listened only on HTTPS 8443 for application traffic;
- CA-verified TLS 1.3 returned 200;
- wrong hostname, wrong CA, TLS 1.2, and plaintext application traffic failed;
- the exact `0440 root:1000` Secret mount worked with supplemental group 1000;
- pinned Caddy adapted the repository Caddyfile successfully.

Current local deterministic gates:

- `go test ./... -count=1`: pass;
- `go vet ./...`: pass;
- auth-critical race suite: pass;
- `golangci-lint --new-from-rev=HEAD`: no findings;
- uncapped full-lint comparison against pre-cutover commit `b745d9d`: no
  cutover-only finding, with total findings reduced from 215 to 148;
- `go mod tidy -diff`: clean;
- changed-file formatting and `git diff --check`: clean;
- OpenTofu recursive formatting/init/validate: pass;
- all nine Kustomize entry points render and pass current-host server-side
  dry-run;
- all shell syntax, protobuf/OpenAPI regeneration, Templ regeneration, and
  release-image invariant gates: pass.

Exact server bundle SHA-256:
`57e8360954190b87c9cde48aaac8b2dc21939666dcd379e3ae6e0a5acb1cdfac`.
The fresh detached server checkout at the commit above passed:

- `go test -p 1 ./... -count=1 -timeout=20m`, including all available
  PostgreSQL and Temporal suites;
- `go vet -p 1 ./...`, `go mod tidy -diff`, and the auth-critical race suite;
- all 15 shell syntax checks and all nine Kustomize renders;
- recursive OpenTofu formatting/init/validation;
- clean protobuf/OpenAPI and Templ regeneration;
- `govulncheck v1.6.0` with zero reachable or imported vulnerabilities.

The Go vulnerability database reports zero symbol or imported-package findings
and one module-only advisory, `GO-2026-5932`, for the unmaintained
`x/crypto/openpgp` package. The repository imports `x/crypto/bcrypt`, not the
affected `openpgp` package. Expected server skips were the separately-passed
real Keycloak environment gate, externally disabled EBS purchase/card-transfer
tests, and two mutually exclusive no-Postgres harness cases while PostgreSQL
was available. Disposable clones, tools, certificates, test containers, and
the Keycloak probe were removed afterward.

The live Google callback and OTP UI still require the real Google credential
configuration and a user-controlled browser session; direct Google redirection
and managed topology alone are proved.

Five-run benchmark evidence, without a checked-in comparison baseline:

- keyring seal/open: median 4,005 ns/op, 11,728 B, 9 allocs;
- CSRF validation: 209.2 ns/op, 256 B, 6 allocs;
- in-memory session authentication: 2,576 ns/op, 3,048 B, 25 allocs;
- warm tenant authorization: 38.88 ns/op, zero allocations;
- warm bearer verification: median 26,069 ns/op, 8,616 B, 108 allocs;
- cached workload capabilities: 49.22 ns/op, 64 B, 2 allocs.

## Reviewed transaction authorization proposal

The reviewed but not yet user-approved no-fallback replacement for wallet PIN
and TOTP is:

- a Keycloak browser flow with reusable Google LoA1 and OTP LoA2 whose LoA
  condition has Max Age zero;
- a LoA2-aware Google post-broker flow for first login, because Keycloak does
  not resume later browser-flow executions after broker completion;
- a confidential `noebs-wallet-authorizer` code+PKCE client with a server-side
  minimum LoA2 requirement;
- one immutable intent bound to tenant, subject, operation, normalized request
  digest, and idempotency key;
- callback verification of issuer, audience/client, subject, nonce, PKCE,
  exact ACR, and authentication time, followed by token disposal;
- one atomic intent claim before any PSP mutation or Temporal start.

An elevated access token alone is not equivalent: it contains no amount,
recipient, provider, destination, or request digest and can authorize multiple
operations during its lifetime. The intent stores authorization state only;
Keycloak would remain the sole human credential and ceremony authority. This
design still requires user approval, implementation, a real fresh-Keycloak
browser proof, concurrency and replay tests, Temporal empty-state reset, and
explicit approval for the protected file overlap. It also adds the authorizer
client and LoA topology to the exact Keycloak state; those objects do not exist
in the committed baseline yet.

## Live state and remaining release gates

Current host facts:

- SSH host: `adonese@100.102.164.34`, K3s v1.35.4, node Ready;
- the Tailscale address is management-only; public DNS resolves both hostnames
  to the public address `213.199.63.78`;
- old Noebs release remains live;
- K3s Secret encryption is disabled;
- `/` is plain ext4 and there is no approved encrypted mount for the K3s
  datastore/token backup, state quarantine, cutover directory, or rendered
  Secret material;
- the server has no proven GHCR publisher credential and the release image is
  not anonymously resolvable;
- no SOPS-encrypted release input, bootstrap input, prepared release, or
  immutable publication receipt exists;
- the live Google credential keys exist and are non-empty, but they have not
  been accepted into a strict release input and the external broker redirect
  registration remains unconfirmed;
- the existing server `.sops/age-key.txt` files are not accepted release-key
  authority or custody; only a deliberately prepared encrypted release input
  may become that authority;
- old Ingress, `noebs-tls`, unhashed edge ConfigMap, legacy service Secrets,
  and old Keycloak data still exist;
- foundation and application server checkouts are old and dirty, so neither
  may be used as source authority;
- the old foundation state contains legacy Kubernetes Secret data-source
  addresses and must be sanitized without displaying values;
- Google still must be confirmed to allow the exact broker redirect
  `https://api.noebs.sd/auth/realms/noebs/broker/google/endpoint`;
- the prepared release still needs an externally authoritative PSP
  `callback_id` value.

Release gates, in order:

1. Obtain approval for the protected overlap and the one-use LoA2 transaction
   authorization proposal. Implement it and remove wallet-owned PIN and TOTP
   end-to-end while preserving unrelated user edits.
2. Commit and push the exact intended tree without staging any unrelated
   protected user changes; repeat the full local/server/fresh-Keycloak matrix.
3. Provide an approved encrypted mount, GHCR publisher authority, a strict
   SOPS release input, the PSP callback identifier, and the registered Google
   broker redirect.
4. Build the exact commit on the server, push an immutable image, verify the
   registry digest, and commit the digest plus exact Argo revision.
5. Back up the complete K3s datastore and matching token, enable `secretbox`
   encryption using the documented existing-cluster sequence, and verify the
   node after each restart.
6. Prepare one strict encrypted release input. Preserve external Google/EBS
   credentials, rotate local credentials, render Secrets without printing
   them, and sanitize foundation state before its first new-code plan.
7. Perform the documented coordinated empty-state replacement, bootstrap
   Keycloak, prove the temporary master client returns 401 after deletion,
   switch to the steady overlay, and remove temporary Secrets.
8. Assign one temporary subject different roles in both tenants through the
   production command. Verify selected-tenant allow/deny behavior, no role
   union, edge 404s for admin/other realms, then remove the subject.
9. Update this file with immutable commit/image/digest, backup/encryption,
   deployment, live token, membership, and cleanup evidence; issue the final
   main-agent verdict.

## Workspace protection

These pre-existing user changes remain outside this cutover checkpoint and
must not be staged or overwritten:

- `parsing/fields.go`
- `parsing/fields_test.go`
- `wallet/grpc/funding.go`
- `wallet/grpc/helpers.go`
- `wallet/grpc/helpers_test.go`

`noebs-fly-litefs.conf` contained a tracked Fly WireGuard private key and is
deleted in this tree. Treat that historical key as compromised. Revoking the
peer or rewriting repository history requires separate explicit approval.
No peer-revocation confirmation has been provided, so that security disposition
remains open.
