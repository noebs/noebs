# current-host Overlay

This overlay targets the existing deployment host `100.102.164.34`.

The `noebs` namespace is owned by `foundation/terraform`; this overlay only renders namespaced runtime resources.

Required DNS records:

- `api.noebs.sd A 100.102.164.34`
- `dsa.adonese.sd A 100.102.164.34`

Required Kubernetes Secrets in namespace `noebs`:

- `api-gateway-secrets` with key `secrets.yaml`.
- `identity-auth-secrets` with key `secrets.yaml`.
- `keycloak-secrets` with key `keycloak.conf`.
- `keycloak-postgres-credentials` with key `password`.
- `card-vault-secrets` with key `secrets.yaml`.
- `ebs-adapter-secrets` with key `secrets.yaml`.
- `psp-webhook-secrets` with key `secrets.yaml`.
- `admin-reporting-secrets` with key `secrets.yaml`.
- `notification-chat-secrets` with key `secrets.yaml`.
- `consumer-beneficiary-secrets` with key `secrets.yaml`.
- `wallet-api-secrets` with key `secrets.yaml`.
- `wallet-ledger-secrets` with key `secrets.yaml`.
- `wallet-worker-secrets` with key `secrets.yaml`.
- `sops-age-key` with key `age-key.txt`.
- `postgres-credentials` with key `password`.
- `temporal-postgres-credentials` with key `password`.
- `ghcr-credentials` with key `.dockerconfigjson`.
- `noebs-tls` TLS secret for `api.noebs.sd` and `dsa.adonese.sd`.

Render these Secrets from the prepared Kubernetes release input directory:

```sh
noebs prepare-kubernetes-release /path/to/noebs-repo /path/to/current-noebs-root /path/to/kubernetes-release.inputs.yaml /path/to/noebs-kubernetes-release
```

`kubernetes-release.inputs.yaml.example` documents the required encrypted cutover input shape. The real file must be SOPS-encrypted with the same Age key used by the current Noebs root.

To avoid duplicating values already transformed from the current encrypted root, render a current-secret-aware template first. The template prints placeholders for missing cutover fields only and never prints current secret values:

```sh
noebs render-kubernetes-release-input-template /path/to/current-noebs-root > /path/to/kubernetes-release.inputs.yaml.plain
```

Fill the placeholders, then encrypt the real `kubernetes-release.inputs.yaml` with the current root Age recipient. Do not add values already omitted from the template; those are transformed from the current encrypted root.

The audit/template path reports field names only. `current_secret` means the current encrypted root has a non-empty value that can be transformed; `empty_current_secret` means the key exists there but still needs explicit cutover input because the value is empty; `unsupported_current_secret` means legacy material exists but is intentionally not transformed.

```sh
noebs render-kubernetes-secrets /path/to/noebs-kubernetes-release noebs /path/to/tls.crt /path/to/tls.key | kubectl apply -f -
```

The preparation command reads the repo Kubernetes config/service role files, transforms values already present in the current encrypted Noebs root, decrypts the explicit encrypted cutover input file for required values absent from the current secret, encrypts generated service secret files with SOPS, and validates the output root. Duplicate non-empty values across the current secret and cutover input are rejected. The renderer then validates the Kubernetes release layout again before it emits Kubernetes `Secret` manifests. The input directory must contain exactly `config.yaml`, `.sops/age-key.txt`, one `services/<role>.yaml` file for every runtime and migration role mounted by the preflight Job, one `secrets/<service>.secrets.yaml` file per service-owned secret, and explicit platform files under `platform/`: `postgres-password.txt`, `temporal-postgres-password.txt`, `keycloak-postgres-password.txt`, `ghcr-dockerconfigjson`, and `keycloak.conf`. Missing files, extra top-level, `.sops`, platform, service config, or service secret entries, placeholders, invalid TLS material, invalid GHCR Docker config JSON, extra or missing service discovery entries, and incomplete service database/EBS/Keycloak inputs fail before any manifest is written.

Each noebs service secret must contain the merged secret material expected by that service, including `noebs.default_tenant_id`, JWT/admin keys it owns, EBS credentials it owns, data key material it owns, and PSP secrets it owns. Database-opening service secrets must include `noebs.service_databases` keyed only by the database owner role. Runtime config copies the owner URL into `noebs.db_url` for that role and rejects non-owner database entries. `api-gateway-secrets` and `wallet-api-secrets` must not contain `noebs.db_url` or `noebs.service_databases`. `wallet-worker-secrets` uses the `wallet-ledger` owner key because wallet-ledger owns wallet state; the worker has no separate database or migration scope.

`ebs-adapter-secrets` must provide explicit resolved EBS runtime values: `consumer_endpoint`, `merchant_endpoint`, `ipin_endpoint`, `consumer_app_id`, `merchant_app_id`, `ipin_username`, `ipin_password`, `pub_key`, `ipin_key`, `pan`, `pin`, `ipin`, and `exp_date`. The runtime does not pick QA or production endpoints from mode booleans. EBS dynamic fees are explicit shared runtime config in `noebs-config` under `noebs.ebs_dynamic_fees`; do not move them into code defaults.

`keycloak-secrets` is not a noebs merged secret. It must contain a Keycloak `keycloak.conf` file with its database, hostname, bootstrap admin, health, and metrics configuration; `deploy/kubernetes/base/keycloak.conf.example` shows the required keys. Keycloak is deployed now as an independent auth platform service; no noebs auth data is wired to it yet.

When using the in-cluster `postgres` StatefulSet, service database URLs should point at the owned database names created by the init script: `identity_auth`, `card_vault`, `ebs_adapter`, `psp_webhook`, `admin_reporting`, `notification_chat`, `consumer_beneficiary`, and `wallet_ledger`.

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

The CI workflow publishes an image only after a push to `master` passes the
full test and race-test jobs. It publishes both `ghcr.io/noebs/noebs:master`
and `ghcr.io/noebs/noebs:<git-sha>` to one OCI manifest digest. Release in two
Git commits so Argo CD never deploys code merely because `master` moved:

1. Merge the application release commit and wait for its `CI` workflow to
   succeed. Record the full source SHA as `RELEASE_SHA`.
2. Read the `containerimage.digest` emitted by that workflow. With a temporary
   Docker config containing the existing GHCR pull credential, independently
   verify that `ghcr.io/noebs/noebs:$RELEASE_SHA` resolves to the same digest.
3. Change only the Noebs `digest:` value in `kustomization.yaml`. Render the
   overlay and confirm every Noebs runtime, preflight, and migration image uses
   `ghcr.io/noebs/noebs@sha256:<digest>`.
4. Retain that tested digest as the rollback floor and announce a controlled
   cutover window. Identity migration 103 and card-vault migration 104 are not
   expand-compatible with the older binary, so the migration-hook-to-wave-20
   interval can reject requests and the pre-alpha baseline is not a valid
   post-migration rollback target.
5. Commit the digest pin, push it to `master`, and let the automated Argo CD
   sync run its preflight and migration hooks before wave-20 workloads. Do not
   start the sync unless an operator can watch it through the post-deploy smoke
   and forward-fix or redeploy the retained schema-aware digest if it fails.

Example verification on the deployment host (the temporary credential file is
deleted on exit and its contents must never be printed):

```sh
release_sha='<full-git-sha>'
release_digest='sha256:<64-hex-digest>'
docker_config="$(mktemp -d)"
trap 'find "$docker_config" -depth -delete' EXIT
kubectl -n noebs get secret ghcr-credentials \
  -o jsonpath='{.data.\.dockerconfigjson}' \
  | base64 -d > "$docker_config/config.json"
DOCKER_CONFIG="$docker_config" docker buildx imagetools inspect \
  "ghcr.io/noebs/noebs:$release_sha"
kubectl kustomize deploy/kubernetes/overlays/current-host > /tmp/noebs-rendered.yaml
grep -F "ghcr.io/noebs/noebs@$release_digest" /tmp/noebs-rendered.yaml
kubectl apply --dry-run=server -f /tmp/noebs-rendered.yaml >/dev/null
```

After Argo CD reports `Synced` and `Healthy`, verify that its revision is the
digest-pin commit, every Deployment and StatefulSet has completed rollout, all
Noebs pod `imageID` values end in the expected digest, no runtime pod is
`BestEffort`, and the identity/card-vault migration versions are at least the
versions required by the release. Then run the non-financial live smoke script
with the digest-pin commit and released OCI digest:

```sh
scripts/alpha-post-deploy-smoke.sh \
  '<40-character-digest-pin-commit>' \
  'sha256:<64-hex-release-digest>'
```

## Rollback boundary

Roll back application content by reverting the digest-pin commit and allowing
Argo CD to sync the previous immutable digest. Do not use `kubectl set image`:
self-heal will overwrite it. Confirm the previous digest still resolves in
GHCR before starting the release.

Database migrations are a separate boundary. Identity migration 103 adds a
`users` column and changes the OTP challenge key, while card-vault migration
104 adds token columns. Pre-alpha binaries use strict `SELECT *` scans and the
old OTP conflict target, so they are not compatible with either advanced
schema. The migration Jobs run before the wave-20 workloads, which also means
this alpha cutover has a bounded service-interruption risk between migration
and successful rollout. Once either migration has run, the baseline digest
above is not a safe application rollback target. The release must retain a
tested, immutable schema-aware digest as its rollback floor; after recovery or
payment traffic starts, prefer a forward fix rather than dropping state. There
is no automatic down-migration in the Argo CD rollback path.

Noebs images are pulled through the explicit `ghcr-credentials` image pull Secret. The release input `noebs.ghcr_dockerconfigjson` must contain a Docker config JSON with `auths.ghcr.io.auth`; the renderer emits it as a `kubernetes.io/dockerconfigjson` Secret with the `.dockerconfigjson` key.

The public Ingress routes both hostnames only to `api-gateway`. Internal HTTP routing is owned by the gateway through `noebs.service_discovery` in the mounted config. Wallet API to wallet ledger gRPC routing uses `noebs.grpc_service_discovery.wallet-ledger`.

Render check:

```sh
kubectl kustomize deploy/kubernetes/overlays/current-host
```
