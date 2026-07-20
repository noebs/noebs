# Noebs architecture

Status: current implementation. Repository code is the design authority.
Rendered manifests, live Kubernetes/OpenTofu state, and locally reproduced
results are deployment evidence and must be reconciled when they differ from
the code. There is no supported monolith role or legacy authentication
fallback.

## Runtime shape

Noebs builds one Go image, but every process starts exactly one configured
service role. Caddy exposes the public HTTPS edge. Public API and back-office
traffic enters `api-gateway`; the edge exposes only the Keycloak endpoints used
by Authorization Code, PKCE, Google brokering, and login-theme assets. Keycloak
administration, account management, client registration, and unused protocols
remain private. Other workloads use ClusterIP services and are not public entry
points.

HTTP service discovery, gRPC service discovery, Kafka brokers, and Temporal
addresses are explicit mounted configuration. A service does not discover a
peer or database by guessing a hostname, tenant, or default identifier.

## Human identity and tenant authorization

Keycloak is the sole human credential and membership authority. The `noebs`
realm contains the public `noebs-mobile` client, confidential
`noebs-backoffice` BFF client, and confidential `noebs-wallet-authorizer`
transaction-authorization client. `noebs-api` is the resource-server audience.
Google is a Keycloak identity provider; the application has no separate
Google-login path.

Each tenant is a Keycloak Organization. Organization membership groups grant
the tenant roles `user`, `backoffice`, and `tenant-admin` and their explicit
route permissions. One subject may belong to several Organizations and hold a
different role set in each. No realm role grants implicit tenant access.

Mobile and API requests carry one Keycloak access token and one canonical
`X-Active-Tenant` header. The gateway accepts only a single bearer credential,
verifies the RS256 signature and exact issuer, audience, authorized party,
token type, subject, and time claims, then authorizes only the selected
organization entry. Roles from different organizations are never combined.
There is no tenant query parameter, configured tenant fallback, HS256 path, or
application-issued user token.

The stable human identifier is `(issuer, subject)`. `identity-auth` stores only
a tenant-scoped domain projection: numeric user ID, profile fields, KYC, and
passport data. It stores no password, OTP secret, refresh token, API key, or
external-provider credential. Resolution never creates a missing projection;
profile creation is an explicit operation.

Browser back-office authentication is an API-gateway BFF using Authorization
Code with PKCE S256 and the confidential Keycloak client. OAuth tokens and
refresh tokens remain server-side in the `gateway_auth` database behind an
opaque secure session cookie. Tenant context is the canonical
`/backoffice/t/:tenant/...` path and is re-authorized for every request. Unsafe
operations also require same-origin evidence and a session-bound CSRF token.

Protected wallet writes use a separate Authorization Code with PKCE flow at
Keycloak LoA2. The gateway binds the callback to the exact tenant, subject,
operation, canonical request digest, idempotency key, and freshly authenticated
session, then issues a one-use authorization consumed atomically by the wallet
route. It cannot authorize a different request or be replayed after success.

The `/ws` chat connection follows the same mobile bearer-token and active-tenant
boundary. After authorization, the gateway proxies the upgrade to
`notification-chat` with the derived principal and workload signature; the
notification service does not parse the bearer token or derive identity from a
mobile claim.

## Internal trust boundary

The gateway removes public credentials and caller-supplied internal identity
headers before proxying. It propagates the selected tenant, issuer, subject,
organization, roles, permission, token expiry, source address, and projected
user ID as a signed downstream principal.

Internal HTTP requests use the `NOEBS-WORKLOAD-V2` Ed25519 signature. The
signature binds caller key, audience, method, target, body digest, request ID,
identity context, timestamp, and nonce. Receivers enforce an exact caller and
route capability matrix and atomically consume nonces in the shared
`workload_auth` database. Replays, unknown callers, wrong audiences, stale
requests, and body changes fail closed. Internal transport also uses
service-specific TLS identities; wallet gRPC accepts only authorized peers and
principal metadata produced at the gateway boundary.

Downstream services never authenticate a human bearer token and never trust a
public `X-Noebs-*`, tenant, admin, role, or actor header. Audit actors come from
the verified subject, not request bodies or form fields.

PSP providers call `/psp/webhooks/:callback_id`, where `callback_id` is an
explicit opaque 32-byte identifier unique to one catalog tenant and provider.
The gateway resolves that mapping, rejects query-based routing, preserves the
raw provider payload and signature, and signs the resolved tenant identity for
the downstream webhook service. Tenant, provider, region, currency, and
direction are never selected from public query parameters.

## Service and data ownership

| Component | Responsibility | Persistent owner |
| --- | --- | --- |
| Keycloak | Credentials, federation, Organizations, memberships, roles, client sessions | Dedicated Keycloak PostgreSQL |
| `api-gateway` / BFF | Public routes, OIDC verification, tenant policy, back-office OAuth/session boundary, principal projection, workload signing | `gateway_auth` for opaque BFF state only |
| `identity-auth` | Explicit profile projection keyed by tenant, issuer, and subject; KYC/passport profile data | `identity_auth` |
| `card-vault` | Opaque card enrollment, encrypted PAN/IPIN material, funded-operation claims | `card_vault` |
| `ebs-adapter` / `ebs-adapter-events` | EBS protocol integration, rail transactions, outbox publication | `ebs_adapter` |
| `psp-webhook` | Signed PSP webhook ingress and Temporal signaling | `wallet_ledger`, through `wallet_ledger_webhook` |
| `wallet-api` | Public and back-office wallet HTTP adapter | None |
| `wallet-ledger` / `wallet-worker` | Wallets, double-entry ledger, holds, fees, rates, limits, PSP configuration and transactions, funding, withdrawal, and Temporal workflows | `wallet_ledger` |
| `admin-reporting` / projector | Read-only operational projections and reports | `admin_reporting` |
| `notification-chat` | WebSocket delivery, internal notification event recording, and biller callbacks | `notification_chat` |
| Workload-auth verifier/cleanup | Internal-request replay prevention | `workload_auth` |

Kafka carries EBS transaction events into the reporting projector. Temporal
owns durable wallet orchestration. Neither mechanism grants data ownership to
the consumer: the service listed above remains the only writer of its model.

Each owned database has one explicit Goose migration scope and migration Job.
The PSP HTTP process is an ingress role inside the wallet aggregate, so its
tables are owned by `wallet_ledger_migrate` and it has no separate migration
scope. Runtime, migration, and cleanup credentials are separate where the role
requires them. Stores accept explicit tenant, user, currency, and resource
identifiers; only API handlers may apply configured defaults.

## Infrastructure and release ownership

`deploy/kubernetes/base` owns workloads, services, migration/reconcile Jobs,
service accounts, and network policy. The current-host Kustomize overlay owns
environment-specific digests, resources, routes, and patches.
`foundation/terraform` owns the host foundation and Argo CD Applications. SOPS
material is split by service and decrypted only into the intended workload or
Job.

Keycloak realm, clients, scopes, organization mappings, identity providers,
organizations, groups, roles, and permissions are declared in
`deploy/kubernetes/keycloak-authority/keycloak-desired-state.yaml` and reconciled
idempotently. A temporary master-realm service account exists only in the
bootstrap overlay and is deleted after the realm-local reconciler is usable.

Release evidence is local. `scripts/publish-alpha-image.sh` exports one reviewed
commit, publishes a write-once full-SHA image tag, verifies the registry
manifest digest, and writes an immutable receipt. A separate GitOps commit pins
that digest. Argo CD reconciles the commit; GitHub Actions and mutable image
tags are not test or release authorities.

## Required invariants

- No local human credential tables, static admin keys, Basic admin login, or
  application JWT issuer.
- No public route reaches a downstream workload without gateway authorization
  and the exact internal workload capability.
- No tenant, role, actor, user ID, currency, or database owner is inferred in a
  lower layer.
- No service reads or writes another service's schema.
- No mutable image tag enters a deployed workload.
- Keycloak reconciliation is drift-free on a second pass, migrations complete
  before runtime rollout, and post-deploy smoke verifies the exact Git revision
  and image digest.
