# Keycloak membership operations

These Jobs are operator-only and are separate from the fleet rollout. They
reuse the realm-local `keycloak-reconciler-credentials` Secret and the deployed
tenant and Keycloak authority. Run them from a clean checkout at the revision
recorded in the `noebs-release` ConfigMap.

`run-keycloak-job.sh` verifies that revision, waits for Keycloak readiness,
compares the rendered authority ConfigMaps and their live immutable bytes, and
sets the Job's application and init images to the deployed release digest. It
applies only the Job. It does not apply or delete shared authority ConfigMaps.
The runner requires kubectl (or local k3s), Git, jq, Python with PyYAML, and
Kustomize (or kubectl kustomize).

## Obtain the immutable subject

After the user's first broker login, look up the resulting realm user by exact
email. Email is used only for this read; the UUID is the assignment authority.

```sh
umask 077
read -r LOOKUP_EMAIL
printf '%s' "$LOOKUP_EMAIL" > /tmp/noebs-keycloak-lookup-email
unset LOOKUP_EMAIL

kubectl -n noebs create secret generic keycloak-subject-lookup \
  --from-file=email=/tmp/noebs-keycloak-lookup-email \
  --dry-run=client -o yaml | kubectl apply -f -
infra/kubernetes/operations/run-keycloak-job.sh lookup
kubectl -n noebs wait --for=condition=complete --timeout=120s \
  job/noebs-keycloak-subject-lookup
kubectl -n noebs logs job/noebs-keycloak-subject-lookup
```

Successful lookup output is exactly one canonical UUID. Zero or multiple exact
matches fail the Job. Record that UUID, then remove the lookup material:

```sh
kubectl -n noebs delete job noebs-keycloak-subject-lookup
kubectl -n noebs delete secret keycloak-subject-lookup
rm -f /tmp/noebs-keycloak-lookup-email
```

## Change tenant access

Use the private [tenant access workflow](../../../docs/tenant-access.md) for
explicit, composable role grants and revocations. It is the single journaled write
path, with a reviewed revision, operation ID, reason and durable signup suppression.
The former `apply` mode and `assign-keycloak-memberships` writes are retired and
fail before contacting Kubernetes or Keycloak. Historical single-class membership
files can still be inspected with `dry-run`; they are never applied.

Initial `admin@noebs.sd` / `noebs-admin` setup uses the separate optional bootstrap
renderer documented in the tenant access guide. It requires the deployed immutable
image, operation-scoped manifests and the database migration owner. It is not part
of ordinary fleet promotion and cannot be reused after later revocation.
