# Docker Compose Secrets

Docker Compose mirrors the Kubernetes deployment model: each Noebs service mounts its own SOPS-encrypted `secrets.yaml` file, plus the shared age key mount. Do not put service runtime secrets into `config.docker.yaml`, Compose environment variables, or a shared Noebs app secret.

Expected files:

- `api-gateway.secrets.yaml`
- `identity-auth.secrets.yaml`
- `card-vault.secrets.yaml`
- `ebs-adapter.secrets.yaml`
- `psp-webhook.secrets.yaml`
- `admin-reporting.secrets.yaml`
- `notification-chat.secrets.yaml`
- `consumer-beneficiary.secrets.yaml`
- `wallet-api.secrets.yaml`
- `wallet-ledger.secrets.yaml`
- `wallet-worker.secrets.yaml`

Each expected file has a checked-in `*.example` next to it. The examples define shape only; every scalar value is a `REPLACE_WITH_*` placeholder and must be replaced in the local SOPS-encrypted file.

Database-opening services get `noebs.db_driver` from their mounted service config and must include `noebs.service_databases` in secrets with only their owner-role database URL. `api-gateway.secrets.yaml` and `wallet-api.secrets.yaml` must not include `noebs.db_url` or `noebs.service_databases`. `wallet-worker.secrets.yaml` uses the `wallet-ledger` owner key because the worker uses ledger state without owning a separate database or migration scope.

`ebs-adapter.secrets.yaml` must carry explicit resolved EBS runtime values: `consumer_endpoint`, `merchant_endpoint`, `ipin_endpoint`, `consumer_app_id`, and `merchant_app_id`. Do not provide QA/prod pairs or mode booleans and expect the runtime to choose. EBS dynamic fees are explicit shared runtime config in `config.docker.yaml` under `noebs.ebs_dynamic_fees`; do not move them into code defaults.

`secrets-init` uses the explicit ignored bootstrap secret at `deploy/docker/postgres/bootstrap.secrets.yaml` to run `noebs render-db-password` and render the local Postgres password file before Postgres starts. The repository carries `deploy/docker/postgres/bootstrap.secrets.yaml.example` with placeholders only. The root `secrets.yaml` is not a Docker Compose bootstrap contract and is not mounted into Noebs app or migration services.

Local Compose also requires explicit, ignored runtime inputs for non-Noebs platform services:

- `deploy/docker/temporal/postgres-password.txt`
- `deploy/docker/keycloak/keycloak.conf`
- `deploy/docker/keycloak/postgres-password.txt`

These files are local-only and must not be committed. The repository carries `deploy/docker/keycloak/keycloak.conf.example` with placeholders only; replace every placeholder in the local ignored file. Kubernetes uses `temporal-postgres-credentials`, `keycloak-secrets`, and `keycloak-postgres-credentials` Secrets instead of these Compose files.

The default Compose deployment publishes only `api-gateway` on host port `8081`. Temporal, Temporal UI, and Keycloak remain reachable through the Compose network. Caddy is available through the explicit `edge` profile for hosts where this stack owns ports `80` and `443`.

Before replacing a host deployment, run the explicit preflight against the release directory:

```sh
noebs validate-deployment /path/to/noebs-release
```

The preflight decrypts every service secret with the release age key and validates the merged service configs before any containers are started. It requires the exact runtime and migration role files under `deploy/docker/services`, rejects extra real `*.secrets.yaml` service secret files under `deploy/docker/secrets`, and rejects missing files, placeholder values, reserved tenant IDs, legacy `noebs.db_path`, non-owner database entries, missing service-owned database URLs, missing EBS adapter endpoints/app IDs, and incomplete Keycloak/Temporal/Postgres platform inputs.

For Kubernetes cutovers, render the required Secret manifests from a Kubernetes release input directory and explicit TLS files:

```sh
noebs render-kubernetes-secrets /path/to/noebs-kubernetes-release noebs /path/to/tls.crt /path/to/tls.key | kubectl apply -f -
```

That renderer uses the exact Kubernetes preflight layout (`config.yaml`, `.sops/age-key.txt`, platform files under `platform/`, runtime and migration role files under `services/`, and service secret files under `secrets/`) instead of the Docker Compose release layout. Extra top-level, `.sops`, platform, service config, or service secret entries fail validation.

The renderer validates the release before writing manifests. It does not derive EBS endpoints, tenant IDs, database URLs, passwords, or TLS material.
