# current-host Overlay

This overlay targets the existing deployment host `100.102.164.34`.

The `noebs` namespace is owned by `foundation/terraform`; this overlay only renders namespaced runtime resources.

Required DNS records:

- `api.noebs.sd A 100.102.164.34`
- `dsa.adonese.sd A 100.102.164.34`

Required Kubernetes Secrets in namespace `noebs`:

- `api-gateway-secrets` with key `secrets.yaml`.
- `identity-auth-secrets` with key `secrets.yaml`.
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

Each noebs service secret must contain the merged secret material expected by that service, including `noebs.default_tenant_id`, that service's `noebs.db_url`, JWT/admin keys it owns, EBS credentials it owns, data key material it owns, and PSP secrets it owns. Wallet API, wallet ledger, and wallet worker share `wallet-ledger-secrets` because ledger is the database owner for wallet state.

When using the in-cluster `postgres` StatefulSet, service `noebs.db_url` values should point at the owned database names created by the init script: `api_gateway`, `identity_auth`, `card_vault`, `ebs_adapter`, `psp_webhook`, `admin_reporting`, `notification_chat`, `consumer_beneficiary`, and `wallet_ledger`.

Noebs service roles are selected by mounted config, not environment variables. The base `noebs-config` ConfigMap provides shared `config.yaml` and one `*.service.yaml` key per workload and migration job.

Render check:

```sh
kubectl kustomize deploy/kubernetes/overlays/current-host
```
