# Keycloak first bootstrap

Use this overlay exactly once against an empty Keycloak database. It creates a
temporary service client in the `master` realm, runs the repository desired
state with that client, and then deletes the temporary client at sync wave 6.
Its Noebs image digest is an explicit promotion pin and must match
`overlays/current-host`, `operations/lookup`, and
`operations/memberships/base` in the same digest-promotion commit.

Bootstrap uses the steady Postgres authority unchanged: exactly 22 service
login roles across eight databases. PSP tables migrate with `wallet_ledger`;
this overlay must not restore a `psp-webhook-migrate` Job, Secret, role, or
database.

First run `prepare-kubernetes-release` and apply its rendered steady Secrets.
That creates `keycloak-reconciler-credentials` and projects the explicit
`noebs-backoffice` and `noebs-wallet-authorizer` credentials into the API
gateway configuration. It also creates the CA-only `keycloak-transport-ca`
mounted by every bootstrap and steady reconciliation Job; no Job accepts
plaintext Keycloak transport.

Create one additional SOPS-encrypted bootstrap input:

```yaml
api_version: noebs.sd/keycloak-bootstrap/v1
client_secret: REPLACE_WITH_CANONICAL_32_BYTE_BASE64URL_SECRET
```

Render the temporary Secrets to an exclusive mode-0600 file and apply it:

```sh
noebs render-keycloak-bootstrap-secrets /path/to/noebs-kubernetes-release noebs /path/to/keycloak-bootstrap.inputs.yaml /path/to/keycloak-bootstrap.secrets.yaml
kubectl apply -f /path/to/keycloak-bootstrap.secrets.yaml
```

The renderer derives all steady values from the validated release and writes
no secret values to stdout. It creates exactly these temporary Secrets:

- `keycloak-bootstrap-admin`, key `client-secret`.
- `keycloak-bootstrap-reconciler-credentials`, key `config.yaml`, using the
  same temporary client secret.

Keycloak rolls out at wave 3 with the temporary bootstrap client. The wave-5
reconcile creates `noebs-keycloak-reconciler` in the `noebs` realm,
enables its service account, maps only `realm-management/realm-admin`, and
installs its independent steady credential. The wave-6 Job authenticates as the
temporary client and deletes that client from `master`.

For the first sync, set the foundation input
`noebs_manifest_path = "deploy/kubernetes/overlays/bootstrap-current-host"`,
set `create_noebs_application = true` and `noebs_automated_sync = true`, then
apply the reviewed OpenTofu plan. Wait for the Application to become `Synced`
and `Healthy` and for the wave-6 delete Job to succeed.

Then set `noebs_manifest_path = "deploy/kubernetes/overlays/current-host"` and
apply the next reviewed plan. Keycloak rolls out at wave 3 without the
bootstrap client environment, then the steady realm-local reconciler runs at
wave 5. Delete both temporary Secrets only after that steady reconcile
succeeds. The bootstrap overlay is never a steady-state fallback.
The steady `keycloak-reconciler-credentials` Secret must authenticate as
`noebs-keycloak-reconciler` against `admin_realm: noebs`. A failed bootstrap is
not a steady-state mode: fix it and repeat with a fresh database.
