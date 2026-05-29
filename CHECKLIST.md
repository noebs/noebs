# Noebs Microservices Migration Completion Checklist

Last updated: 2026-05-29

This file is the migration completion ledger. It answers two questions after
each implementation or deployment pass: what exists now, and whether the
microservices migration goal can be called complete. The task is not complete
until every item in "Completion Checklist" is checked.

Current answer: `NOT COMPLETE`. The architecture and deployment path are mostly
defined, and the latest implementation code commit is `03e82ae`. Server cutover
is still blocked by explicit Kubernetes release inputs, generated release
Secrets, the Noebs Argo CD application/microservice deployment, service health
verification after cutover, and retirement of the old Docker Compose deployment.

## Status Key

- `[x]` Implemented and locally verified.
- `[~]` Implemented, server verification or deployment still in progress.
- `[ ]` Not complete.

## What We Have Now

- [x] A documented microservices-only target architecture with no supported monolith compatibility deployment.
- [x] Kubernetes manifests for the API gateway, service roles, background workers, Kafka, Temporal, Keycloak, Postgres platform services, preflight, and migration Jobs.
- [x] Runtime service roles selected through mounted service config files, not environment variables.
- [x] Service discovery configured through explicit mounted config for HTTP, gRPC, Kafka, Temporal, and platform services.
- [x] API gateway route ownership invariants that keep external HTTP routes mapped to exactly one backend service owner.
- [x] Service-owned migration Jobs for database-owning Noebs services.
- [x] Kafka-backed EBS transaction event publishing and admin-reporting projection consumption.
- [x] Keycloak deployable as an independent platform service with its own Postgres database; application auth wiring remains future work.
- [x] Kubernetes NetworkPolicies for explicit platform backend ingress to Noebs Postgres, Kafka, Temporal, and Keycloak Postgres.
- [x] Kubernetes NetworkPolicies for explicit app service ingress to public HTTP service roles and wallet-ledger gRPC.
- [x] Runtime config/secret rendering paths that reject stale, extra, missing, or empty release inputs instead of guessing values.
- [x] OpenTofu foundation validation and Argo CD preconditions for required Kubernetes Secrets.
- [x] OpenTofu foundation phase controls that bind to the current host's existing Argo CD install and create the Noebs Argo CD application only after explicit release Secrets exist.
- [x] CI image publishing for the Kubernetes-consumed `ghcr.io/noebs/noebs:master` image.
- [x] Local verification passed for implementation commit `03e82ae`; server OpenTofu bootstrap plan passed from a clean temporary tree against `100.102.164.34`.
- [x] Server OpenTofu foundation bootstrap applied from persistent checkout `/home/adonese/src/noebs-foundation`.
- [x] GitHub Actions run `26617505980` passed for committed state `195c8d7`.
- [x] Server `100.102.164.34` has reachable SSH, k3s, Argo CD, GitHub CLI, clean apt metadata, and a healthy current Docker Compose Noebs deployment.
- [x] Server release audit ran from a clean temporary copy of `673e906` against `/app/noebs` without printing secret values.
- [ ] Complete explicit Kubernetes release inputs from current server secrets plus cutover-only values.
- [ ] Generated Kubernetes release Secret/config bundle from those explicit inputs.
- [ ] Live Noebs Kubernetes replacement deployment on `100.102.164.34`.
- [ ] Verified completed k3s/k8s migration Jobs for every service-owned database scope.
- [ ] Verified in-cluster Kafka, EBS publisher, admin-reporting projector, Keycloak, and all Noebs service health checks.
- [ ] Retired old Docker Compose Noebs deployment after Kubernetes health is confirmed.

## Target State

- [ ] Noebs runs as Kubernetes/k3s-managed microservices, not as a monolith plus side services.
- [ ] The old Docker Compose deployment is replaced after the Kubernetes deployment is confirmed healthy.
- [ ] Service migrations run through k8s/k3s migration Jobs.
- [ ] Tests are allowed to run migrations and use testcontainers-backed database setup.
- [ ] Runtime config and secrets are loaded from explicit merged config/secret files, not ambient environment variables.
- [ ] Each service owns its database and migration scope where applicable.
- [ ] API Gateway/BFF owns edge HTTP, CORS, auth enforcement, metrics, and public REST proxying.
- [ ] Identity/Auth owns users, OAuth/JWT, profile, API keys, and device identity.
- [ ] Card/Vault owns PAN/IPIN/tokenization/encryption/card lookup concerns.
- [ ] EBS Adapter owns EBS protocol integration, EBS retries/circuit behavior, and raw EBS logging.
- [ ] Wallet/Ledger owns wallets, balances, holds, double entry, fees, limits, rates, and funding sources.
- [ ] Wallet Worker owns Temporal workers and scheduled PSP/reconciliation workflows.
- [ ] PSP/Webhook owns PSP config, webhook verification, request-response mapping, and wallet workflow signalling.
- [ ] Notification/Chat owns websocket, push, biller callbacks, and notification projections.
- [ ] Admin/Reporting consumes events/projections and does not block payment writes.
- [ ] Kafka carries service events needed for admin/reporting projections.
- [ ] Keycloak is deployable as a platform service; application auth data wiring is a later task.

## Completed Work

- [x] Added Kubernetes-first service manifests for the split service roles.
- [x] Added k3s/k8s migration-job model for service-owned migrations.
- [x] Kept migrations enabled in tests so testcontainers exercises real schema setup.
- [x] Added OpenTofu foundation validation and formatting gates.
- [x] Added Keycloak as a deployable platform service; application data wiring remains separate future work.
- [x] Added Kafka to the service architecture.
- [x] Replaced admin/reporting internal HTTP projection writes with EBS outbox publishing and an admin-reporting Kafka projector.
- [x] Kept payment writes independent from admin/reporting projection processing.
- [x] Required explicit PSP webhook currency and Temporal signal inputs before workflow-backed webhook status updates.
- [x] Added Kubernetes release input audit that reports required field names and sources without printing secret values.
- [x] Bumped the SDK to `26.05.40`; no later SDK bump was needed for backend-only changes.
- [x] Installed and used `golangci-lint` locally and on the server gate path.
- [x] Scrubbed ambient environment inheritance from SOPS release-secret encryption.
- [x] Replaced SOPS CLI decryption's `SOPS_AGE_KEY_FILE` interface with an in-process age decrypt path.
- [x] Added a current-secret-aware Kubernetes release input template command that prints only missing field placeholders.
- [x] Added an API gateway invariant that every external HTTP route has exactly one service owner and is proxied to that owner.
- [x] Added OpenTofu preconditions that block the Noebs Argo CD application unless required Kubernetes Secrets contain the expected data keys.
- [x] Set Noebs Kubernetes workloads and migration/preflight jobs to always pull the mutable `master` image during Argo CD sync.
- [x] Added a CI image job and invariant test that publish the Kubernetes-consumed `ghcr.io/noebs/noebs:master` image after tests pass on `master`.
- [x] Added an explicit `ghcr-credentials` image pull Secret contract for Noebs Kubernetes workloads.
- [x] Installed GitHub CLI on `100.102.164.34` for explicit GHCR credential preparation; no GitHub auth state was configured.
- [x] Refreshed the Caddy apt signing key on `100.102.164.34`; `apt-get update` now runs without the expired-key warning.
- [x] Added a documentation invariant that the foundation and current-host runbooks list every required Kubernetes cutover Secret, including `ghcr-credentials`.
- [x] Added an OpenTofu precondition that blocks the Noebs Argo CD application when required Kubernetes Secret keys exist but their values are empty.
- [x] Added background health endpoints and Kubernetes probes for `wallet-worker`, `ebs-adapter-events`, and `admin-reporting-projector`.
- [x] Mirrored the background health contract in Docker Compose health checks for local microservice parity.
- [x] Added Kubernetes NetworkPolicy manifests and invariant tests for platform backend ingress ownership.
- [x] Added Kubernetes NetworkPolicy manifests and invariant tests for app service ingress ownership.
- [x] Aligned testcontainer-backed migration test deadlines with the explicit Postgres wait strategy used by the suite.
- [x] Replaced remaining CLI Fiber route-test default one-second timeouts with the explicit route test budget used by slow server runs.
- [x] Added explicit OpenTofu phase inputs: `argocd_installation_mode` and `create_noebs_application`.
- [x] Corrected the foundation current-host Argo CD repository URL to `https://github.com/noebs/noebs.git`.

## Verified Gates

- [x] `go test ./...` passed locally for the latest committed state.
- [x] `golangci-lint run --new-from-rev=HEAD ./...` passed locally before commit.
- [x] `docker compose config -q` passed locally.
- [x] `kubectl kustomize deploy/kubernetes/base` passed locally.
- [x] `kubectl kustomize deploy/kubernetes/overlays/current-host` passed locally.
- [x] `tofu -chdir=foundation/terraform fmt -check` passed locally.
- [x] `tofu -chdir=foundation/terraform validate` passed locally.
- [x] Latest committed changes were server-verified on `100.102.164.34` from a clean temporary archive.
- [x] GitHub Actions run `26603575493` passed for `b34ad53`.
- [x] Current NetworkPolicy and test-timeout patch was server-verified on `100.102.164.34` from a clean temporary archive based on `26462e3`; that patch became `b34ad53`.
- [x] Implementation commit `6d03050` passed local `go test ./...`.
- [x] Implementation commit `6d03050` passed local `golangci-lint run --new-from-rev=HEAD ./...`.
- [x] Implementation commit `6d03050` passed local `git diff --check`.
- [x] Implementation commit `6d03050` passed server Docker-backed `timeout 60m go test -p 1 -count=1 ./...` from a clean temporary tree on `100.102.164.34`.
- [x] GitHub Actions run `26612719090` passed for `038eb88`.
- [x] GitHub Actions run `26613199076` passed for `673e906`.
- [x] GitHub Actions run `26617505980` passed for `195c8d7`.
- [x] GitHub Actions run `26618328014` passed for `0a94a65`.
- [x] Implementation commit `03e82ae` passed local `go test ./...`.
- [x] Implementation commit `03e82ae` passed local `golangci-lint run --new-from-rev=HEAD ./...`.
- [x] Implementation commit `03e82ae` passed local `tofu -chdir=foundation/terraform fmt -check`, `tofu -chdir=foundation/terraform validate`, and `git diff --check`.
- [x] Implementation commit `03e82ae` passed server OpenTofu bootstrap planning from `/tmp/noebs-foundation-phase`: `argocd_installation_mode=existing`, `create_noebs_application=false`, and `noebs_repo_url=https://github.com/noebs/noebs.git`; the plan reads existing `argocd` and creates only the `noebs` namespace plus Noebs AppProject.
- [x] Server OpenTofu bootstrap apply from `/home/adonese/src/noebs-foundation/foundation/terraform` added the `noebs` namespace and `argocd/noebs` AppProject; follow-up `tofu plan` returned `No changes`.
- [x] Server audit at `100.102.164.34` with `/tmp/noebs-head-audit/noebs audit-kubernetes-release-inputs /app/noebs` identified transformable current-secret fields without printing secret values.

## Current Server State

- [x] SSH reaches `100.102.164.34`.
- [x] k3s is installed and the node is Ready.
- [x] Argo CD is installed in namespace `argocd`; the current host install is not a Helm release, so foundation uses explicit `argocd_installation_mode = "existing"`.
- [x] Existing Docker Compose Noebs service is still running and healthy; host port `8081` `/test` returned `{"message":true}` after the full server test suite.
- [x] GitHub CLI is installed on the server; `gh auth status` reports no configured GitHub login.
- [x] Server package metadata refresh is clean after the Caddy apt key update.
- [x] `/home/adonese/.testcontainers.properties` sets explicit Testcontainers Ryuk timeouts: `ryuk.connection.timeout=4m` and `ryuk.reconnection.timeout=2m`.
- [x] No unhealthy Docker containers or non-running k8s pods were reported after the full server test suite.
- [x] OpenTofu persistent state lives under `/home/adonese/src/noebs-foundation/foundation/terraform`.
- [x] The `noebs` namespace exists and is managed by OpenTofu.
- [x] The `argocd/noebs` AppProject exists and points only at `https://github.com/noebs/noebs.git` for namespace `noebs`.
- [x] No Noebs workload resources are currently applied in the `noebs` namespace.
- [x] Server OpenTofu bootstrap plan with `create_noebs_application=false` succeeds without requiring Noebs release Secrets and plans only the Noebs namespace plus Noebs AppProject.
- [x] Current-secret-aware Kubernetes release input template was rendered on the server at `/tmp/noebs-kubernetes-release.inputs.yaml.plain`.
- [ ] Noebs Kubernetes replacement deployment has not been applied yet.
- [ ] Argo CD does not yet manage the Noebs Kubernetes app.

## Open Completion Items

- [ ] Complete the explicit Kubernetes release input set from the current server secrets and cutover-only inputs.
- [ ] Generate the Kubernetes release secret/config bundle from the complete explicit inputs.
- [ ] Deploy the Kubernetes microservices replacement to the server.
- [ ] Confirm every Noebs service is healthy after the replacement deployment.
- [ ] Confirm migration jobs complete through k3s/k8s for each service-owned database scope.
- [ ] Confirm Kafka broker, EBS event publisher, and admin-reporting projector are healthy in-cluster.
- [ ] Confirm Keycloak deploys as a service; no application auth data wiring is expected yet.
- [ ] Remove or retire the old Docker Compose deployment after Kubernetes replacement is confirmed.

## Server Release Blockers

- [ ] The current server audit still reports missing explicit cutover inputs; `noebs.db_url`, `noebs.jwt_secret`, `noebs.google_client_id`, and `noebs.google_client_secret` are transformable from the current encrypted root.
- [ ] The current server audit reports empty current `noebs.sms_gateway`, `noebs.sms_key`, and `noebs.sms_sender`; these still need explicit cutover input.
- [ ] The current server audit reports missing explicit values for tenant/admin, card-vault data key, all resolved EBS runtime fields, GHCR Docker config JSON, Google redirect URL, Keycloak bootstrap/database, PSP, SMS values, and Temporal Postgres password.
- [ ] GHCR credentials have not been configured on the server; the required `ghcr-credentials` pull Secret remains an explicit cutover input.
- [ ] The live server checkout at `~/src/noebs` has unrelated dirty files and must not be overwritten.
- [ ] Deployment should continue from a clean temporary worktree or a fresh release checkout.

## Completion Checklist

- [ ] Required Kubernetes release inputs are complete and explicit.
- [ ] Current server secrets have been transformed only where exact source keys exist.
- [ ] Kubernetes release secret/config bundle has been generated from the complete inputs.
- [x] Noebs image published for the deployment tag consumed by Kubernetes manifests.
- [ ] Kubernetes microservices replacement has been applied on `100.102.164.34`.
- [ ] Service-owned migration Jobs completed successfully in k3s/k8s.
- [ ] API gateway routes proxy only to their single service owners.
- [ ] Kafka broker, EBS event publishing, and admin-reporting projector are healthy.
- [ ] Keycloak is running as a deployable service.
- [ ] Public Noebs health endpoint passes after cutover.
- [ ] All pre-existing non-Noebs services on the server still report healthy/running.
- [ ] Old Docker Compose Noebs deployment is retired after Kubernetes health is confirmed.
- [ ] Runtime config and secret paths do not depend on environment variables.

## Completion Decision

After each deployment attempt, record the answer using this rule:

- [ ] `COMPLETE`: every item in "Completion Checklist" is checked, and the verification record below names the commit, server, deployment method, migration Jobs, service health result, Kafka/projector result, Keycloak result, public health result, and old deployment retirement result.
- [x] `NOT COMPLETE`: at least one item in "Completion Checklist" is unchecked. The unchecked items are the remaining blockers.

Latest decision: `NOT COMPLETE`.

Latest blocker summary:

- Explicit Kubernetes release inputs and generated Secrets are not complete.
- The Kubernetes replacement has not been applied on `100.102.164.34`.
- Service-owned migration Jobs, Kafka/projector health, Keycloak health, and all Noebs service health checks have not been verified after cutover.
- The old Docker Compose Noebs deployment is still active.

## Verification Record

- Latest implementation commit: `03e82ae`.
- Latest recorded CI verification: GitHub Actions run `26618328014` passed for `0a94a65`.
- Latest implementation details: explicit OpenTofu Argo CD installation mode, explicit Noebs application creation phase, and corrected current-host Argo CD repository URL.
- Latest local implementation verification: `go test ./...`, `golangci-lint run --new-from-rev=HEAD ./...`, `tofu -chdir=foundation/terraform fmt -check`, `tofu -chdir=foundation/terraform validate`, and `git diff --check` passed.
- Latest server implementation verification: OpenTofu initialized and applied from `/home/adonese/src/noebs-foundation/foundation/terraform` on `100.102.164.34` with `argocd_installation_mode=existing` and `create_noebs_application=false`; it created the `noebs` namespace and `argocd/noebs` AppProject. A follow-up `tofu plan` returned `No changes`; Noebs `/test` still returned `{"message":true}`; no non-running k8s pods were reported.
- Latest server release-input audit: `/app/noebs` provides `noebs.db_url`, `noebs.jwt_secret`, `noebs.google_client_id`, and `noebs.google_client_secret`; `noebs.sms_gateway`, `noebs.sms_key`, and `noebs.sms_sender` exist but are empty; the remaining required cutover fields are still missing.
- Latest server checked: `100.102.164.34`.
- Latest server deployment state: Docker Compose Noebs is still active; foundation bootstrap is applied; the Noebs Argo CD Application and Kubernetes replacement workloads are not active yet.
- Latest goal decision: `NOT COMPLETE`.

## Completion Rule

The migration goal is complete only when the Kubernetes microservices deployment has replaced the old deployment on `100.102.164.34`, all service health checks pass, migration jobs have completed through k3s/k8s, Kafka-backed reporting projections are running, and no runtime config or secret path depends on ambient environment variables.
