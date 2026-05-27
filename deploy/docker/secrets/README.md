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

Database-opening services must include `noebs.service_databases` with only their owner-role database URL. `api-gateway.secrets.yaml` and `wallet-api.secrets.yaml` must not include `noebs.db_url` or `noebs.service_databases`. `wallet-worker` and `wallet-ledger-migrate` mount `wallet-ledger.secrets.yaml` because `wallet-ledger` owns wallet state.

The root `secrets.yaml` remains a bootstrap secret for `secrets-init`, which runs `noebs render-db-password` to render the local Postgres password file before Postgres starts. It is not mounted into Noebs app or migration services.
