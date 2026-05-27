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
- `wallet-ledger-secrets` with key `secrets.yaml`.
- `sops-age-key` with key `age-key.txt`.
- `postgres-credentials` with key `password`.
- `temporal-postgres-credentials` with key `password`.
- `noebs-tls` TLS secret for `api.noebs.sd` and `dsa.adonese.sd`.

Each noebs service secret must contain the merged secret material expected by that service, including `noebs.default_tenant_id`, JWT/admin keys it owns, EBS credentials it owns, data key material it owns, and PSP secrets it owns. Every database-opening service secret must also contain that service's `noebs.db_url`. `api-gateway-secrets` must not contain `noebs.db_url`. Wallet API, wallet ledger, and wallet worker share `wallet-ledger-secrets` because ledger is the database owner for wallet state.

`keycloak-secrets` is not a noebs merged secret. It must contain a Keycloak `keycloak.conf` file with its database, hostname, bootstrap admin, health, and metrics configuration; `deploy/kubernetes/base/keycloak.conf.example` shows the required keys. Keycloak is deployed now as an independent auth platform service; no noebs auth data is wired to it yet.

When using the in-cluster `postgres` StatefulSet, service `noebs.db_url` values should point at the owned database names created by the init script: `identity_auth`, `card_vault`, `ebs_adapter`, `psp_webhook`, `admin_reporting`, `notification_chat`, `consumer_beneficiary`, and `wallet_ledger`.

Noebs service roles are selected by mounted config, not environment variables. The base `noebs-config` ConfigMap provides shared `config.yaml` and one `*.service.yaml` key per workload and migration job.

The public Ingress routes both hostnames only to `api-gateway`. Internal service routing is owned by the gateway through `noebs.service_discovery` in the mounted config.

Render check:

```sh
kubectl kustomize deploy/kubernetes/overlays/current-host
```
