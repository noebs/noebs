# Docker Compose Secrets

Docker Compose mirrors the rendered Kubernetes runtime boundary: each Noebs service mounts one plaintext, service-specific `secrets.yaml` file. SOPS decryption happens on the trusted host before Compose starts; no container receives an age identity. Do not put service runtime secrets into `config.docker.yaml`, Compose environment variables, or a shared Noebs app secret.

Expected files:

- `api-gateway.secrets.yaml`
- `identity-auth.secrets.yaml`
- `card-vault.secrets.yaml`
- `ebs-adapter.secrets.yaml`
- `ebs-adapter-events.secrets.yaml`
- `psp-webhook.secrets.yaml`
- `admin-reporting.secrets.yaml`
- `admin-reporting-projector.secrets.yaml`
- `notification-chat.secrets.yaml`
- `wallet-api.secrets.yaml`
- `wallet-ledger.secrets.yaml`
- `wallet-worker.secrets.yaml`
- `workload-auth-migrate.secrets.yaml`
- `workload-auth-cleanup.secrets.yaml`
- `gateway-auth-migrate.secrets.yaml`
- `gateway-auth-cleanup.secrets.yaml`
- `identity-auth-migrate.secrets.yaml`
- `card-vault-migrate.secrets.yaml`
- `ebs-adapter-migrate.secrets.yaml`
- `admin-reporting-migrate.secrets.yaml`
- `notification-chat-migrate.secrets.yaml`
- `wallet-ledger-migrate.secrets.yaml`

Each expected file has a checked-in `*.example` next to it. The examples define the exact role shape: credentials, certificates, tenant values, and key IDs use `REPLACE_WITH_*` placeholders, while fixed workload caller IDs and TLS database URL structure remain literal. Render protected plaintext files from the encrypted authority on the host and replace every placeholder before validation.

Database-opening services get `noebs.db_driver` from their mounted service config and must include `noebs.service_databases` with only their owner key. The API gateway and its migration and cleanup roles use the `api-gateway` owner key for `gateway_auth`; the workload-auth roles use `workload-auth-migrate` for `workload_auth`. `wallet-api.secrets.yaml` must not include `noebs.db_url` or `noebs.service_databases`. `ebs-adapter-events` and `admin-reporting-projector` mount their own role-scoped secret files with the same database owner keys as their parent runtime roles. `wallet-worker.secrets.yaml` and `psp-webhook.secrets.yaml` both use the `wallet-ledger` owner key: the worker connects as `wallet_ledger_worker`, and the webhook ingress connects as the narrower `wallet_ledger_webhook`. PSP persistence belongs to the wallet migration scope; there is no PSP migration service, role, or database.

`ebs-adapter.secrets.yaml` must carry explicit resolved EBS runtime values: `consumer_endpoint`, `merchant_endpoint`, `ipin_endpoint`, `consumer_app_id`, `merchant_app_id`, `ipin_username`, `ipin_password`, `pub_key`, `ipin_key`, `pan`, `pin`, `ipin`, and `exp_date`. Do not provide QA/prod pairs or mode booleans and expect the runtime to choose.

Local Compose also requires explicit, ignored platform and database-role inputs:

- `deploy/docker/temporal/postgres-password.txt`
- `deploy/docker/temporal/ca.pem`
- `deploy/docker/temporal/postgres-tls.crt`
- `deploy/docker/temporal/postgres-tls.key`
- `deploy/docker/temporal/tls.crt`
- `deploy/docker/temporal/tls.key`
- `deploy/docker/temporal/namespace-bootstrap-client-secret.txt`
- `deploy/docker/keycloak/keycloak.conf`
- `deploy/docker/keycloak/postgres-password.txt`
- `deploy/docker/keycloak/ca.pem`
- `deploy/docker/keycloak/postgres-tls.crt`
- `deploy/docker/keycloak/postgres-tls.key`
- `deploy/docker/keycloak/tls.crt`
- `deploy/docker/keycloak/tls.key`
- `deploy/docker/postgres/service-role-passwords.env`
- `deploy/docker/postgres/ca.pem`
- `deploy/docker/postgres/tls.crt`
- `deploy/docker/postgres/tls.key`

These files are local-only and must not be committed. The repository carries matching examples with placeholders only. `service-role-passwords.env` is the exact 22-role catalog used only by Postgres; every password must be globally distinct and an unpadded canonical base64url encoding of exactly 32 bytes. No workload mounts the catalog. Each service secret contains only its exact role URL, using `postgres://<role>:<password>@db:5432/<database>?sslmode=verify-full`; no alternative role, host, database, query, or plaintext mode is accepted.

Noebs Postgres initializes as the operating-system-matched `postgres` superuser and provisions roles only through a local peer-authenticated Unix socket. The `postgres` role is rejected over the network. A data volume without `.noebs-postgres-authority` or with unexpected roles or databases is rejected and must be recreated; there is no bootstrap password or compatibility path.

Noebs Postgres is TLS-only. Its certificate must chain to `deploy/docker/postgres/ca.pem` and carry the exact DNS identity `db`. Every database-opening service and workload-auth receiver stores that same CA in `noebs.database_ca_certificate`. The Compose cleanup roles run immediately after their migrations and then every five minutes. The Keycloak certificates use a separate explicit CA input and the distinct DNS identities `keycloak-postgres` and `keycloak`; the API gateway receives that Keycloak CA but never either server private key.

The same Noebs transport CA signs one distinct dual-use client/server leaf for
each HTTP runtime and for `wallet-ledger`. Each leaf carries only its exact
role DNS identities and is stored in that role's `noebs.internal_transport`
section. Shared service discovery is HTTPS-only. Runtime startup rejects a
missing identity, a role mismatch, a different CA, or plaintext discovery;
HTTP health checks use the workload's own mTLS identity.

The Keycloak settings follow its supported database TLS interface: [`db-tls-mode=verify-server` with a PostgreSQL PEM trust file](https://www.keycloak.org/server/db#_secure_your_connection). Do not replace it with JDBC `sslmode=require`, a trust-all setting, or plaintext fallback.

The Compose deployment publishes only `api-gateway` on loopback port `8081`.
Temporal uses authenticated TLS on an isolated control network; its internal
frontend and backend services are loopback-only, and the unused UI is absent.
Keycloak and Kafka remain reachable only through private Compose networks. Kafka and
Temporal Compose images are pinned by digest. The Kubernetes
`edge/caddy` deployment is the sole public routing authority.

Before replacing a host deployment, run the explicit preflight against the release directory:

```sh
noebs validate-deployment /path/to/noebs-release
```

The preflight rejects encrypted runtime inputs and validates every plaintext service secret and merged service config before any containers start. It requires the exact runtime and migration role files under `deploy/docker/services`, rejects extra real `*.secrets.yaml` service secret files under `deploy/docker/secrets`, and rejects missing files, placeholder values, reserved tenant IDs, legacy `noebs.db_path`, non-owner database entries, missing service-owned database URLs, invalid Noebs Postgres TLS identity or CA wiring, non-canonical, reused, or mismatched credentials across all 22 database roles, incomplete EBS adapter endpoint/app-id/IPIN/key/bill-inquiry inputs, incomplete Keycloak/Temporal/Postgres platform inputs, and missing or extra HTTP/gRPC service discovery entries.

Database role or TLS rotation is one bounded whole-stack replacement. Render the complete matching platform files, role catalog, service configs, and encrypted service secrets into a protected staging directory, and pass `validate-deployment` against that staged release before touching the running project. Stop the complete project with `docker compose down` while retaining its named volumes, install the entire validated input set, validate the active release directory again, then start it with:

```sh
docker compose up -d --build --force-recreate
```

Wait for Postgres readiness, every migration container to exit successfully, and every runtime health check before reopening traffic. `docker compose restart`, a database-only restart, a partial credential replacement, and simultaneous old/new passwords are invalid rotation procedures.

For Kubernetes cutovers, render the required Secret manifests from a Kubernetes release input directory:

```sh
noebs render-kubernetes-secrets /path/to/noebs-kubernetes-release noebs | kubectl apply -f -
```

That renderer uses the exact Kubernetes preflight layout (`config.yaml`, `.sops/age-key.txt`, platform files under `platform/`, runtime and migration role files under `services/`, and service secret files under `secrets/`) instead of the Docker Compose release layout. Extra top-level, `.sops`, platform, service config, or service secret entries fail validation.

The renderer validates the release before writing manifests. It does not derive EBS endpoints, tenant IDs, database URLs, or passwords.
