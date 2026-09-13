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

## Reconcile memberships

Copy `memberships.example.yaml` outside the repository, replace `subject`, and
declare the complete desired tenant set for that subject. Omitting a tenant
removes that organization membership. Each included tenant has exactly one of
`user`, `backoffice`, or `tenant-admin`.

```sh
umask 077
cp infra/kubernetes/operations/memberships.example.yaml \
  /tmp/noebs-keycloak-memberships.yaml
${EDITOR:?set EDITOR} /tmp/noebs-keycloak-memberships.yaml

kubectl -n noebs create secret generic keycloak-membership-assignment \
  --from-file=memberships.yaml=/tmp/noebs-keycloak-memberships.yaml \
  --dry-run=client -o yaml | kubectl apply -f -
infra/kubernetes/operations/run-keycloak-job.sh dry-run
kubectl -n noebs wait --for=condition=complete --timeout=120s \
  job/noebs-keycloak-membership-assignment
kubectl -n noebs logs job/noebs-keycloak-membership-assignment
```

Review the stable dry-run actions. Delete the dry-run Job, apply the same exact
input, and inspect the verified result:

```sh
kubectl -n noebs delete job noebs-keycloak-membership-assignment
infra/kubernetes/operations/run-keycloak-job.sh apply
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
