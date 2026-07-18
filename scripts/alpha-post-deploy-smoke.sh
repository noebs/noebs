#!/usr/bin/env bash

set -euo pipefail

die() {
    printf 'alpha post-deploy smoke: %s\n' "$*" >&2
    exit 1
}

[[ $# -eq 2 ]] || die "usage: $0 <argo-git-revision> <sha256:image-digest>"

expected_revision="$1"
expected_digest="$2"
deploy_host="${NOEBS_DEPLOY_HOST:-100.102.164.34}"
api_origin="${NOEBS_API_ORIGIN:-https://api.noebs.sd}"

[[ "$expected_revision" =~ ^[0-9a-f]{40}$ ]] || die "Argo revision must be a full 40-character Git SHA"
[[ "$expected_digest" =~ ^sha256:[0-9a-f]{64}$ ]] || die "image digest must be sha256 followed by 64 lowercase hexadecimal characters"
[[ "$api_origin" == https://* ]] || die "NOEBS_API_ORIGIN must use HTTPS"

command -v ssh >/dev/null 2>&1 || die "ssh is unavailable"
command -v curl >/dev/null 2>&1 || die "curl is unavailable"
command -v python3 >/dev/null 2>&1 || die "python3 is unavailable"

printf 'alpha post-deploy smoke: checking Argo, rollouts, images, resources, and migrations\n'
ssh "$deploy_host" bash -s -- "$expected_revision" "$expected_digest" <<'REMOTE'
set -euo pipefail

expected_revision="$1"
expected_digest="$2"
namespace=noebs

fail() {
    printf 'alpha post-deploy smoke (cluster): %s\n' "$*" >&2
    exit 1
}

command -v kubectl >/dev/null 2>&1 || fail "kubectl is unavailable"
command -v jq >/dev/null 2>&1 || fail "jq is unavailable"

application="$(kubectl -n argocd get application noebs -o json)"
actual_revision="$(jq -r '.status.sync.revision // ""' <<<"$application")"
sync_status="$(jq -r '.status.sync.status // ""' <<<"$application")"
health_status="$(jq -r '.status.health.status // ""' <<<"$application")"

[[ "$actual_revision" == "$expected_revision" ]] || fail "Argo revision $actual_revision does not match $expected_revision"
[[ "$sync_status" == Synced ]] || fail "Argo sync status is $sync_status"
[[ "$health_status" == Healthy ]] || fail "Argo health status is $health_status"

while IFS= read -r workload; do
    kubectl -n "$namespace" rollout status "$workload" --timeout=5m >/dev/null
done < <(kubectl -n "$namespace" get deployment,statefulset -o name | sort)

expected_image="ghcr.io/noebs/noebs@$expected_digest"
pods="$(kubectl -n "$namespace" get pods -o json)"

wrong_declared_images="$(
    jq -r --arg expected "$expected_image" '
      .items[]
      | .metadata.name as $pod
      | .spec.containers[]
      | select(.image | startswith("ghcr.io/noebs/noebs"))
      | select(.image != $expected)
      | "\($pod):\(.name)=\(.image)"
    ' <<<"$pods"
)"
[[ -z "$wrong_declared_images" ]] || fail "unexpected declared Noebs images: $wrong_declared_images"

running_noebs_count="$(
    jq '[.items[].spec.containers[] | select(.image | startswith("ghcr.io/noebs/noebs"))] | length' <<<"$pods"
)"
[[ "$running_noebs_count" -gt 0 ]] || fail "no running Noebs containers were found"

wrong_running_images="$(
    jq -r --arg expected "$expected_image" '
      .items[]
      | .metadata.name as $pod
      | .status.containerStatuses[]?
      | select(.image | startswith("ghcr.io/noebs/noebs"))
      | select(.imageID != $expected)
      | "\($pod):\(.name)=\(.imageID)"
    ' <<<"$pods"
)"
[[ -z "$wrong_running_images" ]] || fail "unexpected running Noebs image IDs: $wrong_running_images"

best_effort="$(
    jq -r '.items[] | select(.status.phase == "Running" and .status.qosClass == "BestEffort") | .metadata.name' <<<"$pods"
)"
[[ -z "$best_effort" ]] || fail "BestEffort runtime pods remain: $best_effort"

restarted="$(
    jq -r '
      .items[]
      | .metadata.name as $pod
      | .status.containerStatuses[]?
      | select(.restartCount != 0)
      | "\($pod):\(.name)=\(.restartCount)"
    ' <<<"$pods"
)"
[[ -z "$restarted" ]] || fail "post-rollout containers have restarted: $restarted"

workloads="$(kubectl -n "$namespace" get deployment,statefulset -o json)"
missing_resources="$(
    jq -r '
      .items[]
      | .kind as $kind
      | .metadata.name as $name
      | .spec.template.spec.containers[]
      | select(
          (.resources.requests.cpu // "") == ""
          or (.resources.requests.memory // "") == ""
          or (.resources.limits.cpu // "") == ""
          or (.resources.limits.memory // "") == ""
        )
      | "\($kind)/\($name):\(.name)"
    ' <<<"$workloads"
)"
[[ -z "$missing_resources" ]] || fail "workloads lack CPU/memory requests or limits: $missing_resources"

database_version() {
    local database="$1"
    local table="$2"
    kubectl -n "$namespace" exec postgres-0 -- sh -ceu '
      export PGPASSWORD="$(tr -d "\r\n" < /opt/noebs-postgres/secrets/password)"
      exec psql -U noebs -d "$1" -XAtqc "SELECT COALESCE(MAX(version_id) FILTER (WHERE is_applied), 0) FROM $2"
    ' sh "$database" "$table"
}

identity_version="$(database_version identity_auth goose_db_version_identity_auth)"
card_vault_version="$(database_version card_vault goose_db_version_card_vault)"
[[ "$identity_version" =~ ^[0-9]+$ && "$identity_version" -ge 103 ]] || fail "identity-auth migration version is $identity_version, want at least 103"
[[ "$card_vault_version" =~ ^[0-9]+$ && "$card_vault_version" -ge 104 ]] || fail "card-vault migration version is $card_vault_version, want at least 104"

printf 'alpha post-deploy smoke (cluster): PASS revision=%s image=%s identity=%s card-vault=%s\n' \
    "$actual_revision" "$expected_digest" "$identity_version" "$card_vault_version"
REMOTE

printf 'alpha post-deploy smoke: checking public HTTPS edge without creating data\n'

test_response="$(curl --fail --silent --show-error --max-time 15 "$api_origin/test")"
[[ -n "$test_response" ]] || die "/test returned an empty response"

app_config="$(curl --fail --silent --show-error --max-time 15 "$api_origin/app/config")"
python3 -c '
import json, sys
payload = json.load(sys.stdin)
assert isinstance(payload.get("tenant_id"), str) and payload["tenant_id"]
wallet = payload.get("wallet")
assert isinstance(wallet, dict)
assert isinstance(wallet.get("enabled"), bool)
assert isinstance(wallet.get("default_currency"), str) and wallet["default_currency"]
assert isinstance(wallet.get("pin_required"), bool)
serialized = json.dumps(payload).lower()
for forbidden in ("jwt", "admin_key", "password", "secret", "private_key"):
    assert forbidden not in serialized
' <<<"$app_config" || die "/app/config is malformed or exposes a private field"

http_status() {
    local method="$1"
    local url="$2"
    curl --silent --show-error --output /dev/null --max-time 15 \
        --request "$method" --write-out '%{http_code}' "$url"
}

[[ "$(http_status GET "$api_origin/consumer/user")" == 401 ]] || die "protected user route did not reject an anonymous request"
[[ "$(http_status GET "$api_origin/metrics")" == 401 ]] || die "gateway metrics did not reject an anonymous request"
[[ "$(http_status POST "$api_origin/consumer/payment_request")" == 404 ]] || die "removed payment_request route is still exposed"

printf 'alpha post-deploy smoke: PASS revision=%s image=%s edge=%s\n' \
    "$expected_revision" "$expected_digest" "$api_origin"
