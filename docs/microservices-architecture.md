# noebs Microservices Architecture Plan

Status: reviewed foundation, microservices-only runtime.
Deployment target: existing host `100.102.164.34`.

## Current Shape

The pre-split runtime was a single Go binary built from `./cli`. `cli/config.go` initialized the database, migrations, Fiber routes, auth, consumer handlers, merchant handlers, dashboard, chat hub, wallet HTTP handlers, wallet gRPC server, PSP webhook handling, and the Temporal wallet worker in one process.

The target runtime is microservices-only. There is no supported monolith runtime role, no mixed monolith/microservice deployment mode, and no silent fallback to an all-in-one process. The same image can be reused while packages are being extracted, but every process must declare a single service role and own only that role's process lifecycle. If a split exposes a bug, we fix forward.

The public edge is the `api-gateway` service. Ingress and local Caddy route public hosts only to `api-gateway`; downstream HTTP services are reached through mounted `noebs.service_discovery` endpoints and ClusterIP service names. Internal gRPC clients use mounted `noebs.grpc_service_discovery` host:port entries.

The code already has useful extraction points:

- `apigateway`: Fiber middleware for request IDs, CORS, auth enforcement, metrics, and logging.
- `consumer/handler` and `merchant/handler`: legacy public REST surfaces.
- `wallet/grpc` plus `proto/noebs/wallet/v1/wallet.proto`: existing wallet gRPC contract.
- `wallet/worker` and `wallet/workflow`: Temporal workflow/worker package.
- `wallet/psp` and `wallet/handler/psp_webhook.go`: PSP config, mapping, verification, and webhook handling.
- `dashboard`: admin/reporting views.
- `store` and `wallet/store`: Postgres access and scoped migrations, with tenant/currency validation already asserted by tests.

## Service Boundaries

### API Gateway/BFF

Owns public HTTP shape, Fiber edge routing, request IDs, CORS, auth enforcement, public metrics, request logs, and compatibility routing for legacy clients. It must not own payment state, open a service database, or accept `noebs.db_url`. It calls internal services over gRPC/HTTP using config-driven service discovery and maps their typed errors to the public REST contract. Gateway-local health is `GET /test`; legacy consumer test endpoints such as `POST /consumer/test` are not part of the gateway surface.

Initial package owner: `apigateway`, route composition from `cli`.

### Identity/Auth Service

Owns users, mobile auth, Google OAuth linking, JWT issuance, auth accounts, profiles, API keys, and device identity. Tenancy should stay here initially as tenant identity/configuration, not as a separate service, until tenant lifecycle needs its own API and operators.

Initial package owner: existing user/auth code in `consumer` plus `store` user/auth tables. The first split owns login, registration, OTP generation/login/verification, Google OAuth, profile, language, password, API-key generation, KYC/check-user, recovery JWT issuance, and device-token update routes. `check_user` owns user existence checks in identity-auth but obtains masked PAN values through a card-vault command rather than reading card tables or identity-owned card columns. Completed card registration, `register_with_card`, and account-recovery balance commands into identity-auth carry only identity fields; PAN and expiry are persisted only through card-vault. User creation persists identity-auth state only; it does not emit hard-coded external sync or backup calls. Identity-auth exposes one public OTP generation route; the legacy `/consumer/otp/generate_insecure` route is not part of the service or gateway surface.

### Keycloak Service

Owns the future external identity provider runtime. It is deployed as an independent service with its own Postgres database and mounted `keycloak.conf` secret, but no noebs auth data path is wired to it yet.

Initial package owner: official Keycloak container. The first split only makes the service deployable and discoverable inside the cluster.

### Consumer Beneficiary Service

Owns consumer beneficiary/contact payment targets at the public compatibility path level. It keeps beneficiary CRUD out of the API Gateway while the legacy consumer package is being carved into explicit owners.

Initial package owner: beneficiary methods in `consumer` and `store`. The first split owns `POST /consumer/beneficiary`, `GET /consumer/beneficiary`, and `DELETE /consumer/beneficiary` using the gateway-propagated user ID rather than identity-owned `users` rows.

### Card/Vault Service

Owns PAN/IPIN/card storage, encryption, tokenization, fingerprints, card lookup, and mobile-to-card mappings. Only this service can read decrypted card data. Other services receive tokenized references or last-four metadata.

Initial package owner: card/token functions currently in `consumer` and `store`. The first split owns card CRUD, main-card selection, payment-token storage, payment-request token creation, internal quick-pay token resolution/paid-marking commands using the gateway-propagated user ID rather than identity-owned `users` rows, internal completed-registration card persistence with explicit mobile/user/card inputs, and mobile-to-card lookup from card-vault owned mappings. Card-vault handlers must not use identity-owned user-table joins; public card mutations use gateway user IDs. Mobile-to-PAN lookup is internal-only for service commands and is not exposed as an authenticated public compatibility route.

### EBS Adapter Service

Owns EBS HTTP protocol details, merchant and consumer EBS calls, IPIN protocol calls, retry policy, circuit breakers, EBS endpoint selection, and raw EBS interaction logs. Merchant and consumer APIs become compatibility clients of this adapter rather than owners of EBS protocol logic.

Initial package owner: `ebs_fields`, `consumer/*_service.go`, `merchant/*`. The first split owns merchant EBS passthrough, merchant EBS operations, consumer EBS operations, QR/IPIN calls, voucher generation, EBS card-info/PAN lookup/card-registration-start/card-registration-completion routes, `register_with_card` and account-recovery balance EBS validation, NEC lookup, EBS transaction lookups, mobile-transfer compatibility routes, and quick-pay execution through card-vault token commands. Card-registration completion, `register_with_card`, and account-recovery balance persist service-owned state through explicit identity-auth and card-vault internal commands after the EBS validation succeeds. Mobile-transfer resolves recipient PAN through card-vault rather than reading identity/card tables. Transaction history reads card-vault masked-card projections for the gateway user ID and queries only EBS-owned transaction rows. EBS transaction recording writes only the EBS adapter store; payment-facing flows do not synchronously command `admin-reporting` and do not require admin-reporting service discovery. Quick-pay submits biller callback delivery through `notification-chat`; the EBS adapter does not use in-process callback channels. The shared EBS HTTP client does not publish cache-card state through package-global channels; card/cache persistence must be explicit service commands. Dynamic fees are explicit merged config under `noebs.ebs_dynamic_fees`; the adapter must not hard-code runtime fee defaults.

### Wallet/Ledger Service

Owns wallets, balances, holds, double-entry posting, fees, limits, rates, funding sources, withdrawal destinations, wallet PIN/2FA state, and wallet gRPC APIs. Wallet and ledger stay together because balance correctness depends on local transactional invariants.

Initial package owner: `wallet`, `wallet/store`, `wallet/grpc`, `proto/noebs/wallet/v1`. Public `/wallet` and operational `/admin/wallet` HTTP routes run in `wallet-api` and call `wallet-ledger` through `noebs.grpc_service_discovery.wallet-ledger`. `wallet-api` does not open the wallet-ledger database.

### Wallet Worker Service

Owns Temporal workers and scheduled wallet workflows. It uses the same wallet codebase at first, but runs as a separate process with no public HTTP listener. It executes PSP polling, reconciliation, P2P, deposit, withdrawal, and manual-transfer workflows.

Initial package owner: `wallet/worker`, `wallet/workflow`, `wallet/activity`.

Startup invariants: wallet-worker requires Temporal config, wallet PSP dependencies, an explicit task queue, and tenant rows from its service database before scheduling PSP polling or reconciliation workflows. It does not fall back to `default_tenant_id` when tenant discovery is empty.

### PSP/Webhook Service

Owns PSP config loading, webhook signature verification, PSP request/response mapping, idempotent webhook persistence, and workflow signaling. It must not post ledger entries directly; successful webhooks signal wallet workflows.

Initial package owner: `wallet/psp`, PSP rows in `wallet/store`, and `wallet/handler/psp_webhook.go`. The runtime initializes a PSP/webhook store directly against the `psp-webhook` database; it does not initialize `wallet.Service` or open wallet-ledger state. Webhooks require an explicit `tenant_id` query parameter, and provider payload fields are read only through configured PSP response mappings. The handler does not infer tenant, client reference, status, currency, direction, or messages from ad hoc payload aliases.

### Notification/Chat Service

Owns websocket hub, persisted push data, biller callback delivery, notification projections, and push fanout. It consumes domain events from wallet/EBS/auth rather than reading write models directly.

Initial package owner: current `github.com/tutipay/ws` integration plus notification methods in `consumer`. The first split owns `/ws`, `GET /consumer/notifications`, `POST /consumer/submit_contacts`, notification push-data commands, and biller callback delivery commands. Notification reads use the gateway-provided mobile claim against `notification-chat` owned `push_data`; they do not open identity/auth user tables to confirm the mobile.

### Admin/Reporting Service

Owns read-only dashboards, settlement reports, issue reports, and operational projections. It consumes events or reporting tables. It must not own payment writes or block payment-facing EBS calls.

Initial package owner: `dashboard` plus wallet admin templates as a transitional read surface.

Admin/reporting requires an explicit tenant at the HTTP boundary. Dashboard service helpers require an explicit tenant from request headers or request locals and do not fall back to service config.

### Frontend

The first split should keep frontend delivery behind the API Gateway/BFF. Admin UI can later move into a separate static frontend served by the gateway or an object store/CDN.

## Invariants

- Tenant ID is required at the API boundary and must be explicit for every service call.
- Currency is required at the API boundary for money movement and must be explicit below handlers.
- JWT tenant/user claims bind to request tenant/user fields; a mismatch is an auth failure.
- Store and service layers return typed validation errors for missing tenant, missing currency, invalid user ID, invalid wallet ID, and missing idempotency keys.
- The Card/Vault service is the only owner of PAN/IPIN decryption.
- The Wallet/Ledger service is the only owner of wallet balances and double-entry writes.
- Wallet writes are idempotent by caller-provided idempotency key.
- PSP webhooks must be verified or explicitly authorized by provider config before they can signal workflows.
- EBS calls are logged with tenant, service, request reference, response code, and raw adapter metadata.
- Migrations run only as Kubernetes/k3s `Job` deployment steps, not from service replicas.
- Each applicable service owns its database and its migration scope. A service must not rely on another service's schema in the same process.

## Deployment Shape

Kubernetes provides service discovery through ClusterIP services:

- `api-gateway.noebs.svc.cluster.local:8080`
- `identity-auth.noebs.svc.cluster.local:8080`
- `keycloak.noebs.svc.cluster.local:8080`
- `card-vault.noebs.svc.cluster.local:8080`
- `ebs-adapter.noebs.svc.cluster.local:8080`
- `psp-webhook.noebs.svc.cluster.local:8080`
- `admin-reporting.noebs.svc.cluster.local:8080`
- `notification-chat.noebs.svc.cluster.local:8080`
- `consumer-beneficiary.noebs.svc.cluster.local:8080`
- `wallet-api.noebs.svc.cluster.local:8080`
- `wallet-ledger.noebs.svc.cluster.local:9090`
- `wallet-worker` has no service; it is a worker deployment.
- `temporal-frontend.noebs.svc.cluster.local:7233`
- `keycloak-postgres.noebs.svc.cluster.local:5432`
- noebs service-owned Postgres databases, addressed through each service's mounted `secrets.yaml`.
- Keycloak's Postgres database, addressed through the mounted `keycloak.conf` secret.

Argo CD owns application sync from `deploy/kubernetes/overlays/current-host`. The public Ingress only targets `api-gateway`; `api-gateway` proxies public compatibility routes to the ClusterIP service catalog. OpenTofu under `foundation/terraform` owns platform installation, the `noebs` namespace, service-discovery outputs, and the Argo CD application definition. Standalone Argo CD application manifests under `deploy/` are not a supported deployment path. Runtime secrets remain outside Git as service-owned SOPS material rendered into Kubernetes Secrets.

`noebs-deployment-preflight` runs as an Argo CD `Sync` hook at sync wave 0 before Temporal schema and Noebs service migration jobs. It mounts the same Kubernetes ConfigMap and Secret keys as the runtime workloads, then runs `noebs validate-kubernetes-deployment /preflight` to reject missing files, placeholders, reserved tenant IDs, legacy `noebs.db_path`, incomplete service-owned database URLs, incomplete EBS adapter endpoint/app-id/IPIN/key/bill-inquiry inputs, and incomplete Keycloak/Temporal/Postgres platform inputs before any migration runs.

Migrations are deployed through `deploy/kubernetes/base/migrate-job.yaml` as Argo CD `Sync` hooks at sync wave 10, before Noebs runtime Deployments at sync wave 20. Each job has a service-specific role and runs only that service's embedded migration scope. Service Deployments must not run migrations in their startup path.

Service identity and service discovery are config-driven. Each noebs workload mounts the shared `/app/config.yaml` plus a tracked `/app/service.yaml` containing `noebs.service_role` and matching `noebs.otel_service_name`; deployments do not select noebs roles through environment variables. The image does not bake `config.yaml`, and the service entrypoint requires mounted config, service config, secrets, and the SOPS age key before starting. SOPS decryption requires the explicit mounted `noebs.sops_age_key_file` and does not inherit process environment. HTTP route discovery uses an exact `noebs.service_discovery` catalog for `identity-auth`, `keycloak`, `card-vault`, `ebs-adapter`, `psp-webhook`, `admin-reporting`, `notification-chat`, `consumer-beneficiary`, and `wallet-api`; wallet-api reaches wallet-ledger through the exact `noebs.grpc_service_discovery.wallet-ledger` entry. Extra or missing discovery entries fail deployment preflight and runtime startup. Runtime outbound HTTP clients do not consult proxy environment variables; service routing must be explicit in mounted config. Tenant-scoped public identity, EBS, and admin/reporting routes accept `X-Tenant-ID` only at the API gateway, where it is validated and forwarded as `X-Noebs-Tenant-ID`; downstream service handlers and internal command clients trust only gateway-issued tenant locals or headers, never public `X-Tenant-ID`. Database-opening service configs carry `noebs.db_driver`; no-database roles such as `api-gateway` and `wallet-api` do not receive database driver config. Secrets continue to merge through `secrets.yaml`, with service-owned Postgres database URLs supplied by service-specific Kubernetes Secrets. Runtime database config rejects `noebs.db_path`; service databases are Postgres-only. `identity-auth` requires `card-vault` HTTP service discovery for masked PAN lookup in `check_user`. `ebs-adapter` requires explicit resolved EBS `consumer_endpoint`, `merchant_endpoint`, `ipin_endpoint`, `consumer_app_id`, `merchant_app_id`, `ipin_username`, `ipin_password`, `pub_key`, `ipin_key`, `pan`, `pin`, `ipin`, `exp_date`, and positive `ebs_dynamic_fees` values, plus `identity-auth`, `card-vault`, and `notification-chat` HTTP service discovery for its registration, quick-pay, and biller callback command paths. It does not require `admin-reporting` service discovery to serve payment calls. `card-vault` resolves mobile-to-card locally from its owned card mappings and does not require identity-auth service discovery. The runtime does not derive QA or production endpoints from booleans. `wallet-api` uses its own no-database secret; `wallet-worker` uses its own secret with the `wallet-ledger` database owner key because it has no separate database or migration scope. Wallet-ledger owns workflow starts, so it requires Temporal config while wallet-api does not.

Runtime startup initializes only the service objects and route handlers owned by the active role. Identity/card/EBS/notification/beneficiary roles construct consumer compatibility handlers, EBS additionally constructs merchant handlers, admin-reporting initializes dashboard reads, PSP webhook initializes only its PSP store and PSP provider dependencies, wallet-ledger/wallet-worker initialize wallet services, and wallet-worker also initializes PSP provider dependencies from wallet-ledger state.

For local Docker Compose, each Noebs service mounts its own SOPS secret file from `deploy/docker/secrets`. Database-opening service secrets use `noebs.service_databases` keyed only by database owner role. Migration roles use their owning service key, and `wallet-worker` uses the `wallet-ledger` key in `wallet-worker.secrets.yaml`. When that map exists, the runtime requires an owner entry for every database-opening role, copies that URL into `noebs.db_url`, and rejects database entries for no-database or non-owner roles such as `api-gateway`, `wallet-api`, `identity-auth-migrate`, and `wallet-worker`. Compose preflight requires the exact runtime and migration role files under `deploy/docker/services` and rejects extra real `*.secrets.yaml` service secret files under `deploy/docker/secrets` so stale monolith or compatibility inputs cannot be carried next to the service release. `secrets-init` uses the explicit ignored bootstrap secret at `deploy/docker/postgres/bootstrap.secrets.yaml` through `noebs render-db-password` to render the Postgres password file; the root `secrets.yaml` is not a Docker Compose bootstrap contract and is not mounted into Noebs app or migration services. Temporal and Keycloak Compose secret inputs are explicit local-only files ignored by Git, and Temporal's broadcast address is an explicit mounted config value, not a derived hostname or pod address. The default Compose deployment publishes only `api-gateway` on host port `8081`; Temporal, Temporal UI, and Keycloak stay on the Compose network. The Caddy edge proxy is behind the explicit `edge` profile for hosts where no shared reverse proxy already owns ports `80` and `443`. Deployment is Kubernetes/k3s plus Argo CD; direct VM Docker deployment scripts are intentionally not part of the repository.

Kubernetes runtime Secrets are materialized from an explicit Kubernetes release input directory with `noebs render-kubernetes-secrets /path/to/noebs-kubernetes-release noebs /path/to/tls.crt /path/to/tls.key | kubectl apply -f -`. The renderer validates the Kubernetes preflight layout before output: the exact root entries, `.sops/age-key.txt`, the exact platform files under `platform/`, the exact runtime and migration role files under `services/`, and the exact service secret files under `secrets/`. Extra top-level, `.sops`, platform, service config, or service secret entries are rejected so stale monolith or compatibility inputs cannot be ignored. It copies service SOPS files into service-owned Kubernetes Secrets, reads Postgres/Temporal/Keycloak platform passwords from explicit files, includes the SOPS age key, and renders `noebs-tls` from explicit TLS files. Generated Secret manifests are deployment output and must not be committed.

## Migration Plan

1. Add explicit config-selected runtime roles to the current binary: `api-gateway`, `identity-auth`, `card-vault`, `ebs-adapter`, `psp-webhook`, `admin-reporting`, `notification-chat`, `consumer-beneficiary`, `wallet-api`, `wallet-ledger`, and `wallet-worker`.
2. Run database migrations only through Kubernetes/k3s migration Jobs with service-specific roles: `identity-auth-migrate`, `card-vault-migrate`, `ebs-adapter-migrate`, `psp-webhook-migrate`, `admin-reporting-migrate`, `notification-chat-migrate`, `consumer-beneficiary-migrate`, and `wallet-ledger-migrate`.
3. Deploy role-specific Kubernetes workloads with ClusterIP service discovery. No monolith workload is retained.
4. Move PSP webhook traffic into the `psp-webhook` workload. It owns provider verification, request/response mapping, interaction persistence, and Temporal workflow signaling.
5. Move dashboard traffic into the `admin-reporting` workload. It owns read-only dashboard, settlement, merchant-view, status, and stream routes. `GET /dashboard/create` and `POST /dashboard/issues` are not registered by the reporting service.
6. Move notification/chat traffic into the `notification-chat` workload. It owns websocket contacts, notification reads, push-data commands, and biller callback delivery at the public/internal path level.
7. Deploy Keycloak as an independent auth platform service with its own database and config-mounted secret. Do not wire noebs auth data to it until the migration contract is explicit.
8. Move Identity/Auth traffic into the `identity-auth` workload. It owns JWT issuance, OAuth, user/profile, API-key, KYC/check-user, and device-token update routes. `check_user` reads only identity-owned user rows and commands card-vault for masked PAN data. Card registration completion, `register_with_card`, and recovery balance flows create or validate identity rows without PAN or expiry data.
9. Move Card/Vault traffic into the `card-vault` workload. It owns stored card management, payment-token routes, payment-request token creation by gateway user ID, internal quick-pay token resolution/paid-marking commands, internal completed-registration card persistence by explicit mobile/user/card inputs, and mobile-to-card lookups from card-vault owned mappings. Legacy mobile-based card mutation helpers that joined identity-owned user rows are removed, and raw mobile-to-PAN/card-list lookups, including the EBS `pan_from_mobile` compatibility route, are internal-only rather than public gateway routes.
10. Move EBS Adapter traffic into the `ebs-adapter` workload. It owns merchant EBS endpoints, consumer EBS/IPIN/QR/voucher endpoints, EBS card-info/card-registration-start/card-registration-completion routes, `register_with_card`, account-recovery balance validation, NEC meter lookup, EBS transaction lookup, mobile-transfer compatibility routes, and quick-pay execution through card-vault and notification-chat commands. Completion, `register_with_card`, and recovery balance no longer write identity or card tables directly; they command identity-auth and card-vault after EBS returns or validates card data. Mobile-transfer no longer reads identity/card tables; it commands card-vault for recipient PAN resolution. Transaction history no longer reads identity/card tables; it commands card-vault for the caller's masked PANs. Quick-pay no longer uses an in-process biller callback channel; callback delivery is commanded to notification-chat. EBS response handling no longer emits card-cache updates through process-local channels.
11. Move wallet HTTP traffic into the `wallet-api` workload. Public `/wallet` and operational `/admin/wallet` routes call `wallet-ledger` over gRPC; wallet-ledger remains the database and workflow boundary for wallet state.
12. Move consumer beneficiary traffic into the `consumer-beneficiary` workload. It owns beneficiary CRUD at the public path level using gateway user IDs and does not open identity tables.
13. Move admin/reporting to event-driven projections. Block payment writes from reporting code.
14. Keep migration scopes service-owned as schemas move forward; do not add new tables to the legacy monolith scope.

## Verification Gates

- `go test ./...`
- `go test ./cli ./apigateway ./store ./wallet/...`
- `noebs validate-deployment /path/to/noebs-release`
- `noebs render-kubernetes-secrets /path/to/noebs-kubernetes-release noebs /path/to/tls.crt /path/to/tls.key`
- `noebs validate-kubernetes-deployment /preflight` through the Argo CD preflight Job
- `kubectl kustomize deploy/kubernetes/overlays/current-host`
- `tofu -chdir=foundation/terraform fmt -check`
- `tofu -chdir=foundation/terraform validate`
- Smoke checks after deployment:
  - `GET /test` on the public gateway.
  - wallet gRPC health from inside the cluster.
  - Temporal worker polling the configured task queue.
  - one PSP webhook signature rejection test.
  - one admin/reporting read-only dashboard load.
  - one notification/chat websocket upgrade or notification read through `notification-chat`.
