# noebs Foundation OpenTofu

This root owns platform-level deployment wiring for the existing host `100.102.164.34`:

- install Argo CD into the configured cluster;
- create the `noebs` namespace for microservice workloads;
- create the noebs Argo CD project;
- create the noebs Argo CD application pointing at `deploy/kubernetes/overlays/current-host`.

Every input is explicit. Copy `terraform.tfvars.example` to `terraform.tfvars`, review each value, and keep the chosen host, kubeconfig, namespaces, Argo CD chart version, repository, branch, and manifest path in that file rather than relying on OpenTofu defaults.

Commands:

```sh
tofu -chdir=foundation/terraform init
tofu -chdir=foundation/terraform plan
tofu -chdir=foundation/terraform apply
```

The Kubernetes cluster itself must already be reachable through `kubeconfig_path`. Cluster bootstrap for the host happens before applying this root so OpenTofu can manage Argo CD through the Kubernetes API.

Runtime secrets are not stored in OpenTofu. Before applying the Noebs Argo CD application, create the required Kubernetes Secrets in the OpenTofu-owned `noebs` namespace. The foundation root reads this exact Secret name set as Kubernetes data sources before creating the Argo CD application, so missing release Secrets fail the OpenTofu run instead of starting an Argo CD sync with incomplete inputs:

- `api-gateway-secrets` with key `secrets.yaml`
- `identity-auth-secrets` with key `secrets.yaml`
- `keycloak-secrets` with key `keycloak.conf`
- `keycloak-postgres-credentials` with key `password`
- `card-vault-secrets` with key `secrets.yaml`
- `ebs-adapter-secrets` with key `secrets.yaml`
- `psp-webhook-secrets` with key `secrets.yaml`
- `admin-reporting-secrets` with key `secrets.yaml`
- `notification-chat-secrets` with key `secrets.yaml`
- `consumer-beneficiary-secrets` with key `secrets.yaml`
- `wallet-api-secrets` with key `secrets.yaml`
- `wallet-ledger-secrets` with key `secrets.yaml`
- `wallet-worker-secrets` with key `secrets.yaml`
- `sops-age-key` with key `age-key.txt`
- `postgres-credentials` with key `password`
- `temporal-postgres-credentials` with key `password`
- `noebs-tls` for `api.noebs.sd` and `dsa.adonese.sd`

The foundation root also checks each required Secret's expected data keys before it creates the Argo CD `Application`; `noebs-tls` must be a `kubernetes.io/tls` Secret and `ghcr-credentials` must be a `kubernetes.io/dockerconfigjson` Secret. The `noebs_required_kubernetes_secrets` and `noebs_required_kubernetes_secret_keys` outputs expose this required shape for deployment checks without storing any secret values in OpenTofu state.

`api-gateway-secrets` carries edge auth/admin material only; it must not include `noebs.db_url`.
`wallet-api-secrets` carries wallet HTTP facade auth/admin material only; it must not include `noebs.db_url`.
`wallet-worker-secrets` carries worker-specific PSP credentials and the `wallet-ledger` service database owner entry; wallet-worker does not own a database or migration role.
`keycloak-secrets` carries Keycloak's own `keycloak.conf`; no noebs auth data is wired to Keycloak yet.

For Docker Compose cutovers on the current host, run `noebs validate-deployment /path/to/noebs-release` against the prepared release directory before replacing the old project. It validates the same explicit config and secret contracts locally, including the exact service role file set, absence of extra real service secret files, exact HTTP/gRPC service discovery catalogs, per-service database ownership, Keycloak inputs, Temporal/Postgres password files, non-reserved tenant IDs, and EBS adapter endpoint/app-id/IPIN/key/bill-inquiry requirements.

Before syncing the Argo CD application, render the runtime Kubernetes Secrets from a Kubernetes release input directory. The directory must use the same file layout as the in-cluster preflight mount: `config.yaml`, runtime and migration role files under `services/*.yaml`, service secret files under `secrets/*.secrets.yaml`, `.sops/age-key.txt`, and platform files under `platform/`. Extra top-level, `.sops`, platform, service config, or service secret entries are rejected so stale monolith or compatibility inputs cannot be carried into a Kubernetes release.

To build that release input directory from the current server material, use the explicit preparation command. It reads Kubernetes config and service role files from the repo root, reads the current encrypted Noebs secret from the legacy root, decrypts an encrypted cutover input file for values that do not exist in the legacy secret, writes an empty output directory, encrypts generated service secret files with SOPS, and immediately validates the prepared Kubernetes release root.

```sh
noebs prepare-kubernetes-release /path/to/noebs-repo /path/to/current-noebs-root /path/to/kubernetes-release.inputs.yaml /path/to/noebs-kubernetes-release
```

The command fails instead of deriving missing values. For each cutover value, the current encrypted `secrets.yaml` wins when it already contains a non-empty value; the encrypted cutover inputs file supplies only values absent from the current secret. Duplicate non-empty values across the current secret and cutover input are rejected instead of being silently chosen. The legacy root must still supply the existing Postgres password through `noebs.db_url` and the JWT secret through `noebs.jwt_secret`; all other required service-owned values, including API gateway admin credentials, SMS provider values, Google OAuth client values, Keycloak bootstrap/database credentials, GHCR Docker config JSON, card-vault data key, PSP provider secrets, and resolved EBS endpoint/app-id/IPIN/key/bill-inquiry fields, may come from either current secret material or the encrypted cutover input.

`deploy/kubernetes/overlays/current-host/kubernetes-release.inputs.yaml.example` documents the required input keys. The real input file is secret material and must be SOPS-encrypted before use.

To audit the current encrypted root before preparing the release, run:

```sh
noebs audit-kubernetes-release-inputs /path/to/current-noebs-root
```

To include an encrypted cutover input file in the audit, run:

```sh
noebs audit-kubernetes-release-inputs /path/to/current-noebs-root /path/to/kubernetes-release.inputs.yaml
```

The audit reports only input names and sources. `current_secret` means a non-empty value can be transformed from the current encrypted root; `empty_current_secret` means the key exists there but has no usable value and still needs explicit cutover input. It does not print secret values, derive missing values, choose QA or production EBS endpoints, or make deployment changes.

```sh
noebs render-kubernetes-secrets /path/to/noebs-kubernetes-release noebs /path/to/tls.crt /path/to/tls.key | kubectl apply -f -
```

The generated manifests are sensitive deployment output and must not be committed.

For Kubernetes cutovers, Argo CD runs `noebs-deployment-preflight` at sync wave 0 before Temporal and service migration jobs. The hook runs `noebs validate-kubernetes-deployment /preflight` against mounted ConfigMap and Secret files, so missing or placeholder platform/service inputs fail the sync before any schema changes run.

The `noebs_service_discovery` output is the explicit platform service catalog for every Kubernetes Service in the noebs base. The `noebs_database_ownership` output lists each service-owned database, including Temporal's `temporal` and `temporal_visibility` schemas migrated by `temporal-schema-migrate`.
