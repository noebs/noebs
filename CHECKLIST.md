# Noebs Microservices Migration Checklist

Last updated: 2026-05-28

## Status Key

- `[x]` Implemented and locally verified.
- `[~]` Implemented, server verification or deployment still in progress.
- `[ ]` Not complete.

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

## Current Server State

- [x] SSH reaches `100.102.164.34`.
- [x] k3s is installed and the node is Ready.
- [x] Argo CD is installed.
- [x] Existing Docker Compose Noebs service is still running and healthy; `/test` returned `{"message":true}` after server gates.
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

- [ ] The current server audit still reports missing explicit cutover inputs.
- [ ] The live server checkout at `~/src/noebs` has unrelated dirty files and must not be overwritten.
- [ ] Deployment should continue from a clean temporary worktree or a fresh release checkout.

## Completion Rule

The migration goal is complete only when the Kubernetes microservices deployment has replaced the old deployment on `100.102.164.34`, all service health checks pass, migration jobs have completed through k3s/k8s, Kafka-backed reporting projections are running, and no runtime config or secret path depends on ambient environment variables.
