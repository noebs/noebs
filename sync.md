# Keycloak authority cutover verdict

Last updated: 2026-07-20

The repository, immutable image digest, local test output, and observed server
state are authority. GitHub checks and pull-request state were not used.

## Verdict

GO for the clean-slate Keycloak-backed alpha platform and its IaC deployment.

Implementation authority is commit
`045df171eb63046df2da252bfeb9ae0d1b2bb87b`. The runtime image was built from
`a32a671b4bbb0cd7d651ea6a961c72242f73b50c` and is pinned everywhere as:

`ghcr.io/noebs/noebs@sha256:b63099c0122fcdd48ca0ca64fd771cce45e9fcd2ea3117603d44552959148ade`

The documentation commit containing this verdict is a source-identical child
of the implementation authority. Its exact GitOps revision is reported by the
final local handoff and remains directly observable in both Argo Applications.

The only acceptance step that cannot be completed unattended is a real
user-controlled Google login followed by TOTP enrollment/challenge. The live
authorization request is proven to redirect to the Google broker and then to
`accounts.google.com` with the exact callback, with no local username or
password form. A human browser session is still required to prove Google's
credential ceremony and the authenticator interaction end to end.

## Human and tenant authority

- Keycloak is the only human credential authority. Repository-owned wallet
  PIN, wallet TOTP, password, recovery, direct-grant, Basic-auth, static API-key,
  and HS256 human-token paths were removed rather than retained as fallbacks.
- Google federation supplies LoA1. Keycloak TOTP supplies LoA2 with HMAC-SHA256,
  six digits, a 30-second period, look-ahead one, and non-reusable codes.
- Wallet-sensitive operations use a fresh LoA2 ceremony with Max Age zero and
  one immutable, expiring, one-use intent bound to tenant, subject, operation,
  normalized request digest, and idempotency key. The intent is atomically
  claimed before any PSP mutation or Temporal workflow start.
- `tenant-cutover` and `tenant-sandbox` are exact Keycloak Organizations.
  Each contains exactly `user`, `backoffice`, and `tenant-admin` groups. Group
  client-role mappings are repository-owned and undeclared topology is pruned.
- One subject can hold a different class in each tenant. Each request selects
  one active tenant, and roles or permissions from other organizations are not
  unioned into that request.
- The API gateway is the sole bearer verifier. It verifies RS256, issuer,
  audience, authorized party, token type, key ID, lifetime, ACR where required,
  and the selected organization. Downstream services receive only the signed
  V2 workload envelope over authenticated internal transport.
- Back-office sessions are opaque PostgreSQL records with encrypted token
  material, PKCE, CSRF, origin checks, refresh locking, nonce validation, and
  RP-initiated logout.

## Live Keycloak evidence

- Fresh bootstrap completed, then the temporary master-realm client was
  deleted. A CA-verified TLS 1.3 token request using its retired credential
  returned `401`.
- Bootstrap-only Secrets `keycloak-bootstrap-admin` and
  `keycloak-bootstrap-reconciler-credentials` are absent. Steady reconciler,
  Keycloak transport, and Keycloak server Secrets remain present.
- A fresh steady-client reconciliation against the live Admin API reported
  exactly `created=0 updated=0 deleted=0`.
- A disabled, credentialless probe subject was assigned through the production
  operator Job as `user` in `tenant-cutover` and `backoffice` in
  `tenant-sandbox`. Dry-run planned two adds, apply verified two adds, and the
  second apply reported `actions=0`.
- The same subject was reconciled to an explicit empty membership sequence.
  Apply verified two removals and the second empty apply reported `actions=0`.
  The subject, operation Job, operation Secret, and temporary files were then
  deleted; exact-email lookup returned zero users.
- The operator Job initially exposed a real CNI policy-programming race. The
  membership and lookup Jobs now gate on CA-verified Keycloak availability;
  the corrected live membership Job completed its init gate with exit `0` and
  its main container with zero restarts.

## Deployment and data authority

- K3s `v1.35.4` Secret encryption is enabled with `secretbox`; the final
  rotation state is `reencrypt_finished` with key hash match.
- The encrypted pre-encryption recovery archive is
  `/home/adonese/.local/state/noebs/backups/k3s-before-secret-encryption-20260720T160205Z.tar.sops`,
  SHA-256
  `370e54770586dc3d17a67d4fd490fd790be3fc3d1c688b37b1222ce9563a0f44`.
- The encrypted legacy foundation-state archive is
  `/home/adonese/.local/state/noebs/backups/foundation-legacy-state-20260720T161200Z.tar.sops`,
  SHA-256
  `1e8544f0cc2204ded3b8c95fbf7d45af5b85e9345b8cd7c2840e796dd270c6d2`.
- OpenTofu owns the `noebs` and `noebs-edge` Argo Applications, namespaces, and
  immutable Git revisions. Its state contains no Kubernetes Secret values.
  The final plan is zero-diff.
- Strict SOPS inputs preserved only the external Google OAuth and EBS
  credentials; every internal password, client secret, signing key, TLS leaf,
  and transport identity was rotated. Decryption and rendering happened in
  tmpfs. The release age key, rendered Secrets, registry config, plans, and
  probe material were removed after deployment.
- The retired age identity in the old ext4 checkout was unlinked. This removes
  the live file but is not a claim of forensic erasure from SSD history.
- PostgreSQL has exactly 22 service/migration roles and 8 service databases.
  Every migration table is exactly `0:true,1:true`; the authority marker is
  current and topology drift count is zero.
- PSP configuration is intentionally inactive: live `psp_configs` and
  `psp_config_overrides` row counts are both zero. No provider fallback exists.

## Live workload evidence

- Both Argo Applications are `Synced` and `Healthy` at one immutable revision.
- Every active Noebs, Keycloak, PostgreSQL, Kafka, Temporal, wallet, and edge
  container is ready with restart count zero.
- Temporal gates on both its PostgreSQL authority and CA-verified Keycloak
  JWKS availability. Wallet API gates on PostgreSQL because workload-auth nonce
  storage is a real database dependency even though the role owns no service
  database.
- Keycloak cluster transport is restricted to its documented TCP ports `7800`
  and `57800`. Application traffic is TLS 1.3 on `8443`; the management listener
  remains pod-local to kubelet probes.
- Caddy is Argo-owned, pinned by digest, runs as UID/GID `10001`, has a
  read-only root filesystem, and uses the prepared client identity for mTLS to
  the gateway. Its state directories are owned by `10001:10001` with `0700`
  roots. The obsolete unhashed ConfigMap is absent.
- `https://rd.adonese.sd/`, Keycloak discovery, JWKS, and Android asset links
  return `200`. RD remained healthy throughout the Noebs-only replacement and
  edge adoption.
- The edge exposes only the required `noebs` discovery/JWKS, authorization,
  token, logout, Google broker, and required-action routes. Admin, master realm,
  registration, SAML, UMA, account, userinfo, introspection, revocation, device,
  PAR, CIBA, password-reset, and broker-link/token surfaces return `404`.

## Reproduced test evidence

At implementation parent `29bbc7c6f8270f480aec45db7ff62d799b188ecd`,
the detached server checkout passed:

- `go test -p=1 ./... -count=1 -timeout=30m`, including real PostgreSQL and
  Temporal integration suites;
- `go vet -p=1 ./...` and `go mod tidy -diff`;
- race-enabled gateway, back-office, Keycloak admin, OIDC, tenant policy,
  transaction authorization, and workload authorization packages;
- the local release-image invariant script and `git diff --check`.

The exact implementation commit `045df171eb63046df2da252bfeb9ae0d1b2bb87b`
changes only the two operator Job manifests and their contract test. On the
server it passed `go test ./cli -count=1` and all nine Kustomize entry points
with Kustomize `v5.7.1`; the corrected Job then passed the live membership
round trip described above.

The final post-deploy smoke passed the exact Git revision and image digest,
both Argo Applications, all rollouts, zero restarts, resource budgets, image
IDs, 22 roles, 8 databases, every migration set, public `/test` and
`/app/config`, discovery/JWKS, Android asset links, Google-only authorization
redirects, and the complete public 404 deny contract.

Five-run benchmark evidence, without a checked-in regression threshold:

- keyring seal/open: median `4,005 ns/op`, `11,728 B`, 9 allocations;
- CSRF validation: `209.2 ns/op`, `256 B`, 6 allocations;
- in-memory session authentication: `2,576 ns/op`, `3,048 B`, 25 allocations;
- warm tenant authorization: `38.88 ns/op`, zero allocations;
- warm bearer verification: median `26,069 ns/op`, `8,616 B`, 108 allocations;
- cached workload capabilities: `49.22 ns/op`, `64 B`, 2 allocations.

Three requested final review agents were started for internal TLS, edge mTLS,
and performance review, but the platform rejected each with its usage-limit
error. No GitHub review or CI fallback was used. The main agent therefore
reproduced the relevant local/server checks and recorded this limitation rather
than inventing an independent verdict.

## Feedback-loop fixes

The local/server loop found and corrected these release defects:

- password-file newline drift in mounted-secret fingerprints;
- an unreadable entrypoint mode caused by the publisher's restrictive umask;
- Keycloak hook startup before verified HTTPS readiness;
- missing exact Keycloak cluster transport ports;
- PostgreSQL consumer startup before CNI policy programming;
- Temporal startup before both PostgreSQL and Keycloak were reachable;
- wallet API's previously unmodeled workload-auth database dependency;
- membership and lookup operator startup before Keycloak policy readiness.

One raw NetworkPolicy apply accidentally targeted `default` because base
manifests rely on Kustomize namespace injection. All 33 objects had the same
new timestamp, were removed immediately, and `default` was verified to contain
zero policies and zero pods. The committed policies were then applied to
`noebs`; RD remained healthy.

## Workspace protection

These pre-existing user edits remain unstaged and were not altered by the
cutover commits:

- `parsing/fields.go`
- `parsing/fields_test.go`
- `wallet/grpc/funding.go`
- `wallet/grpc/helpers.go`
- `wallet/grpc/helpers_test.go`
