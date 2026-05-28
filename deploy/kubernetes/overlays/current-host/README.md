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
- `noebs-tls` TLS secret for `api.noebs.sd` and `dsa.adonese.sd`.

Render these Secrets from the prepared Kubernetes release input directory:

```sh
noebs render-kubernetes-secrets /path/to/noebs-kubernetes-release noebs /path/to/tls.crt /path/to/tls.key | kubectl apply -f -
```

The renderer first validates the Kubernetes release layout, then emits Kubernetes `Secret` manifests. The input directory must contain exactly `config.yaml`, `.sops/age-key.txt`, one `services/<role>.yaml` file for every runtime and migration role mounted by the preflight Job, one `secrets/<service>.secrets.yaml` file per service-owned secret, and explicit platform files under `platform/`: `postgres-password.txt`, `temporal-postgres-password.txt`, `keycloak-postgres-password.txt`, and `keycloak.conf`. Missing files, extra top-level, `.sops`, platform, service config, or service secret entries, placeholders, invalid TLS material, extra or missing service discovery entries, and incomplete service database/EBS/Keycloak inputs fail before any manifest is written.

Each noebs service secret must contain the merged secret material expected by that service, including `noebs.default_tenant_id`, JWT/admin keys it owns, EBS credentials it owns, data key material it owns, and PSP secrets it owns. Database-opening service secrets must include `noebs.service_databases` keyed only by the database owner role. Runtime config copies the owner URL into `noebs.db_url` for that role and rejects non-owner database entries. `api-gateway-secrets` and `wallet-api-secrets` must not contain `noebs.db_url` or `noebs.service_databases`. `wallet-worker-secrets` uses the `wallet-ledger` owner key because wallet-ledger owns wallet state; the worker has no separate database or migration scope.

`ebs-adapter-secrets` must provide explicit resolved EBS runtime values: `consumer_endpoint`, `merchant_endpoint`, `ipin_endpoint`, `consumer_app_id`, and `merchant_app_id`. The runtime does not pick QA or production endpoints from mode booleans. EBS dynamic fees are explicit shared runtime config in `noebs-config` under `noebs.ebs_dynamic_fees`; do not move them into code defaults.

`keycloak-secrets` is not a noebs merged secret. It must contain a Keycloak `keycloak.conf` file with its database, hostname, bootstrap admin, health, and metrics configuration; `deploy/kubernetes/base/keycloak.conf.example` shows the required keys. Keycloak is deployed now as an independent auth platform service; no noebs auth data is wired to it yet.

When using the in-cluster `postgres` StatefulSet, service database URLs should point at the owned database names created by the init script: `identity_auth`, `card_vault`, `ebs_adapter`, `psp_webhook`, `admin_reporting`, `notification_chat`, `consumer_beneficiary`, and `wallet_ledger`.

Noebs service roles and OTel service names are selected by mounted config, not environment variables. The base `noebs-config` ConfigMap provides shared `config.yaml` and one `*.service.yaml` key per workload and migration job.

The public Ingress routes both hostnames only to `api-gateway`. Internal HTTP routing is owned by the gateway through `noebs.service_discovery` in the mounted config. Wallet API to wallet ledger gRPC routing uses `noebs.grpc_service_discovery.wallet-ledger`.

Render check:

```sh
kubectl kustomize deploy/kubernetes/overlays/current-host
```
