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
- `wallet-api.secrets.yaml`
- `wallet-ledger.secrets.yaml`
- `wallet-worker.secrets.yaml`

Each expected file has a checked-in `*.example` next to it. The examples define shape only; every scalar value is a `REPLACE_WITH_*` placeholder and must be replaced in the local SOPS-encrypted file.

Database-opening services get `noebs.db_driver` from their mounted service config and must include `noebs.service_databases` in secrets with only their owner-role database URL. `api-gateway.secrets.yaml` and `wallet-api.secrets.yaml` must not include `noebs.db_url` or `noebs.service_databases`. `ebs-adapter-events` mounts `ebs-adapter.secrets.yaml` because it publishes the EBS-owned outbox, and `admin-reporting-projector` mounts `admin-reporting.secrets.yaml` because it writes the reporting projection. `wallet-worker.secrets.yaml` uses the `wallet-ledger` owner key because the worker uses ledger state without owning a separate database or migration scope.

`ebs-adapter.secrets.yaml` must carry explicit resolved EBS runtime values: `consumer_endpoint`, `merchant_endpoint`, `ipin_endpoint`, `consumer_app_id`, `merchant_app_id`, `ipin_username`, `ipin_password`, `pub_key`, `ipin_key`, `pan`, `pin`, `ipin`, and `exp_date`. Do not provide QA/prod pairs or mode booleans and expect the runtime to choose.

`secrets-init` uses the explicit ignored bootstrap secret at `deploy/docker/postgres/bootstrap.secrets.yaml` to run `noebs render-db-password` and render the local Postgres password file before Postgres starts. The repository carries `deploy/docker/postgres/bootstrap.secrets.yaml.example` with placeholders only. The root `secrets.yaml` is not a Docker Compose bootstrap contract and is not mounted into Noebs app or migration services.

Local Compose also requires explicit, ignored runtime inputs for non-Noebs platform services:

- `deploy/docker/temporal/postgres-password.txt`
- `deploy/docker/keycloak/keycloak.conf`
- `deploy/docker/keycloak/postgres-password.txt`
- `deploy/docker/keycloak/ca.pem`
- `deploy/docker/keycloak/postgres-tls.crt`
- `deploy/docker/keycloak/postgres-tls.key`
- `deploy/docker/keycloak/tls.crt`
- `deploy/docker/keycloak/tls.key`

These files are local-only and must not be committed. The repository carries matching examples with placeholders only. The two certificates chain to `ca.pem` but use distinct keys and DNS identities: `keycloak-postgres` for the database and `keycloak` for the HTTPS application listener. Kubernetes generates the same distinct identities from the release transport CA. The API gateway receives only the CA; the Keycloak server private key remains confined to `keycloak-secrets`.

The Keycloak settings follow its supported database TLS interface: [`db-tls-mode=verify-server` with a PostgreSQL PEM trust file](https://www.keycloak.org/server/db#_secure_your_connection). Do not replace it with JDBC `sslmode=require`, a trust-all setting, or plaintext fallback.

The Compose deployment publishes only `api-gateway` on loopback port `8081`.
Temporal, Temporal UI, Keycloak, and Kafka remain reachable only through the
Compose network. Compose does not own TLS or a public edge; the Kubernetes
`edge/caddy` deployment is the sole public routing authority.

Before replacing a host deployment, run the explicit preflight against the release directory:

```sh
noebs validate-deployment /path/to/noebs-release
```

The preflight decrypts every service secret with the release age key and validates the merged service configs before any containers are started. It requires the exact runtime and migration role files under `deploy/docker/services`, rejects extra real `*.secrets.yaml` service secret files under `deploy/docker/secrets`, and rejects missing files, placeholder values, reserved tenant IDs, legacy `noebs.db_path`, non-owner database entries, missing service-owned database URLs, incomplete EBS adapter endpoint/app-id/IPIN/key/bill-inquiry inputs, incomplete Keycloak/Temporal/Postgres platform inputs, and missing or extra HTTP/gRPC service discovery entries.

For Kubernetes cutovers, render the required Secret manifests from a Kubernetes release input directory:

```sh
noebs render-kubernetes-secrets /path/to/noebs-kubernetes-release noebs | kubectl apply -f -
```

That renderer uses the exact Kubernetes preflight layout (`config.yaml`, `.sops/age-key.txt`, platform files under `platform/`, runtime and migration role files under `services/`, and service secret files under `secrets/`) instead of the Docker Compose release layout. Extra top-level, `.sops`, platform, service config, or service secret entries fail validation.

The renderer validates the release before writing manifests. It does not derive EBS endpoints, tenant IDs, database URLs, or passwords.
