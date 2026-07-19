# Keycloak first bootstrap

Use this overlay exactly once against an empty Keycloak database. It creates a
temporary service client in the `master` realm, runs the repository desired
state with that client, and then deletes the temporary client at sync wave 6.

Before the first sync, create these two temporary Secrets in `noebs`:

- `keycloak-bootstrap-admin`, key `client-secret`.
- `keycloak-bootstrap-reconciler-credentials`, key `config.yaml`, shaped like
  `keycloak-bootstrap-reconciler-config.example.yaml` and using the same
  temporary client secret.

The wave-5 reconcile creates `noebs-keycloak-reconciler` in the `noebs` realm,
enables its service account, maps only `realm-management/realm-admin`, and
installs its independent steady credential. The wave-6 Job authenticates as the
temporary client and deletes that client from `master`.

After the delete Job succeeds, switch Argo CD to
`deploy/kubernetes/overlays/current-host` and delete both temporary Secrets.
The steady `keycloak-reconciler-credentials` Secret must authenticate as
`noebs-keycloak-reconciler` against `admin_realm: noebs`. A failed bootstrap is
not a steady-state mode: fix it and repeat with a fresh database.
