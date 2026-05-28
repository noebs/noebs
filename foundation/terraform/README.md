# noebs Foundation OpenTofu

This root owns platform-level deployment wiring for the existing host `100.102.164.34`:

- install Argo CD into the configured cluster;
- create the `noebs` namespace for microservice workloads;
- create the noebs Argo CD project;
- create the noebs Argo CD application pointing at `deploy/kubernetes/overlays/current-host`.

Defaults are pinned for the current host, namespace names, Argo CD chart version, repository, branch, and manifest path. Override them only when intentionally moving infrastructure ownership.

Commands:

```sh
tofu -chdir=foundation/terraform init
tofu -chdir=foundation/terraform plan
tofu -chdir=foundation/terraform apply
```

The Kubernetes cluster itself must already be reachable through `kubeconfig_path`. Cluster bootstrap for the host happens before applying this root so OpenTofu can manage Argo CD through the Kubernetes API.

Runtime secrets are not stored in OpenTofu. Before syncing the Argo CD application, create the required Kubernetes Secrets in the OpenTofu-owned `noebs` namespace:

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

`api-gateway-secrets` carries edge auth/admin material only; it must not include `noebs.db_url`.
`wallet-api-secrets` carries wallet HTTP facade auth/admin material only; it must not include `noebs.db_url`.
`wallet-worker-secrets` carries worker-specific PSP credentials and the `wallet-ledger` service database owner entry; wallet-worker does not own a database or migration role.
`keycloak-secrets` carries Keycloak's own `keycloak.conf`; no noebs auth data is wired to Keycloak yet.

For Docker Compose cutovers on the current host, run `noebs validate-deployment /path/to/noebs-release` against the prepared release directory before replacing the old project. It validates the same explicit config and secret contracts locally, including per-service database ownership, Keycloak inputs, Temporal/Postgres password files, non-reserved tenant IDs, and EBS adapter endpoint/app-id requirements.

The `noebs_service_discovery` output is the explicit platform service catalog for every Kubernetes Service in the noebs base. The `noebs_database_ownership` output lists each service-owned database, including Temporal's `temporal` and `temporal_visibility` schemas migrated by `temporal-schema-migrate`.
