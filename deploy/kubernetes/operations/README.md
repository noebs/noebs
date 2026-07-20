# Keycloak membership operations

These Jobs are operator-only and are not part of the Argo CD application. They
reuse the realm-local `keycloak-reconciler-credentials` Secret and the exact
tenant and Keycloak desired-state ConfigMaps. The membership render consumes
the same `keycloak-authority` generator as the steady overlay, so both manifests
name and carry byte-identical immutable ConfigMaps. The operation runner
requires a clean checkout at the exact healthy steady-overlay Argo revision,
compares both rendered ConfigMaps, requires those exact hashes and bytes to
already exist live with Argo tracking, and applies only the rendered Job.
Direct `kubectl apply -k` and `kubectl delete -k` are forbidden: either command
would change ownership of ConfigMaps managed by the steady application. Never
add this directory to the base or an application overlay.

The image digest in `lookup` and `memberships/base` must match both the steady
and bootstrap overlay Noebs pins. All four digest fields are updated in the
same promotion commit; an operation built from another binary is invalid.

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
kubectl apply --dry-run=server -k deploy/kubernetes/operations/lookup >/dev/null
kubectl apply -k deploy/kubernetes/operations/lookup
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

## Reconcile memberships

Copy `memberships.example.yaml` outside the repository, replace `subject`, and
declare the complete desired tenant set for that subject. Omitting a tenant
removes that organization membership. Each included tenant has exactly one of
`user`, `backoffice`, or `tenant-admin`.

```sh
umask 077
cp deploy/kubernetes/operations/memberships.example.yaml \
  /tmp/noebs-keycloak-memberships.yaml
${EDITOR:?set EDITOR} /tmp/noebs-keycloak-memberships.yaml

kubectl -n noebs create secret generic keycloak-membership-assignment \
  --from-file=memberships.yaml=/tmp/noebs-keycloak-memberships.yaml \
  --dry-run=client -o yaml | kubectl apply -f -
deploy/kubernetes/operations/run-membership-job.sh dry-run
kubectl -n noebs wait --for=condition=complete --timeout=120s \
  job/noebs-keycloak-membership-assignment
kubectl -n noebs logs job/noebs-keycloak-membership-assignment
```

Review the stable dry-run actions. Delete the dry-run Job, apply the same exact
input, and inspect the verified result:

```sh
kubectl -n noebs delete job noebs-keycloak-membership-assignment
deploy/kubernetes/operations/run-membership-job.sh apply
kubectl -n noebs wait --for=condition=complete --timeout=120s \
  job/noebs-keycloak-membership-assignment
kubectl -n noebs logs job/noebs-keycloak-membership-assignment
```

For an idempotency check, delete and run the apply Job once more; its summary
must report zero actions. Then remove the Job, temporary Secret, and local
input. The command assigns organization and organization-group membership only;
it never assigns a user role directly.

```sh
kubectl -n noebs delete job noebs-keycloak-membership-assignment
kubectl -n noebs delete secret keycloak-membership-assignment
rm -f /tmp/noebs-keycloak-memberships.yaml
```
