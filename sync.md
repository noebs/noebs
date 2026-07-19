# Keycloak cutover coordination

Last updated: 2026-07-19

This file records evidence, decisions, ownership, and verification for the
Keycloak cutover. Repository code and locally reproduced results are the
authority. GitHub checks and pull-request state are not release evidence.

## Main-agent verdict

The current deployment is not Keycloak-backed. Keycloak 26.6.1 is running with
only the `master` realm, while the API gateway authenticates users with a
shared HS256 secret and administrators with a shared key or Basic credentials.
The static admin credential can select arbitrary tenants, admin actor IDs come
from request forms, and the default-deny NetworkPolicies do not permit any
Noebs workload or the public edge to reach Keycloak.

This is a replacement, not a dual-auth migration. The completed cutover must
have no HS256 user-token path, static admin credential, local password/OTP
authority, or request-supplied audit actor fallback.

## Accepted design

- One `noebs` realm represents human identities. One subject may belong to
  several tenants.
- Each tenant is a Keycloak Organization. The live first organization alias is
  `tenant-cutover`; more aliases use the same declarative structure.
- The API is a separate `noebs-api` resource-server client. Access tokens must
  have the exact public issuer, the `noebs-api` audience, an allowed authorized
  party, an access-token type, RS256 signature, subject, issue time, and expiry.
- A request selects an active tenant explicitly. Authorization uses only the
  selected entry in the Keycloak `organization` claim; roles from different
  organizations are never combined.
- Organization-scoped roles are exactly `user`, `backoffice`, and
  `tenant-admin`. The realm-level `platform-admin` role is reserved for
  genuinely cross-tenant platform operations.
- The immutable human identity is `(issuer, subject)`. Numeric local user IDs
  may remain only as a domain projection. Credentials never return to the
  application database.
- The gateway derives tenant, role, subject, and any projected user ID. It
  clears public identity headers and carries the decision through the existing
  signed workload boundary. Downstream services never interpret bearer tokens.
- Back-office reporting requires `backoffice` or stronger membership. Wallet
  mutation and approval routes require `tenant-admin` or `platform-admin` plus
  exact route permissions. Actor identity is always the verified subject.
- Browser back-office authentication is an in-process API-gateway BFF, not an
  identity-aware proxy. It uses Authorization Code with PKCE S256, a
  confidential Keycloak client, one-time flow state, and opaque shared
  PostgreSQL sessions; OAuth tokens never enter the browser or downstream
  services.
- Back-office tenant context is part of the canonical path
  `/backoffice/t/:tenant/...` and is re-authorized against the current token on
  every request. There is no session-global active tenant and no tenant query,
  form, header, or default fallback. Unsafe forms also require a per-session
  synchronizer token and same-origin evidence.
- Keycloak realm, clients, scopes/mappers, organizations, groups, and role
  mappings are reconciled idempotently from repository-owned desired state.
  Startup import alone is insufficient because it skips an existing realm.
- The canonical public Keycloak path is under the already trusted
  `https://api.noebs.sd/auth` edge until a separately managed auth DNS record
  exists. Keycloak uses a fixed hostname; `hostname-strict=false` is removed.

## Review evidence

### Authentication and authorization review

- `apigateway/jwt_auth.go` issues ten-hour shared-secret tokens.
- `apigateway/admin_auth.go` accepts a shared key, Basic auth, and a debug
  bypass.
- `cli/gateway_proxy.go` hard-codes the internal admin role and accepts the
  tenant from public headers/query/form values.
- `wallet/grpc/admin_render.go` accepts `requested_by`, `set_by`, and
  `approver_id` from forms instead of the authenticated actor.
- `wallet_ledger/008_wallet_admin_audit.sql` stores a second administrator
  password/TOTP authority.
- `/generate_api_key` creates credentials that no incoming auth path consumes.
- The strict verifier and gateway adapter are now committed in `c498bb8` and
  `563fe0b`. They reject duplicate/bare credentials, non-RS256 tokens, invalid
  issuer/audience/client/type/time claims, and organization role union before
  producing a tenant-scoped principal.
- Internal TLS peer authorization is committed in `fa1cb40`. Wallet-ledger now
  accepts only the `wallet-api` certificate identity rather than every client
  certificate issued by the Noebs CA.

### IaC and live-host review

- The in-cluster Keycloak database contains only realm `master`; no Noebs
  organization or client exists.
- `deploy/kubernetes/base/network-policies.yaml` permits Keycloak to reach only
  its PostgreSQL instance. There is no public Keycloak route.
- The live Noebs Argo application is pinned to stale revision `34acb6ce...`,
  workloads use mutable image references, and the edge Caddy deployment is not
  owned by an Argo application.
- K3s secret encryption at rest is disabled. The cluster-admin kubeconfig,
  OpenTofu state, and its backup are mode `0644`; the state contains release
  secret values. Permissions/encryption must be fixed and all affected secrets
  rotated before calling the platform secure.

### Host hardening evidence

- A stopped-restart-policy Testcontainers PostgreSQL container had published a
  random port on the public interface. Container `8abb4599cdcc` and its
  anonymous data volume were removed after confirming their Testcontainers
  labels and creation metadata.
- `deploy/host/noebs-public-docker-firewall` now rejects new traffic forwarded
  from `eth0` to any Docker default or user-defined bridge instead of relying on
  a list of known ports. The installed systemd unit reapplies the policy after
  Docker restarts.
- From outside the server, ports 3100, 9100, 18081, and the orphaned 33001 no
  longer connect. Port 9100 remains reachable through Tailscale and on the host,
  and the public HTTPS edge still responds.
- The live K3s cluster-admin kubeconfig, OpenTofu state and backup, tfvars, and
  saved plan files are now mode `0600`; the node remained Ready after the
  change. Secret encryption and credential rotation are still outstanding.
- The node runs K3s v1.35.4+k3s1, new enough for the supported
  existing-cluster encryption transition. Its install-time systemd arguments
  still specify `write-kubeconfig-mode=644`, so a restart would undo the manual
  permission fix. Repository-owned K3s config now replaces those arguments,
  keeps mode `0600`, and selects the `secretbox` provider; it has not yet been
  installed or activated.

### Server test evidence

- Commit `2d4d508` was checked out as an isolated detached worktree on
  `100.102.164.34` and tested with Go 1.26.5. CLI tests and race-enabled OIDC,
  tenant-policy, and gateway tests passed.
- On the server's four-vCPU test allocation, warm token verification measured
  about 92.6 microseconds per operation and tenant policy about 121 nanoseconds
  with zero policy allocations. Two Ryuk helpers left by the test process were
  removed; no Testcontainers resources remain.

## Work streams

| Stream | Owner | State | Exit evidence |
|---|---|---|---|
| OIDC verification and tenant policy | quality reviewer | complete | race tests; rotation/adversarial matrix; 26.4 us verification and 23 ns policy benchmarks |
| Gateway/runtime cutover | main agent | pending | no static/HS auth routes; route policy matrix passes |
| Keycloak desired state and reconciler | IaC reviewer | pending | empty-state and idempotent reconcile tests |
| Principal projection and actor propagation | auth reviewer | pending | `(issuer, sub)` uniqueness and spoof tests |
| Back-office OAuth BFF | quality reviewer | active | flow replay, refresh locking, CSRF, tenant-tab isolation, and hot-path benchmark |
| Host hardening and release path | main agent | active | local build, immutable server deploy, live negative/positive smoke |

## Release gates

1. Local unit, integration, race, vet, vulnerability, manifest, OpenTofu, and
   benchmark checks pass without relying on GitHub automation.
2. A clean Keycloak/PostgreSQL state reconciles twice without drift and emits
   tokens whose organization-specific roles pass the gateway matrix.
3. Static admin, Basic, HS256, legacy login/refresh/OTP/recovery/Google, local
   admin password/TOTP, and unused API-key paths are absent.
4. Cross-tenant, cross-client, role-union, direct-backend, header-spoof, actor
   spoof, and maker-approver self-approval cases fail.
5. A scoped commit is pushed, built locally into an immutable image, deployed
   to `100.102.164.34`, and verified against the exact source revision.
6. Server filesystem permissions and secret storage are hardened, exposed
   release credentials are rotated, and no secret values enter Git history or
   command output.
