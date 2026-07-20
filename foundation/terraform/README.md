# noebs Foundation OpenTofu

This root owns platform-level deployment wiring for the existing host `100.102.164.34`:

- install Argo CD into the configured cluster or bind to the existing Argo CD install on the current host;
- create the `noebs` namespace for microservice workloads;
- create the `edge` namespace before any release-owned edge credentials are applied;
- create the noebs Argo CD project;
- create the noebs Argo CD application pointing at the explicit bootstrap or steady manifest path;
- optionally create the `noebs-edge` Argo CD application pointing at
  `deploy/kubernetes/edge`, so the host-network Caddy deployment and its full
  shared-host configuration are pruned and self-healed from Git.

Every input is explicit. Copy `terraform.tfvars.example` to `terraform.tfvars`, review each value, and keep the chosen host, kubeconfig, namespaces, Argo CD installation mode, Argo CD chart version, repository, exact lowercase 40-hex promotion commit, manifest path, and application-creation phase in that file rather than relying on OpenTofu defaults. Branches and tags are rejected for `noebs_target_revision`; the Noebs and edge Applications must resolve the same immutable commit. The current host already has Argo CD installed, so its explicit mode is `existing`; use `helm` only when foundation should create the Argo CD Helm release on a fresh cluster.

For an existing Argo CD installation, initialize and apply normally:

```sh
tofu -chdir=foundation/terraform init
tofu -chdir=foundation/terraform plan
tofu -chdir=foundation/terraform apply
```

A fresh cluster using `argocd_installation_mode = "helm"` requires three
explicit applies. The Kubernetes provider cannot plan an `AppProject` until
the Helm-installed Argo CD CRDs exist; apply ordering inside one plan cannot
solve schema discovery that happens while planning.

```sh
tofu -chdir=foundation/terraform init
tofu -chdir=foundation/terraform apply \
  -target=kubernetes_namespace_v1.argocd
tofu -chdir=foundation/terraform apply \
  -target=helm_release.argocd
kubectl wait --for=condition=Established --timeout=2m \
  crd/applications.argoproj.io crd/appprojects.argoproj.io
tofu -chdir=foundation/terraform plan -out=foundation.tfplan
tofu -chdir=foundation/terraform apply foundation.tfplan
```

The targeted applies bootstrap only the namespace and pinned Helm release.
The final full plan establishes the `noebs` and `edge` namespaces and the
AppProject with both applications still controlled by their explicit gates.

The Kubernetes cluster itself must already be reachable through `kubeconfig_path`. Cluster bootstrap for the host happens before applying this root so OpenTofu can manage Argo CD through the Kubernetes API.

## Remove legacy Secret data from live state

The previous foundation read Kubernetes Secrets through
`data.kubernetes_secret_v1` resources, which persisted Secret values in the
live state and in saved plans/backups. Sanitize that state before any cutover
plan. Do not run `tofu state show`, `tofu show`, `jq`, or text tools against the
old artifacts. Resource addresses may be listed; values must never be printed.

The current host's live local state is
`/home/adonese/src/noebs-foundation/foundation/terraform/terraform.tfstate`;
the application checkout does not own it. Relocate that state into the exact
reviewed release checkout before running any command from the new foundation
code. `STATE_QUARANTINE` must be on an approved encrypted filesystem and
outside both repositories.

```bash
set -euo pipefail
umask 077
: "${STATE_QUARANTINE:?set a protected encrypted quarantine directory}"
: "${RELEASE_REPO_ROOT:?set the clean reviewed release checkout}"
legacy_repo_root=/home/adonese/src/noebs-foundation
legacy_foundation_root=/home/adonese/src/noebs-foundation/foundation/terraform
release_foundation_root="$RELEASE_REPO_ROOT/foundation/terraform"

test -d "$release_foundation_root"
git -C "$RELEASE_REPO_ROOT" diff --quiet
git -C "$RELEASE_REPO_ROOT" diff --cached --quiet
test -z "$(git -C "$RELEASE_REPO_ROOT" ls-files --others --exclude-standard)"
test -s "$legacy_foundation_root/terraform.tfstate"
test -s "$legacy_foundation_root/terraform.tfvars"
test -s "$legacy_foundation_root/terraform.tfvars.example"
test -s "$release_foundation_root/terraform.tfvars.example"
git -C "$legacy_repo_root" ls-files --error-unmatch \
  foundation/terraform/terraform.tfvars.example >/dev/null
git -C "$RELEASE_REPO_ROOT" ls-files --error-unmatch \
  foundation/terraform/terraform.tfvars.example >/dev/null
test ! -e "$release_foundation_root/terraform.tfstate"
test ! -e "$release_foundation_root/terraform.tfvars"
test ! -e "$STATE_QUARANTINE"
install -d -m 0700 "$STATE_QUARANTINE"

install -m 0600 "$legacy_foundation_root/terraform.tfstate" \
  "$STATE_QUARANTINE/pre-relocation.tfstate"
install -m 0600 "$legacy_foundation_root/terraform.tfvars" \
  "$release_foundation_root/terraform.tfvars"
mv -- "$legacy_foundation_root/terraform.tfstate" \
  "$release_foundation_root/terraform.tfstate"
chmod 0600 "$release_foundation_root/terraform.tfstate"
cmp -s "$STATE_QUARANTINE/pre-relocation.tfstate" \
  "$release_foundation_root/terraform.tfstate"
mv -- "$legacy_foundation_root/terraform.tfvars" \
  "$STATE_QUARANTINE/legacy-terraform.tfvars"
test ! -e "$legacy_foundation_root/terraform.tfstate"
test ! -e "$legacy_foundation_root/terraform.tfvars"

for artifact_root in "$legacy_foundation_root" "$release_foundation_root"; do
  artifact_prefix=release
  [[ "$artifact_root" == "$legacy_foundation_root" ]] && artifact_prefix=legacy
  while IFS= read -r -d '' artifact; do
    destination="$STATE_QUARANTINE/$artifact_prefix-$(basename -- "$artifact")"
    test ! -e "$destination"
    mv -- "$artifact" "$destination"
    chmod 0600 "$destination"
  done < <(find "$artifact_root" -maxdepth 1 -type f \
    \( -name 'terraform.tfstate.backup' -o -name '*.tfplan' \
       -o -name '*.plan' -o -name 'errored.tfstate' -o -name 'crash.log' \
       -o \( -name 'terraform.tfvars.*' \
         ! -name 'terraform.tfvars.example' \) \) -print0)
done

test -s "$legacy_foundation_root/terraform.tfvars.example"
test -s "$release_foundation_root/terraform.tfvars.example"
git -C "$RELEASE_REPO_ROOT" diff --quiet
git -C "$RELEASE_REPO_ROOT" diff --cached --quiet
test -z "$(git -C "$RELEASE_REPO_ROOT" ls-files --others --exclude-standard)"

${EDITOR:?set EDITOR} "$release_foundation_root/terraform.tfvars"
tofu -chdir="$release_foundation_root" init
tofu -chdir="$release_foundation_root" state pull \
  > "$STATE_QUARANTINE/pre-removal.tfstate"
chmod 0600 "$STATE_QUARANTINE/pre-removal.tfstate"
tofu -chdir="$release_foundation_root" state list \
  | grep -Fx 'kubernetes_namespace_v1.noebs' >/dev/null
tofu -chdir="$release_foundation_root" state list \
  | grep -Fx 'kubernetes_manifest.noebs_project' >/dev/null

mapfile -t legacy_secret_addresses < <(
  tofu -chdir="$release_foundation_root" state list \
    | awk '/(^|\.)data\.kubernetes_secret_v1\./'
)
((${#legacy_secret_addresses[@]} > 0))

tofu -chdir="$release_foundation_root" state rm -dry-run \
  "${legacy_secret_addresses[@]}" \
  > "$STATE_QUARANTINE/state-rm.dry-run.log"
tofu -chdir="$release_foundation_root" state rm \
  -backup="$STATE_QUARANTINE/state-rm.automatic-backup.tfstate" \
  "${legacy_secret_addresses[@]}" \
  > "$STATE_QUARANTINE/state-rm.log"
chmod 0600 "$STATE_QUARANTINE"/*

tofu -chdir="$release_foundation_root" state pull \
  > "$STATE_QUARANTINE/post-removal.tfstate"
tofu -chdir="$release_foundation_root" state list \
  | grep -Fx 'kubernetes_namespace_v1.noebs' >/dev/null
tofu -chdir="$release_foundation_root" state list \
  | grep -Fx 'kubernetes_manifest.noebs_project' >/dev/null
python3 - "$STATE_QUARANTINE/post-removal.tfstate" <<'PY'
import json
import pathlib
import sys

state = json.loads(pathlib.Path(sys.argv[1]).read_text())
for resource in state.get("resources", []):
    if resource.get("type") in {"kubernetes_secret", "kubernetes_secret_v1"}:
        raise SystemExit("Kubernetes Secret data remains in OpenTofu state")
PY

! rg -n 'data "kubernetes_secret(_v1)?"' "$release_foundation_root"/*.tf
tofu -chdir="$release_foundation_root" plan \
  -out="$STATE_QUARANTINE/post-removal.tfplan" \
  > "$STATE_QUARANTINE/post-removal-plan.log" 2>&1
chmod 0600 "$STATE_QUARANTINE"/*
```

The editor step must reconcile the relocated current-host values against every
field in `terraform.tfvars.example`, including the exact 40-hex revision, edge
namespace/path, and both application-creation gates. The following plan is the
first new-code read of the relocated state; an empty-state plan is a hard stop.

The explicit `-backup` captures OpenTofu's automatic state-removal backup in
quarantine instead of leaving another `terraform.tfstate.backup` beside the
working root. Keep the pre-removal state, automatic backup, and protected plans
only through the reviewed recovery window. Then retire the entire quarantine
and every older plan/state copy under the encrypted-media retention policy,
including filesystem snapshots and external backups. Plain `rm` or `shred` is
not a secure erasure boundary for unencrypted SSD or snapshot storage; if the
quarantine was not encrypted, stop and use approved media destruction or
cryptographic erasure. Never copy these artifacts into the repository.

Runtime secrets must not enter OpenTofu state. Bootstrap the foundation with
`create_noebs_application = false` and `noebs_automated_sync = false` while
preparing release Secrets; this still creates Argo CD, the Noebs namespace,
and the Noebs AppProject. OpenTofu deliberately does not read Kubernetes Secret
objects: provider data sources persist their full `.data` maps in state. Secret
key, value, and type validation belongs to the local renderer and the wave-0
deployment preflight.

For an empty Keycloak database, apply the temporary bootstrap Secrets, set
`noebs_manifest_path = "deploy/kubernetes/overlays/bootstrap-current-host"`,
set `create_noebs_application = true` and `noebs_automated_sync = true`, then
apply the reviewed plan. After the bootstrap Application is `Synced` and
`Healthy` and its wave-6 deletion Job succeeds, change only
`noebs_manifest_path` to `deploy/kubernetes/overlays/current-host` and apply the
next reviewed plan. Wait for the steady realm-local reconcile before deleting
the temporary bootstrap Secrets. A non-empty Keycloak database uses the steady
path directly; the bootstrap path is not a fallback.

The current-host destructive first release must follow
[`deploy/host/keycloak-empty-state-cutover.md`](../../deploy/host/keycloak-empty-state-cutover.md).
It pauses and removes the Application before replacing the foundation-owned
namespace, then applies rotated Secrets before bootstrap and steady sync.

The static outputs document the release Secret names and keys expected by those boundary validators:

- `noebs-release-manifest` with key `release-manifest.yaml`
- `api-gateway-secrets` with key `secrets.yaml`
- `identity-auth-secrets` with key `secrets.yaml`
- `keycloak-secrets` with keys `keycloak.conf`, `db-ca.pem`, `tls.crt`, and `tls.key`
- `keycloak-transport-ca` with public key `ca.pem`
- `keycloak-reconciler-credentials` with key `config.yaml`
- `keycloak-postgres-credentials` with keys `password`, `tls.crt`, and `tls.key`
- `card-vault-secrets` with key `secrets.yaml`
- `ebs-adapter-secrets` with key `secrets.yaml`
- `psp-webhook-secrets` with key `secrets.yaml`
- `admin-reporting-secrets` with key `secrets.yaml`
- `notification-chat-secrets` with key `secrets.yaml`
- `wallet-api-secrets` with key `secrets.yaml`
- `wallet-ledger-secrets` with key `secrets.yaml`
- `wallet-worker-secrets` with key `secrets.yaml`
- `ebs-adapter-events-secrets`, `admin-reporting-projector-secrets`, `workload-auth-migrate-secrets`, `workload-auth-cleanup-secrets`, `gateway-auth-migrate-secrets`, and `gateway-auth-cleanup-secrets`, each with key `secrets.yaml`
- `identity-auth-migrate-secrets`, `card-vault-migrate-secrets`, `ebs-adapter-migrate-secrets`, `admin-reporting-migrate-secrets`, `notification-chat-migrate-secrets`, and `wallet-ledger-migrate-secrets`, each with key `secrets.yaml`
- `workload-auth-postgres-roles` with key `roles.yaml`
- `gateway-auth-postgres-roles` with key `roles.yaml`
- `service-postgres-roles` with keys `passwords.env`, `bootstrap.sql`, and `roles.yaml`
- `internal-transport-platform` with key `credentials.yaml`
- `postgres-credentials` with keys `ca.pem`, `tls.crt`, and `tls.key`
- `temporal-postgres-credentials` with keys `password`, `ca.pem`, `tls.crt`, and `tls.key`
- `temporal-server-credentials` with keys `ca.pem`, `tls.crt`, and `tls.key`
- `temporal-namespace-bootstrap-credentials` with keys `ca.pem` and `client-secret`
- `ghcr-credentials` with key `.dockerconfigjson`

The `noebs_required_kubernetes_secrets` and `noebs_required_kubernetes_secret_keys` outputs expose names and key shape only. They do not cause OpenTofu to read live Secret values.

`api-gateway-secrets` carries the confidential back-office client,
session-encryption keyring, opaque PSP callback routes, and least-privilege
gateway session database URL.
`wallet-api-secrets` carries wallet HTTP facade auth/admin material only; it must not include `noebs.db_url`.
`wallet-worker-secrets` carries worker-specific PSP credentials and the `wallet-ledger` service database owner entry. `psp-webhook-secrets` carries the same owner key with the exact `wallet_ledger_webhook` URL. Neither process owns a database or migration role; PSP tables belong to the wallet-ledger migration scope.
`keycloak-secrets` carries Keycloak's steady configuration, database CA, and its distinct HTTPS server identity. `keycloak-transport-ca` carries only the release CA certificate consumed by the gateway and reconciliation jobs.
`keycloak-reconciler-credentials` carries the realm-local reconciler client,
the confidential back-office client credential, and the configured identity
provider credential in `config.yaml`; it never contains the temporary
master-realm bootstrap client.

## Edge GitOps adoption

The existing current-host Caddy deployment is a separate release component in
the foundation-owned `edge` namespace because it owns the host listeners on 80 and 443 and
serves other hostnames in addition to Noebs. Keep
`create_edge_application = false` during
foundation bootstrap or while reviewing an adoption. Before enabling it,
render `deploy/kubernetes/edge`, compare it with the live `edge/caddy`
Deployment and ConfigMap, and run a server-side dry-run. Then set
`create_edge_application = true`, review the OpenTofu plan, and apply it to
create the `noebs-edge` Application. OpenTofu creates the namespace before
release credentials or the Application; Argo CD thereafter prunes and
self-heals this exact kustomization.

The current host already has an `edge` namespace. Adopt it once after the
state-sanitization step and before the first plan from this revision:

```sh
tofu -chdir="$release_foundation_root" state list \
  | grep -Fx 'kubernetes_namespace_v1.edge' >/dev/null \
  || tofu -chdir="$release_foundation_root" import \
    kubernetes_namespace_v1.edge edge
```

Move the import-created `terraform.tfstate.backup`, if present, into the same
protected state quarantine before continuing. A fresh cluster does not import;
the full foundation apply creates the namespace.

Before the edge Application rolls out, render and apply its dynamic transport
Secret from the validated release: `noebs render-edge-internal-transport
/path/to/noebs-kubernetes-release edge | kubectl apply -f -`. This Secret is
labeled as release-renderer-owned and contains the public CA plus the exact
`edge` client certificate and private key. Argo owns the Deployment and
content-addressed Caddy configuration, not this release identity.

The edge Deployment uses `Recreate` because only one host-network pod can own
the listeners. A pod-template difference during the first Argo sync causes a
brief public-edge interruption, so adopt it in the same controlled cutover
window as the alpha release and verify `/.well-known/assetlinks.json`
immediately after the sync. Do not leave a second manual edge apply
workflow active after adoption.

For Docker Compose cutovers on the current host, run `noebs validate-deployment /path/to/noebs-release` against the prepared release directory before replacing the old project. It validates the same explicit config and secret contracts locally, including the exact service role file set, absence of extra real service secret files, exact HTTP/gRPC service discovery catalogs, per-service database ownership, Keycloak inputs, Temporal/Postgres password files, non-reserved tenant IDs, and EBS adapter endpoint/app-id/IPIN/key/bill-inquiry requirements.

Before syncing the Argo CD application, render the runtime Kubernetes Secrets from a prepared release directory containing `config.yaml`, `tenant-catalog.yaml`, `release-manifest.yaml`, runtime and migration role files under `services/*.yaml`, SOPS-encrypted service files under `secrets/*.secrets.yaml`, `.sops/age-key.txt`, and platform files under `platform/`. The renderer validates that source set, decrypts each service into its own Secret, omits the age identity, and writes a distinct fingerprint for the exact plaintext files mounted by in-cluster preflight. Extra source or rendered artifacts are rejected.

Build the directory from one strict SOPS-encrypted authority input. The preparation command reads tracked Kubernetes config, service roles, and tenant catalog from the repo, decrypts only the named input with the named age identity, writes an empty output directory, encrypts service secrets with SOPS, records a release-wide artifact fingerprint, and immediately validates the result.

```sh
noebs prepare-kubernetes-release /path/to/noebs-repo /path/to/kubernetes-release.inputs.yaml /path/to/age-key.txt /path/to/noebs-kubernetes-release
```

The input is the sole release authority. It explicitly supplies the exact 22-role/eight-database credential catalog, the realm-local reconciler and confidential back-office and wallet-authorization client credentials, gateway encryption keyring, workload signing keys, transport CA, Google identity-provider credentials, GHCR Docker config, card-vault key, opaque PSP callback IDs and provider secrets, and resolved EBS values. Missing values fail preparation; normal preparation never generates or recovers authority from another root.

`deploy/kubernetes/overlays/current-host/kubernetes-release.inputs.yaml.example` documents the required input keys. The real input file is secret material and must be SOPS-encrypted before use.

```sh
noebs render-kubernetes-secrets /path/to/noebs-kubernetes-release noebs | kubectl apply -f -
```

The generated manifests are sensitive deployment output and must not be committed.

For the first sync of an empty Keycloak database, create one additional SOPS-encrypted input containing only the temporary bootstrap client secret:

```yaml
api_version: noebs.sd/keycloak-bootstrap/v1
client_secret: REPLACE_WITH_CANONICAL_32_BYTE_BASE64URL_SECRET
```

Render the two temporary bootstrap Secrets to an exclusive mode-0600 file. The renderer derives every other reconciler value from the validated steady release and never writes secret values to stdout:

```sh
noebs render-keycloak-bootstrap-secrets /path/to/noebs-kubernetes-release noebs /path/to/keycloak-bootstrap.inputs.yaml /path/to/keycloak-bootstrap.secrets.yaml
kubectl apply -f /path/to/keycloak-bootstrap.secrets.yaml
```

For Kubernetes cutovers, Argo CD runs `noebs-deployment-preflight` at sync wave 0 before Temporal and service migration jobs. The hook runs `noebs validate-kubernetes-deployment /preflight` against mounted ConfigMap and Secret files, so missing or placeholder platform/service inputs fail the sync before any schema changes run.

The `noebs_service_discovery` output is the explicit platform service catalog for every Kubernetes Service in the noebs base. The `noebs_database_ownership` output lists the eight Noebs service databases plus Temporal's `temporal` and `temporal_visibility` schemas migrated by `temporal-schema-migrate`. There is no `psp_webhook` database; the HTTP service persists only its wallet-owned PSP model through `wallet_ledger_webhook`.
