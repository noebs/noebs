# current-host Overlay

This overlay targets the existing deployment host through its Tailscale
management address `100.102.164.34`. Public edge traffic reaches the same host
through its `eth0` address `213.199.63.78`.

The `noebs` namespace is owned by `foundation/terraform`; this overlay only renders namespaced runtime resources.

Required DNS records:

- `api.noebs.sd A 213.199.63.78`
- `dsa.adonese.sd A 213.199.63.78`

Required Kubernetes Secrets in namespace `noebs`:

- `noebs-release-manifest` with key `release-manifest.yaml`.
- `api-gateway-secrets` with key `secrets.yaml`.
- `identity-auth-secrets` with key `secrets.yaml`.
- `keycloak-secrets` with keys `keycloak.conf`, `db-ca.pem`, `tls.crt`, and `tls.key`.
- `keycloak-transport-ca` with the public `ca.pem` only.
- `keycloak-reconciler-credentials` with key `config.yaml`.
- `keycloak-postgres-credentials` with keys `password`, `tls.crt`, and `tls.key`.
- `card-vault-secrets` with key `secrets.yaml`.
- `ebs-adapter-secrets` with key `secrets.yaml`.
- `psp-webhook-secrets` with key `secrets.yaml`.
- `admin-reporting-secrets` with key `secrets.yaml`.
- `notification-chat-secrets` with key `secrets.yaml`.
- `wallet-api-secrets` with key `secrets.yaml`.
- `wallet-ledger-secrets` with key `secrets.yaml`.
- `wallet-worker-secrets` with key `secrets.yaml`.
- `ebs-adapter-events-secrets`, `admin-reporting-projector-secrets`, `workload-auth-migrate-secrets`, `workload-auth-cleanup-secrets`, `gateway-auth-migrate-secrets`, and `gateway-auth-cleanup-secrets`, each with key `secrets.yaml`.
- `identity-auth-migrate-secrets`, `card-vault-migrate-secrets`, `ebs-adapter-migrate-secrets`, `admin-reporting-migrate-secrets`, `notification-chat-migrate-secrets`, and `wallet-ledger-migrate-secrets`, each with key `secrets.yaml`.
- `workload-auth-postgres-roles` with key `roles.yaml`.
- `gateway-auth-postgres-roles` with key `roles.yaml`.
- `service-postgres-roles` with keys `passwords.env`, `bootstrap.sql`, and `roles.yaml`; only Postgres and deployment preflight mount this complete catalog.
- `internal-transport-platform` with key `credentials.yaml`.
- `postgres-credentials` with keys `ca.pem`, `tls.crt`, and `tls.key`; there is no network bootstrap password.
- `temporal-postgres-credentials` with keys `password`, `ca.pem`, `tls.crt`, and `tls.key`.
- `temporal-server-credentials` with keys `ca.pem`, `tls.crt`, and `tls.key`.
- `temporal-namespace-bootstrap-credentials` with keys `ca.pem` and `client-secret`.
- `ghcr-credentials` with key `.dockerconfigjson`.

Prepare the release from one strict SOPS-encrypted authority input and its
explicit Age identity:

```sh
noebs prepare-kubernetes-release /path/to/noebs-repo /path/to/kubernetes-release.inputs.yaml /path/to/age-key.txt /path/to/noebs-kubernetes-release
```

`kubernetes-release.inputs.yaml.example` documents every required authority.
The real input must be SOPS-encrypted. Missing authority, unknown fields,
noncanonical identifiers, and ambiguous YAML fail preparation; the command
does not inspect another deployment root or generate substitute credentials.

```sh
noebs render-kubernetes-secrets /path/to/noebs-kubernetes-release noebs | kubectl apply -f -
```

The preparation command reads tracked Kubernetes config, service roles, and
tenant catalog from the repo, decrypts only the named authority input,
encrypts service secret files with SOPS, fingerprints the complete artifact
set, and validates the output. The renderer validates that same layout and
cross-artifact authority, decrypts each service into its own Kubernetes
`Secret`, and emits a second fingerprint for the rendered plaintext set. The
release directory contains exactly `config.yaml`, `tenant-catalog.yaml`,
`release-manifest.yaml`, `.sops/age-key.txt`, one `services/<role>.yaml` file
for every runtime and migration role, one `secrets/<service>.secrets.yaml`
file per service-owned secret, and the explicit platform files under
`platform/`. Missing or extra artifacts, placeholders, mismatched database
roles/passwords, Keycloak credentials, CAs, workload keys, PSP routes, tenant
catalog membership, or invalid OIDC/BFF construction fail before output.
The age identity never enters Kubernetes; application and preflight pods
accept only the rendered plaintext Secret format.

The encrypted authority input supplies the internal CA certificate and private
key. Preparation derives per-role leaf identities, but the CA signing key is
input-only: it is never written to the prepared release or rendered into
Kubernetes. `internal-transport-platform` contains the public CA and the
server leaf identities consumed by the renderer and deployment preflight. Rotating the CA requires a
coordinated full workload cutover; do not roll only part of the identity set.

Noebs HTTP service discovery, the wallet API-to-ledger gRPC hop, service connections to every Postgres server, and every Keycloak application hop use TLS 1.3 with generated identities. Keycloak verifies the distinct `keycloak-postgres` database identity; Temporal and its schema job verify the distinct `temporal-postgres` identity; Caddy, the API gateway, and the reconciliation jobs verify the `keycloak` HTTPS identity. Keycloak has no plaintext application listener. Its HTTP management listener stays pod-local to kubelet probes through a node-only NetworkPolicy. Internal Noebs HTTP and wallet gRPC servers require a trusted client certificate. All Postgres servers use `hostssl` authentication and explicitly reject `hostnossl`.

Kafka remains a plaintext single-node platform dependency for this alpha. Temporal exposes only its TLS/JWT frontend to the exact wallet and bootstrap peers; backend and internal-frontend listeners remain loopback-only. Keycloak owns distinct revocable identities for wallet-ledger, wallet-worker, and namespace bootstrap, and the unused Temporal UI is absent. Docker Compose uses the same Temporal authority boundary on isolated networks.

Keycloak may open public HTTPS connections for configured identity providers. Kubernetes NetworkPolicy cannot constrain egress by DNS name, so the policy permits TCP 443 to public IPv4 addresses while excluding cluster, private, loopback, carrier-grade NAT, and link-local ranges.

Each noebs service secret contains only the material owned by that service. Database-opening service secrets include `noebs.service_databases` keyed only by the database owner role. Runtime config copies the owner URL into `noebs.db_url` for that role and rejects non-owner database entries. `api-gateway-secrets` contains the explicit confidential back-office and wallet-authorization clients, session-encryption keyring, opaque PSP callback routing map, and `api-gateway` session database entry; it contains no JWT or static administrator credential. `wallet-api-secrets` has no database entry. `wallet-worker-secrets` and `psp-webhook-secrets` use the `wallet-ledger` owner key and connect as the exact `wallet_ledger_worker` and `wallet_ledger_webhook` roles. PSP tables are in the wallet migration scope; the webhook ingress has no migration role or database.

`ebs-adapter-secrets` must provide explicit resolved EBS runtime values: `consumer_endpoint`, `merchant_endpoint`, `ipin_endpoint`, `consumer_app_id`, `merchant_app_id`, `ipin_username`, `ipin_password`, `pub_key`, `ipin_key`, `pan`, `pin`, `ipin`, and `exp_date`. The runtime does not pick QA or production endpoints from mode booleans.

`keycloak-secrets` is not a noebs merged secret. It contains the steady configuration, the CA used to verify `keycloak-postgres`, and the `keycloak` HTTPS certificate and private key; bootstrap admin options are forbidden. `keycloak-transport-ca` contains only the public CA used by HTTPS clients. `keycloak-reconciler-credentials` contains the realm-local reconciler config rendered by the release preparation command. Keycloak realm, client, organization, role, and identity-provider state is reconciled from `deploy/kubernetes/keycloak-authority/keycloak-desired-state.yaml`. The one-time service-account bootstrap is isolated in `deploy/kubernetes/overlays/bootstrap-current-host`.

When using the in-cluster `postgres` StatefulSet, its exact 22 login roles connect to these eight service databases: `gateway_auth`, `workload_auth`, `identity_auth`, `card_vault`, `ebs_adapter`, `admin_reporting`, `notification_chat`, and `wallet_ledger`.

Postgres role or TLS rotation is a bounded-outage operation because the StatefulSet uses fixed-name Secret `subPath` mounts and reconciles role passwords only at process start. Pause automated Argo CD sync, scale every Noebs application Deployment to zero, suspend `noebs-workload-auth-cleanup` and `noebs-gateway-auth-cleanup`, and apply the complete newly rendered Secret set. Replace the database pod with exactly `kubectl -n noebs delete pod postgres-0`, then wait for the replacement `postgres-0` pod to become Ready. Run one sync of the unchanged reviewed revision: preflight must succeed, migration hooks must complete in waves 9–16, cleanup resumes at wave 19, and application Deployments return at waves 20–21. Resume automated sync only after the new pods pass readiness. Do not use `rollout restart`, a database-only secret update, partial credentials, or simultaneous old/new passwords.

The Postgres data volume is also a clean-install boundary. Startup requires `.noebs-postgres-authority` and rejects any volume with unexpected roles or databases. Recreate a nonconforming PVC; do not migrate its grants or owners forward.

Noebs service roles and OTel service names are selected by mounted config, not environment variables. The base `noebs-config` ConfigMap provides shared `config.yaml` and one `*.service.yaml` key per workload and migration job.

The overlay pins every runtime image by registry digest. The checked-in Noebs
digest is the immutable baseline that was already serving traffic before the
alpha rollout (`f2e3e660aaf7cca6932a585f2d5d0ffddfa2a446`,
`sha256:dee1f46c6826b741be166fcb04edb0c579af6f495db518c954e887b0bf2d806e`).
Do not replace a digest with `master`, a release tag, or another mutable tag.
`IfNotPresent` is safe with a digest because the requested content cannot move.

The current-host resource patches are based on observed steady-state usage on
the 12 GiB single-node host. Go HTTP services reserve 25 millicores/64 MiB and
workers reserve 50 millicores/64 MiB, with bounded burst limits. PostgreSQL,
Kafka, Temporal, and Keycloak have role-specific reservations and limits. All
runtime and hook pods must render with both requests and limits; a
`BestEffort` pod is a release failure.

## Immutable alpha release

Release evidence is produced locally; GitHub automation is not part of the
test, build, or publication path. Follow
[`docs/alpha-image-release.md`](../../../../docs/alpha-image-release.md) to
export one reviewed commit, build it on a trusted Docker host, publish only its
write-once full-SHA tag, verify the registry manifest, and create the release
receipt.

Use two commits so application content and GitOps promotion remain distinct:

The destructive first Keycloak promotion follows the exact empty-state order
in [`deploy/host/keycloak-empty-state-cutover.md`](../../../host/keycloak-empty-state-cutover.md).
Do not improvise individual PVC or password rotations.

1. Finish the application commit and run the required local test, race,
   migration, manifest, and security gates. Record its full SHA as
   `RELEASE_SHA`.
2. Run `scripts/publish-alpha-image.sh` for that SHA. Retain its JSON receipt
   and verify that the recorded source SHA, source tree, tag, and digest match
   the reviewed commit and registry result.
3. In a separate commit, change only the four Noebs `digest:` fields in
   `overlays/current-host`, `overlays/bootstrap-current-host`,
   `operations/lookup`, and `operations/memberships/base`. Render all four
   workflows and require every Noebs runtime, bootstrap, lookup, and membership
   image to use the receipt's `ghcr.io/noebs/noebs@sha256:<digest>` reference.
4. Retain the tested digest and receipt as the rollback floor, announce the
   cutover window, and push the digest-pin commit. Set
   `noebs_target_revision` to that exact lowercase 40-hex commit and apply the
   reviewed foundation plan so both Argo CD Applications target it directly.
5. Watch the preflight and migration hooks, wave-20 rollout, and post-deploy
   smoke. If the schema boundary has been crossed, forward-fix or redeploy the
   retained schema-aware digest; never substitute a mutable tag.

Example local publication and manifest verification:

```sh
release_sha="$(git rev-parse --verify HEAD^{commit})"
receipt="$HOME/noebs-release-$release_sha.json"
scripts/publish-alpha-image.sh "$release_sha" "$receipt"
release_digest="$(jq -er '.digest' "$receipt")"
kubectl kustomize deploy/kubernetes/overlays/current-host > /tmp/noebs-rendered.yaml
grep -F "ghcr.io/noebs/noebs@$release_digest" /tmp/noebs-rendered.yaml
kubectl apply --dry-run=server -f /tmp/noebs-rendered.yaml >/dev/null
for operation in \
  deploy/kubernetes/overlays/bootstrap-current-host \
  deploy/kubernetes/operations/lookup \
  deploy/kubernetes/operations/memberships/dry-run \
  deploy/kubernetes/operations/memberships/apply
do
  kubectl kustomize "$operation" | grep -F "ghcr.io/noebs/noebs@$release_digest" >/dev/null
done
```

After Argo CD reports `Synced` and `Healthy`, verify that its revision is the
digest-pin commit, every Deployment and StatefulSet has completed rollout, all
Noebs pod `imageID` values end in the expected digest, no runtime pod is
`BestEffort`, and every service database has exactly the release's migration
set. Then run the non-financial live smoke script
with the digest-pin commit and released OCI digest:

```sh
scripts/alpha-post-deploy-smoke.sh \
  '<40-character-digest-pin-commit>' \
  'sha256:<64-hex-release-digest>'
```

## One-time wallet money schema `001` to `002` cutover

Wallet migration `002_groosh_money.sql` is intentionally not rolling-compatible
with the preceding wallet schema. Old processes omit the new required unit
identifiers and use strict row scans that do not accept the added columns; new
processes require the versioned currency catalog. The ordinary wave-16 migration
followed by a wave-20 rolling update is therefore unsafe for this one release.

Use a bounded stop-the-world cutover after the reviewed application image and
separate digest-promotion commit exist:

1. Record the exact old Git revision and image digest, the wallet migration set,
   money-bearing table counts, and open Deposit, Withdrawal, P2P, ManualTransfer,
   Reconciliation, and PSPStatusPoller workflow executions. Do not cross the
   boundary while a money-moving workflow is open.
2. Use a reviewed foundation plan to disable automated sync for the `noebs`
   Application. Require that only its `syncPolicy` changes and verify that
   automated prune and self-heal are absent before touching a Deployment.
3. Scale `wallet-ledger`, `wallet-worker`, and `psp-webhook` to zero. Wait for
   all five pods to disappear and require zero `pg_stat_activity` sessions for
   `wallet_ledger_runtime`, `wallet_ledger_worker`, and
   `wallet_ledger_webhook`. Recheck the workflow and database baselines. No
   Deposit, Withdrawal, P2P, or ManualTransfer execution may be open. The
   expected Reconciliation and PSPStatusPoller cron-backoff executions may
   remain open, but every current PSPStatusPoller history and pending-activity
   query must contain zero `GetTransactionStatus` activities before crossing
   the schema boundary.
4. While automation remains disabled, set foundation's
   `noebs_target_revision` to the exact 40-hex digest-promotion commit. Review
   and apply that plan, then request one explicit Argo CD sync at that revision.
   Do not issue an unqualified foundation apply during the outage.
5. Require the wave-16 `noebs-wallet-ledger-migrate` hook to succeed and the
   wallet migration set to contain applied versions `001` and `002` before accepting the
   new wave-20 writer pods. Verify their source revision and image ID rather
   than trusting the tag or desired manifest alone.
6. The recurring Temporal Cron execution waits until its next scheduled UTC
   time on first creation. After the new worker is Ready, start one explicit
   `FXReferenceSync` on task queue `wallet-main` with a distinct immutable ID
   `wallet-fx-reference-bootstrap-<40-character-promotion-sha>`; never reuse the
   recurring ID `wallet-fx-reference-sync`. Run it from the existing
   `temporal-namespace-bootstrap` identity and network boundary by invoking
   `scripts/alpha-fx-bootstrap.sh <40-character-promotion-sha>` on the trusted
   host. Require one fresh observation for each enabled ECB pair (EUR/CHF,
   EUR/GBP, EUR/JPY, and EUR/USD), then exercise a reference quote with an
   authenticated wallet user.
7. Run the post-deploy smoke, persist the exact target revision in foundation,
   and restore automated prune and self-heal through a reviewed saved plan.
   Finish with both Applications `Synced`/`Healthy` at the promotion revision
   and an empty unqualified foundation plan.

Before step 4, rollback means restoring the retained old revision and replicas
while automation is still disabled. After migration `002` succeeds, do not run
an application-only rollback or a down migration on a database that may have
accepted version-bound writes. Keep writers stopped and forward-fix with a
schema-aware immutable image. Restore from a pre-cutover database backup only
under a separately reviewed data-loss procedure.

## Rollback boundary

Roll back application content by reverting the digest-pin commit and allowing
Argo CD to sync the previous immutable digest. Do not use `kubectl set image`:
self-heal will overwrite it. Confirm the previous digest still resolves in
GHCR before starting the release.

Database migration is a separate boundary. The Keycloak cutover deliberately
resets the resettable Noebs service databases and creates the consolidated
identity projection schema `001` with Keycloak as the sole identity authority. A pre-cutover binary
is incompatible with that schema and trust boundary and is not a rollback
candidate.

Migration Jobs run before wave-20 workloads, so the coordinated reset and
rollout have a bounded interruption window. The overlay uses `Recreate` for
the gateway and schema-coupled identity/card workloads to prevent mixed
security models from serving concurrently. Retain the immutable image and
receipt that created and verified the current schemas as the rollback floor.
For an incompatible schema change, deploy a forward fix; Argo CD rollback does
not run down migrations or reconstruct removed credential data.

Noebs images are pulled through the explicit `ghcr-credentials` image pull Secret. The release input `noebs.ghcr_dockerconfigjson` must contain a Docker config JSON with `auths.ghcr.io.auth`; the renderer emits it as a `kubernetes.io/dockerconfigjson` Secret with the `.dockerconfigjson` key.

The host-network edge Caddy deployment is the sole public TLS and routing
authority. It sends Noebs API traffic to `api-gateway` and the explicit public
Keycloak paths to `keycloak`; this overlay creates no Ingress and no public TLS
Secret. The edge release requires `edge/edge-internal-transport` with
`ca.pem`, `tls.crt`, and `tls.key`; Caddy uses that exact client identity and
the `api-gateway.noebs.svc.cluster.local` server name for the gateway hop.
Internal HTTP routing is owned by the gateway through
`noebs.service_discovery` in the mounted config. Wallet API to wallet ledger
gRPC routing uses `noebs.grpc_service_discovery.wallet-ledger`.

Render check:

```sh
kubectl kustomize deploy/kubernetes/overlays/current-host
```
