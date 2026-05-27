# noebs Microservices Architecture Plan

Status: reviewed foundation, microservices-only runtime.
Deployment target: existing host `100.102.164.34`.

## Current Shape

The pre-split runtime was a single Go binary built from `./cli`. `cli/config.go` initialized the database, migrations, Fiber routes, auth, consumer handlers, merchant handlers, dashboard, chat hub, wallet HTTP handlers, wallet gRPC server, PSP webhook handling, and the Temporal wallet worker in one process.

The target runtime is microservices-only. There is no supported monolith runtime role, no mixed monolith/microservice deployment mode, and no silent fallback to an all-in-one process. The same image can be reused while packages are being extracted, but every process must declare a single service role and own only that role's process lifecycle. If a split exposes a bug, we fix forward.

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

Owns public HTTP shape, Fiber edge routing, request IDs, CORS, auth enforcement, public metrics, request logs, and compatibility routing for legacy clients. It must not own payment state. It calls internal services over gRPC/HTTP and maps their typed errors to the public REST contract.

Initial package owner: `apigateway`, route composition from `cli`.

### Identity/Auth Service

Owns users, mobile auth, Google OAuth linking, JWT issuance, auth accounts, profiles, API keys, and device identity. Tenancy should stay here initially as tenant identity/configuration, not as a separate service, until tenant lifecycle needs its own API and operators.

Initial package owner: existing user/auth code in `consumer` plus `store` user/auth tables. The first split owns login, registration, OTP, Google OAuth, profile, language, password, API-key generation, KYC/check-user, and device-token update routes.

### Consumer Beneficiary Service

Owns consumer beneficiary/contact payment targets at the public compatibility path level. It keeps beneficiary CRUD out of the API Gateway while the legacy consumer package is being carved into explicit owners.

Initial package owner: beneficiary methods in `consumer` and `store`. The first split owns `POST /consumer/beneficiary`, `GET /consumer/beneficiary`, and `DELETE /consumer/beneficiary`.

### Card/Vault Service

Owns PAN/IPIN/card storage, encryption, tokenization, fingerprints, card lookup, and mobile-to-card mappings. Only this service can read decrypted card data. Other services receive tokenized references or last-four metadata.

Initial package owner: card/token functions currently in `consumer` and `store`. The first split owns card info, card registration/completion, card CRUD, card lookup, mobile-to-PAN, NEC lookup, payment-token, payment-request, and quick-pay token routes.

### EBS Adapter Service

Owns EBS HTTP protocol details, merchant and consumer EBS calls, IPIN protocol calls, retry policy, circuit breakers, EBS endpoint selection, and raw EBS interaction logs. Merchant and consumer APIs become compatibility clients of this adapter rather than owners of EBS protocol logic.

Initial package owner: `ebs_fields`, `consumer/*_service.go`, `merchant/*`. The first split owns merchant EBS passthrough, merchant EBS operations, consumer EBS operations, QR/IPIN calls, voucher generation, EBS transaction lookups, and mobile-transfer compatibility routes.

### Wallet/Ledger Service

Owns wallets, balances, holds, double-entry posting, fees, limits, rates, funding sources, withdrawal destinations, wallet PIN/2FA state, and wallet gRPC APIs. Wallet and ledger stay together because balance correctness depends on local transactional invariants.

Initial package owner: `wallet`, `wallet/store`, `wallet/grpc`, `proto/noebs/wallet/v1`. The HTTP split uses `wallet-api` for public wallet and wallet-admin routes while `wallet-ledger` remains the gRPC ledger process.

### Wallet Worker Service

Owns Temporal workers and scheduled wallet workflows. It uses the same wallet codebase at first, but runs as a separate process with no public HTTP listener. It executes PSP polling, reconciliation, P2P, deposit, withdrawal, and manual-transfer workflows.

Initial package owner: `wallet/worker`, `wallet/workflow`, `wallet/activity`.

### PSP/Webhook Service

Owns PSP config loading, webhook signature verification, PSP request/response mapping, idempotent webhook persistence, and workflow signaling. It must not post ledger entries directly; successful webhooks signal wallet workflows.

Initial package owner: `wallet/psp`, `wallet/handler/psp_webhook.go`.

### Notification/Chat Service

Owns websocket hub, persisted push data, biller callback delivery, notification projections, and push fanout. It consumes domain events from wallet/EBS/auth rather than reading write models directly.

Initial package owner: current `github.com/tutipay/ws` integration plus notification methods in `consumer`. The first split owns `/ws`, `GET /consumer/notifications`, and `POST /consumer/submit_contacts`.

### Admin/Reporting Service

Owns read-only dashboards, settlement reports, issue reports, and operational projections. It consumes events or reporting tables. It must not own payment writes.

Initial package owner: `dashboard` plus wallet admin templates as a transitional read surface.

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
- service-owned Postgres databases, addressed through each service's mounted `secrets.yaml`.

Argo CD owns application sync from `deploy/kubernetes/overlays/current-host`. Terraform under `foundation/terraform` owns platform installation, the `noebs` namespace, service-discovery outputs, and the Argo CD application definition. Secrets remain outside Git as Kubernetes Secrets generated from the existing SOPS material.

Migrations are deployed through `deploy/kubernetes/base/migrate-job.yaml` as Argo CD PreSync hooks. Each job has a service-specific role and runs only that service's embedded migration scope. Service Deployments must not run migrations in their startup path.

Service identity is config-driven. Each noebs workload mounts the shared `/app/config.yaml` plus a tracked `/app/service.yaml` containing `noebs.service_role`; deployments do not select noebs roles through environment variables. Secrets continue to merge through `secrets.yaml`, with service-owned database URLs supplied by service-specific Kubernetes Secrets.

For local Docker Compose, a single SOPS file can still track service-owned database URLs by using `noebs.service_databases` keyed by service role and migration role. When that map exists, the runtime requires an entry for the current `noebs.service_role` and copies that URL into `noebs.db_url`.

## Migration Plan

1. Add explicit config-selected runtime roles to the current binary: `api-gateway`, `identity-auth`, `card-vault`, `ebs-adapter`, `psp-webhook`, `admin-reporting`, `notification-chat`, `consumer-beneficiary`, `wallet-api`, `wallet-ledger`, and `wallet-worker`.
2. Run database migrations only through Kubernetes/k3s migration Jobs with service-specific roles: `identity-auth-migrate`, `card-vault-migrate`, `ebs-adapter-migrate`, `psp-webhook-migrate`, `admin-reporting-migrate`, `notification-chat-migrate`, `consumer-beneficiary-migrate`, and `wallet-ledger-migrate`.
3. Deploy role-specific Kubernetes workloads with ClusterIP service discovery. No monolith workload is retained.
4. Move PSP webhook traffic into the `psp-webhook` workload. It owns provider verification, request/response mapping, interaction persistence, and Temporal workflow signaling.
5. Move dashboard traffic into the `admin-reporting` workload. It owns read-only dashboard, settlement, merchant-view, status, and stream routes. `GET /dashboard/create` and `POST /dashboard/issues` are not registered by the reporting service.
6. Move notification/chat traffic into the `notification-chat` workload. It owns websocket contacts and notification reads at the public path level.
7. Move Identity/Auth traffic into the `identity-auth` workload. It owns JWT issuance, OAuth, user/profile, API-key, KYC/check-user, and device-token update routes.
8. Move Card/Vault traffic into the `card-vault` workload. It owns card storage, card lookup, mobile-to-PAN, and payment-token routes at the public path level.
9. Move EBS Adapter traffic into the `ebs-adapter` workload. It owns merchant EBS endpoints, consumer EBS/IPIN/QR/voucher endpoints, EBS transaction lookup, and mobile-transfer compatibility routes.
10. Move wallet HTTP traffic into the `wallet-api` workload. It owns `/wallet` and `/admin/wallet` routes at the public path level while `wallet-ledger` owns the gRPC ledger process.
11. Move consumer beneficiary traffic into the `consumer-beneficiary` workload. It owns beneficiary CRUD at the public path level.
12. Move admin/reporting to event-driven projections. Block payment writes from reporting code.
13. Keep migration scopes service-owned as schemas move forward; do not add new tables to the legacy monolith scope.

## Verification Gates

- `go test ./...`
- `go test ./cli ./apigateway ./store ./wallet/...`
- `kubectl kustomize deploy/kubernetes/overlays/current-host`
- `terraform -chdir=foundation/terraform fmt -check`
- `terraform -chdir=foundation/terraform validate`
- Smoke checks after deployment:
  - `GET /test` on the public gateway.
  - wallet gRPC health from inside the cluster.
  - Temporal worker polling the configured task queue.
  - one PSP webhook signature rejection test.
  - one admin/reporting read-only dashboard load.
  - one notification/chat websocket upgrade or notification read through `notification-chat`.
