# Noebs Microservices Migration Completion Checklist

Last updated: 2026-05-29

Current answer: `COMPLETE`.

The Noebs deployment on `100.102.164.34` has been cut over from the old Docker Compose Noebs stack to k3s/Kubernetes microservices managed by Argo CD. The old Noebs Compose stack was removed after Kubernetes health checks passed. Remaining non-Noebs services were checked after the cutover; DSA Temporal was restarted and is now healthy.

## Completion Checklist

- [x] Required Kubernetes release inputs are explicit and rendered into Kubernetes Secrets.
- [x] Current server secrets were transformed only where exact source keys existed; generated/dummy cutover values were encrypted through the SOPS release bundle.
- [x] Kubernetes release secret/config bundle was generated from explicit inputs.
- [x] Noebs image `ghcr.io/noebs/noebs:master` is consumed by Kubernetes manifests.
- [x] Kubernetes microservices replacement is applied on `100.102.164.34`.
- [x] Service-owned migration Jobs completed successfully in k3s/k8s.
- [x] API gateway routes proxy to their service owners through mounted service discovery.
- [x] Kafka broker, EBS event publisher, and admin-reporting projector are healthy.
- [x] Keycloak is running as a deployable service with its own Postgres database.
- [x] Public Noebs health endpoint passes after cutover.
- [x] Pre-existing non-Noebs services on the server were checked after cutover.
- [x] Old Docker Compose Noebs deployment is retired.
- [x] Runtime config and secret paths do not depend on ambient environment variables.

## Deployment Record

- Server: `100.102.164.34`.
- Repository revision: current `master` at verification time.
- Latest runtime-manifest change: `98708d7f8b526443459bc9425b34b2d4badda19d`.
- Deployment method: Argo CD Application `argocd/noebs`, path `deploy/kubernetes/overlays/current-host`, namespace `noebs`.
- Runtime model: Kubernetes/k3s microservices only; no monolith/Compose Noebs runtime remains active.
- Edge route: `api.noebs.sd` proxies through the `edge/caddy` deployment to `api-gateway.noebs.svc.cluster.local:8080`.
- SDK release: `~/src/sdk` bumped, committed, tagged, and pushed as `v26.05.41`.

## Verified Server State

- [x] Argo CD reports Noebs `Synced` and `Healthy`.
- [x] All Noebs Deployments are available: `api-gateway`, `identity-auth`, `card-vault`, `ebs-adapter`, `ebs-adapter-events`, `psp-webhook`, `admin-reporting`, `admin-reporting-projector`, `notification-chat`, `consumer-beneficiary`, `wallet-api`, `wallet-ledger`, and `wallet-worker`.
- [x] Platform services are running: Noebs Postgres, Kafka, Temporal, Temporal UI, Keycloak, Keycloak Postgres.
- [x] Temporal namespace `default` is registered for Noebs wallet workflows.
- [x] Kafka topic `noebs-ebs-transactions-v1` exists.
- [x] In-cluster `/test` checks passed for HTTP services and background workers.
- [x] Public `https://api.noebs.sd/test` returned HTTP 200 with `{"message":true}`.
- [x] DSA public root `https://dsa.adonese.sd/` returned HTTP 200 after the Noebs cutover.
- [x] Docker Compose Noebs project containers were removed with `docker compose down` from `/app/noebs`.
- [x] Other Docker services were checked after cutover; DSA Temporal health was repaired and reports healthy.

## Verification Commands

- Local Noebs static verification:
  - `kubectl kustomize deploy/kubernetes/overlays/current-host`
  - `go test -c ./cli`
  - `git diff --check`
- Server Noebs focused verification:
  - `timeout 30m go test ./cli -run "TestTemporalKubernetesUsesMountedConfigAndSchemaJob|TestTemporalDockerComposeUsesMountedConfigAndSchemaJob|TestKubernetesNetworkPoliciesDeclareIngressPorts|TestKubernetesAPIGatewayTargetsHaveIngressPolicies|TestMigrationJobsRunBeforeNoebsRuntimeWorkloads"`
- SDK verification:
  - `./gradlew test`

## Notes

- NetworkPolicies are port-scoped for the current host because kube-router did not install accept rules for short-lived source-selected migration Jobs. This keeps target ports explicit without reintroducing source-selector brittleness during hook execution.
- The Temporal namespace bootstrap is an Argo CD hook at sync wave `18`, after Temporal is up and after service-owned migrations, before runtime workloads need the namespace.
- Kafka was introduced for EBS transaction events and admin-reporting projections; admin/reporting projection processing no longer blocks payment writes.
