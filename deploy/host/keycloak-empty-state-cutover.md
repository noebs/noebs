# Keycloak empty-state cutover

This destructive procedure is valid only while the Noebs namespace has no
production state. Complete the K3s encryption transition in `README.md` and
the state sanitization in `foundation/terraform/README.md` first.

The reset replaces the foundation-owned `noebs` namespace. That removes every
old workload, Secret, database volume, and retired object at one boundary,
including the main, Keycloak, and Temporal PostgreSQL claims. Merely changing
their password Secrets is invalid: `POSTGRES_PASSWORD_FILE` is consumed only
while initializing empty `PGDATA`; it does not update roles in an existing
database cluster.

## Prepare the immutable release

Run from the reviewed digest-promotion commit in one Bash session. `CUTOVER_DIR`
must be on an approved encrypted filesystem because its contents are sensitive.

```bash
set -euo pipefail
umask 077

: "${CURRENT_COMMIT:?set the currently targeted 40-hex commit}"
: "${RELEASE_COMMIT:?set the reviewed promotion 40-hex commit}"
: "${RELEASE_DIGEST:?set the verified sha256 image digest}"
: "${RELEASE_REPO_ROOT:?set the reviewed release checkout}"
: "${RELEASE_ROOT:?set the validated Kubernetes release directory}"
: "${BOOTSTRAP_INPUT:?set the SOPS-encrypted bootstrap input}"
: "${CUTOVER_DIR:?set a protected encrypted directory}"

[[ "$CURRENT_COMMIT" =~ ^[0-9a-f]{40}$ ]]
[[ "$RELEASE_COMMIT" =~ ^[0-9a-f]{40}$ ]]
[[ "$RELEASE_DIGEST" =~ ^sha256:[0-9a-f]{64}$ ]]
[[ "$(git -C "$RELEASE_REPO_ROOT" rev-parse --verify HEAD^{commit})" == "$RELEASE_COMMIT" ]]
git -C "$RELEASE_REPO_ROOT" diff --quiet
git -C "$RELEASE_REPO_ROOT" diff --cached --quiet
test -z "$(git -C "$RELEASE_REPO_ROOT" ls-files --others --exclude-standard)"
cd "$RELEASE_REPO_ROOT"
foundation_root="$RELEASE_REPO_ROOT/foundation/terraform"
test -s "$foundation_root/terraform.tfstate"

install -d -m 0700 "$CUTOVER_DIR"
steady_secrets="$CUTOVER_DIR/steady-secrets.yaml"
bootstrap_secrets="$CUTOVER_DIR/bootstrap-secrets.yaml"
edge_keycloak_ca="$CUTOVER_DIR/edge-keycloak-transport-ca.yaml"
pause_plan="$CUTOVER_DIR/pause.tfplan"
namespace_plan="$CUTOVER_DIR/namespace-replacement.tfplan"
bootstrap_plan="$CUTOVER_DIR/bootstrap.tfplan"
steady_plan="$CUTOVER_DIR/steady.tfplan"
kubectl=(sudo -n k3s kubectl)

encryption_status="$(sudo k3s secrets-encrypt status)"
grep -Fx 'Encryption Status: Enabled' <<<"$encryption_status" >/dev/null
grep -Fx 'Current Rotation Stage: reencrypt_finished' \
  <<<"$encryption_status" >/dev/null
grep -Fx 'Server Encryption Hashes: All hashes match' \
  <<<"$encryption_status" >/dev/null
encryption_json="$(sudo k3s secrets-encrypt status --output json)"
jq -e '
  .enable == true
  and .stage == "reencrypt_finished"
  and .hashmatch == true
  and (.activekey | startswith("XSalsa20-POLY1305 secretboxkey-"))
' <<<"$encryption_json" >/dev/null
"${kubectl[@]}" get --raw=/readyz >/dev/null
"${kubectl[@]}" wait --for=condition=Ready node --all --timeout=30s >/dev/null

pin_paths=(
  deploy/kubernetes/overlays/current-host/kustomization.yaml
  deploy/kubernetes/overlays/bootstrap-current-host/kustomization.yaml
  deploy/kubernetes/operations/lookup/kustomization.yaml
  deploy/kubernetes/operations/memberships/base/kustomization.yaml
)
for pin_path in "${pin_paths[@]}"; do
  pinned_digest="$(awk '
    $1 == "-" && $2 == "name:" && $3 == "ghcr.io/noebs/noebs" { noebs = 1; next }
    noebs && $1 == "digest:" { print $2; exit }
  ' "$pin_path")"
  test "$pinned_digest" = "$RELEASE_DIGEST"
done

noebs validate-kubernetes-deployment "$RELEASE_ROOT"
noebs render-kubernetes-secrets "$RELEASE_ROOT" noebs > "$steady_secrets"
noebs render-keycloak-transport-ca "$RELEASE_ROOT" edge > "$edge_keycloak_ca"
noebs render-keycloak-bootstrap-secrets \
  "$RELEASE_ROOT" noebs "$BOOTSTRAP_INPUT" "$bootstrap_secrets"
chmod 0600 "$steady_secrets" "$bootstrap_secrets" "$edge_keycloak_ca"
"${kubectl[@]}" get namespace edge >/dev/null
"${kubectl[@]}" apply --dry-run=server -f "$edge_keycloak_ca" >/dev/null

for render_root in \
  deploy/kubernetes/overlays/bootstrap-current-host \
  deploy/kubernetes/overlays/current-host \
  deploy/kubernetes/edge
do
  "${kubectl[@]}" kustomize "$render_root" \
    | "${kubectl[@]}" apply --dry-run=server -f - >/dev/null
done
```

All four Noebs digest pins must equal `RELEASE_DIGEST`. Keep
`noebs_target_revision` at `CURRENT_COMMIT` while stopping the old application.

## Stop old consumers and replace the namespace

Set these foundation values, substituting the actual current commit, then
review and apply the plan:

```hcl
noebs_target_revision    = "CURRENT_COMMIT"
create_noebs_application = false
noebs_automated_sync     = false
```

```bash
old_namespace_uid="$("${kubectl[@]}" get namespace noebs -o jsonpath='{.metadata.uid}')"
"${kubectl[@]}" -n noebs get pvc \
  -o jsonpath='{range .items[*]}{.spec.volumeName}{"\n"}{end}' \
  > "$CUTOVER_DIR/old-noebs-pvs"

tofu -chdir="$foundation_root" plan -out="$pause_plan"
tofu -chdir="$foundation_root" apply "$pause_plan"
! "${kubectl[@]}" -n argocd get application noebs >/dev/null 2>&1

"${kubectl[@]}" -n noebs scale deployment,statefulset --all --replicas=0
"${kubectl[@]}" -n noebs delete job --all --wait=true
"${kubectl[@]}" -n noebs wait --for=delete pod --all --timeout=5m

tofu -chdir="$foundation_root" plan \
  -replace=kubernetes_namespace_v1.noebs \
  -out="$namespace_plan"
tofu -chdir="$foundation_root" apply "$namespace_plan"

new_namespace_uid="$("${kubectl[@]}" get namespace noebs -o jsonpath='{.metadata.uid}')"
test "$new_namespace_uid" != "$old_namespace_uid"
test -z "$("${kubectl[@]}" -n noebs get pvc -o name)"
while IFS= read -r old_pv; do
  [[ -z "$old_pv" ]] && continue
  for _ in $(seq 1 60); do
    ! "${kubectl[@]}" get persistentvolume "$old_pv" >/dev/null 2>&1 && break
    sleep 5
  done
  ! "${kubectl[@]}" get persistentvolume "$old_pv" >/dev/null 2>&1
done < "$CUTOVER_DIR/old-noebs-pvs"
```

There is now no old process, Secret, PVC, or database. Apply rotated authority
only after both Secret manifests pass server validation in the new namespace:

```bash
"${kubectl[@]}" apply --dry-run=server -f "$steady_secrets" >/dev/null
"${kubectl[@]}" apply --dry-run=server -f "$bootstrap_secrets" >/dev/null
"${kubectl[@]}" apply -f "$steady_secrets"
"${kubectl[@]}" apply -f "$bootstrap_secrets"
```

## Bootstrap, then establish steady authority

Set the actual promotion commit and bootstrap path:

```hcl
noebs_target_revision    = "RELEASE_COMMIT"
noebs_manifest_path      = "deploy/kubernetes/overlays/bootstrap-current-host"
create_noebs_application = true
noebs_automated_sync     = true
create_edge_application  = true
```

```bash
sudo install -d -m 0700 /var/lib/docker/volumes/noebs_caddy_data/_data /var/lib/docker/volumes/noebs_caddy_config/_data
sudo chown -R -- 10001:10001 /var/lib/docker/volumes/noebs_caddy_data/_data /var/lib/docker/volumes/noebs_caddy_config/_data
caddy_wrong_owner="$(sudo find \
  /var/lib/docker/volumes/noebs_caddy_data/_data \
  /var/lib/docker/volumes/noebs_caddy_config/_data \
  \( ! -uid 10001 -o ! -gid 10001 \) -print -quit)"
test -z "$caddy_wrong_owner"
caddy_host_path_status="$(sudo stat --format='%u:%g %a %n' \
  /var/lib/docker/volumes/noebs_caddy_data/_data \
  /var/lib/docker/volumes/noebs_caddy_config/_data)"
expected_caddy_host_path_status=$'10001:10001 700 /var/lib/docker/volumes/noebs_caddy_data/_data\n10001:10001 700 /var/lib/docker/volumes/noebs_caddy_config/_data'
test "$caddy_host_path_status" = "$expected_caddy_host_path_status"

"${kubectl[@]}" apply -f "$edge_keycloak_ca"
"${kubectl[@]}" -n edge get secret keycloak-transport-ca \
  -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}' \
  | grep -Fx noebs-release-renderer >/dev/null

tofu -chdir="$foundation_root" plan -out="$bootstrap_plan"
tofu -chdir="$foundation_root" apply "$bootstrap_plan"

until application="$("${kubectl[@]}" -n argocd get application noebs -o json)" \
  && jq -e --arg revision "$RELEASE_COMMIT" '
    .spec.source.targetRevision == $revision
    and .status.sync.revision == $revision
    and .status.sync.status == "Synced"
    and .status.health.status == "Healthy"
    and .status.operationState.phase == "Succeeded"
    and any(.status.operationState.syncResult.resources[]?;
      .name == "noebs-keycloak-delete-bootstrap-client"
      and .hookPhase == "Succeeded")
  ' <<<"$application" >/dev/null
do
  sleep 5
done
```

The wave-6 result is not sufficient on its own. Use the still-present
temporary Secret to prove those retired master-realm client credentials can no
longer obtain a token. The response body stays in the protected directory and
is never printed:

```bash
bootstrap_client_secret="$("${kubectl[@]}" -n noebs get secret \
  keycloak-bootstrap-admin -o jsonpath='{.data.client-secret}' | base64 -d)"
keycloak_service_ip="$("${kubectl[@]}" -n noebs get service keycloak \
  -o jsonpath='{.spec.clusterIP}')"
keycloak_ca="$CUTOVER_DIR/keycloak-ca.pem"
"${kubectl[@]}" -n noebs get secret keycloak-transport-ca \
  -o jsonpath='{.data.ca\.pem}' | base64 -d > "$keycloak_ca"
chmod 0600 "$keycloak_ca"
token_status="$(
  printf 'client_id=noebs-keycloak-bootstrap&client_secret=%s&grant_type=client_credentials' \
    "$bootstrap_client_secret" \
    | curl --silent --show-error \
        --output "$CUTOVER_DIR/retired-bootstrap-token-response" \
        --write-out '%{http_code}' \
        --cacert "$keycloak_ca" \
        --tlsv1.3 \
        --tls-max 1.3 \
        --resolve "keycloak.noebs.svc.cluster.local:8443:$keycloak_service_ip" \
        --header 'Content-Type: application/x-www-form-urlencoded' \
        --data-binary @- \
        "https://keycloak.noebs.svc.cluster.local:8443/auth/realms/master/protocol/openid-connect/token"
)"
test "$token_status" = 401
unset bootstrap_client_secret
rm -f "$CUTOVER_DIR/retired-bootstrap-token-response" "$keycloak_ca"
```

Change only the path to steady state, then review and apply:

```hcl
noebs_manifest_path = "deploy/kubernetes/overlays/current-host"
```

```bash
tofu -chdir="$foundation_root" plan -out="$steady_plan"
tofu -chdir="$foundation_root" apply "$steady_plan"

until application="$("${kubectl[@]}" -n argocd get application noebs -o json)" \
  && jq -e --arg revision "$RELEASE_COMMIT" '
    .spec.source.targetRevision == $revision
    and .status.sync.revision == $revision
    and .status.sync.status == "Synced"
    and .status.health.status == "Healthy"
    and .status.operationState.phase == "Succeeded"
    and any(.status.operationState.syncResult.resources[]?;
      .name == "noebs-keycloak-reconciler"
      and .hookPhase == "Succeeded")
  ' <<<"$application" >/dev/null
do
  sleep 5
done

"${kubectl[@]}" -n noebs delete secret \
  keycloak-bootstrap-admin keycloak-bootstrap-reconciler-credentials
```

Wait for `noebs-edge` to report the same declared and resolved commit and a
healthy sync. Require Caddy to use the generated ConfigMap before deleting the
unhashed predecessor:

```bash
until edge_application="$("${kubectl[@]}" -n argocd get application noebs-edge -o json)" \
  && jq -e --arg revision "$RELEASE_COMMIT" '
    .spec.source.targetRevision == $revision
    and .status.sync.revision == $revision
    and .status.sync.status == "Synced"
    and .status.health.status == "Healthy"
  ' <<<"$edge_application" >/dev/null
do
  sleep 5
done

"${kubectl[@]}" -n edge rollout status deployment/caddy --timeout=2m
caddy_config="$("${kubectl[@]}" -n edge get deployment caddy \
  -o jsonpath='{.spec.template.spec.volumes[?(@.name=="config")].configMap.name}')"
[[ "$caddy_config" =~ ^caddy-config-[a-z0-9]+$ ]]
"${kubectl[@]}" -n edge delete configmap caddy-config --ignore-not-found
```

## Prove retired state is gone

```bash
for retired in \
  deployment/consumer-beneficiary \
  service/consumer-beneficiary \
  secret/consumer-beneficiary-secrets \
  secret/consumer-beneficiary-migrate-secrets \
  serviceaccount/consumer-beneficiary \
  serviceaccount/consumer-beneficiary-migrate \
  job/noebs-consumer-beneficiary-migrate
do
  ! "${kubectl[@]}" -n noebs get "$retired" >/dev/null 2>&1
done

consumer_beneficiary_count="$("${kubectl[@]}" -n noebs exec postgres-0 -- sh -ceu '
  export PGPASSWORD="$(tr -d "\r\n" < /opt/noebs-postgres/secrets/password)"
  exec psql -U noebs -d postgres -XAtqc \
    "SELECT count(*) FROM pg_database WHERE datname = '\''consumer_beneficiary'\''"
')"
test "$consumer_beneficiary_count" = 0

scripts/alpha-post-deploy-smoke.sh "$RELEASE_COMMIT" "$RELEASE_DIGEST"
rm -f "$steady_secrets" "$bootstrap_secrets"
```

Keep protected plans and the state-removal backup until acceptance. Retire
them through the encrypted-media procedure in the foundation runbook; never
print their values with `tofu show`.
