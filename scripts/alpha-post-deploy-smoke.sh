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

assert_application_ready() {
    local name="$1"
    local application actual_revision sync_status health_status
    application="$(kubectl -n argocd get application "$name" -o json)"
    actual_revision="$(jq -r '.status.sync.revision // ""' <<<"$application")"
    sync_status="$(jq -r '.status.sync.status // ""' <<<"$application")"
    health_status="$(jq -r '.status.health.status // ""' <<<"$application")"

    [[ "$actual_revision" == "$expected_revision" ]] || fail "$name Argo revision $actual_revision does not match $expected_revision"
    [[ "$sync_status" == Synced ]] || fail "$name Argo sync status is $sync_status"
    [[ "$health_status" == Healthy ]] || fail "$name Argo health status is $health_status"
}

assert_application_ready noebs
assert_application_ready noebs-edge

while IFS= read -r workload; do
    kubectl -n "$namespace" rollout status "$workload" --timeout=5m >/dev/null
done < <(kubectl -n "$namespace" get deployment,statefulset -o name | sort)

kubectl -n edge rollout status deployment/caddy --timeout=2m >/dev/null
expected_caddy_digest="sha256:834468128c7696cec0ceea6172f7d692daf645ae51983ca76e39da54a97c570d"
caddy_pod="$(kubectl -n edge get pods -l app.kubernetes.io/name=caddy -o json)"
[[ "$(jq '.items | length' <<<"$caddy_pod")" == 1 ]] || fail "edge must have exactly one Caddy pod"
[[ "$(jq -r '.items[0].spec.containers[0].image' <<<"$caddy_pod")" == "caddy@$expected_caddy_digest" ]] || fail "edge Caddy declaration is not pinned to the release digest"
[[ "$(jq -r '.items[0].status.containerStatuses[0].imageID' <<<"$caddy_pod")" == *@"$expected_caddy_digest" ]] || fail "running edge Caddy digest does not match its declaration"
[[ "$(jq -r '.items[0].status.containerStatuses[0].ready' <<<"$caddy_pod")" == true ]] || fail "edge Caddy is not ready"
[[ "$(jq -r '.items[0].status.containerStatuses[0].restartCount' <<<"$caddy_pod")" == 0 ]] || fail "edge Caddy restarted after rollout"
[[ "$(jq -r '.items[0].status.qosClass' <<<"$caddy_pod")" != BestEffort ]] || fail "edge Caddy is BestEffort"

caddy_deployment="$(kubectl -n edge get deployment caddy -o json)"
caddy_missing_resources="$(jq -r '
  .spec.template.spec.containers[]
  | select(
      (.resources.requests.cpu // "") == ""
      or (.resources.requests.memory // "") == ""
      or (.resources.limits.cpu // "") == ""
      or (.resources.limits.memory // "") == ""
    )
  | .name
' <<<"$caddy_deployment")"
[[ -z "$caddy_missing_resources" ]] || fail "edge Caddy lacks CPU/memory requests or limits"

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
    "$expected_revision" "$expected_digest" "$identity_version" "$card_vault_version"
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

python3 - "$api_origin" <<'PY' || die "Android App Links or payment-link fallback is invalid"
import json
import sys
import urllib.request

origin = sys.argv[1].rstrip("/")

with urllib.request.urlopen(origin + "/.well-known/assetlinks.json", timeout=15) as response:
    assert response.status == 200
    assert response.headers.get_content_type() == "application/json"
    links = json.load(response)

assert links == [{
    "relation": ["delegate_permission/common.handle_all_urls"],
    "target": {
        "namespace": "android_app",
        "package_name": "com.tutipay.app.alpha",
        "sha256_cert_fingerprints": [
            "B4:45:C2:79:FE:FB:B0:95:AA:33:4F:67:42:4D:EA:6B:52:77:38:EA:FF:A5:EF:FB:80:B5:E2:F5:9B:66:1C:AE",
        ],
    },
}]

payment_url = origin + "/pay/00000000-0000-4000-8000-000000000000"
with urllib.request.urlopen(payment_url, timeout=15) as response:
    assert response.status == 200
    assert response.headers.get_content_type() == "text/html"
    assert response.headers.get("Cache-Control") == "no-store"
    assert response.headers.get("Content-Security-Policy") == "default-src 'none'; base-uri 'none'; frame-ancestors 'none'"
    body = response.read(64 * 1024).decode("utf-8")

assert "Open this link with TutiPay Alpha" in body
assert "<form" not in body.lower()
PY

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
