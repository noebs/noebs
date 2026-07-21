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

command -v sudo >/dev/null 2>&1 || fail "sudo is unavailable"
command -v k3s >/dev/null 2>&1 || fail "k3s is unavailable"
command -v jq >/dev/null 2>&1 || fail "jq is unavailable"
sudo -n true >/dev/null 2>&1 || fail "passwordless sudo is unavailable"
kubectl_cmd=(sudo -n k3s kubectl)

assert_application_ready() {
    local name="$1"
    local application target_revision actual_revision sync_status health_status
    application="$("${kubectl_cmd[@]}" -n argocd get application "$name" -o json)"
    target_revision="$(jq -r '.spec.source.targetRevision // ""' <<<"$application")"
    actual_revision="$(jq -r '.status.sync.revision // ""' <<<"$application")"
    sync_status="$(jq -r '.status.sync.status // ""' <<<"$application")"
    health_status="$(jq -r '.status.health.status // ""' <<<"$application")"

    [[ "$target_revision" == "$expected_revision" ]] || fail "$name Argo target revision $target_revision does not match $expected_revision"
    [[ "$actual_revision" == "$expected_revision" ]] || fail "$name Argo revision $actual_revision does not match $expected_revision"
    [[ "$sync_status" == Synced ]] || fail "$name Argo sync status is $sync_status"
    [[ "$health_status" == Healthy ]] || fail "$name Argo health status is $health_status"
}

assert_application_ready noebs
assert_application_ready noebs-edge

while IFS= read -r workload; do
    "${kubectl_cmd[@]}" -n "$namespace" rollout status "$workload" --timeout=5m >/dev/null
done < <("${kubectl_cmd[@]}" -n "$namespace" get deployment,statefulset -o name | sort)

"${kubectl_cmd[@]}" -n edge rollout status deployment/caddy --timeout=2m >/dev/null
expected_caddy_digest="sha256:834468128c7696cec0ceea6172f7d692daf645ae51983ca76e39da54a97c570d"
caddy_pod="$("${kubectl_cmd[@]}" -n edge get pods -l app.kubernetes.io/name=caddy -o json)"
[[ "$(jq '.items | length' <<<"$caddy_pod")" == 1 ]] || fail "edge must have exactly one Caddy pod"
[[ "$(jq -r '.items[0].spec.containers[0].image' <<<"$caddy_pod")" == "caddy@$expected_caddy_digest" ]] || fail "edge Caddy declaration is not pinned to the release digest"
[[ "$(jq -r '.items[0].status.containerStatuses[0].imageID' <<<"$caddy_pod")" == *@"$expected_caddy_digest" ]] || fail "running edge Caddy digest does not match its declaration"
[[ "$(jq -r '.items[0].status.containerStatuses[0].ready' <<<"$caddy_pod")" == true ]] || fail "edge Caddy is not ready"
[[ "$(jq -r '.items[0].status.containerStatuses[0].restartCount' <<<"$caddy_pod")" == 0 ]] || fail "edge Caddy restarted after rollout"
[[ "$(jq -r '.items[0].status.qosClass' <<<"$caddy_pod")" != BestEffort ]] || fail "edge Caddy is BestEffort"

caddy_deployment="$("${kubectl_cmd[@]}" -n edge get deployment caddy -o json)"
caddy_config_name="$(jq -r '.spec.template.spec.volumes[] | select(.name == "config") | .configMap.name' <<<"$caddy_deployment")"
[[ "$caddy_config_name" =~ ^caddy-config-[a-z0-9]+$ ]] || fail "edge Caddy does not reference a content-addressed ConfigMap: $caddy_config_name"
if "${kubectl_cmd[@]}" -n edge get configmap caddy-config >/dev/null 2>&1; then
    fail "obsolete un-hashed edge/caddy-config remains"
fi
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
pods="$("${kubectl_cmd[@]}" -n "$namespace" get pods -o json)"

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

workloads="$("${kubectl_cmd[@]}" -n "$namespace" get deployment,statefulset -o json)"
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

if "${kubectl_cmd[@]}" -n "$namespace" get ingress api-gateway >/dev/null 2>&1; then
    fail "obsolete api-gateway Ingress remains"
fi
if "${kubectl_cmd[@]}" -n "$namespace" get secret noebs-tls >/dev/null 2>&1; then
    fail "obsolete noebs-tls Secret remains"
fi
for retired in \
    deployment/consumer-beneficiary \
    service/consumer-beneficiary \
    secret/consumer-beneficiary-secrets \
    secret/consumer-beneficiary-migrate-secrets \
    secret/psp-webhook-migrate-secrets \
    serviceaccount/psp-webhook-migrate \
    job/noebs-psp-webhook-migrate
do
    if "${kubectl_cmd[@]}" -n "$namespace" get "$retired" >/dev/null 2>&1; then
        fail "retired resource remains: $retired"
    fi
done

database_migration_set() {
    local database="$1"
    local table="$2"
    "${kubectl_cmd[@]}" -n "$namespace" exec postgres-0 -- sh -ceu '
      exec gosu postgres psql -U postgres -d "$1" -XAtqc \
        "SELECT string_agg(version_id::text || chr(58) || is_applied::text, chr(44) ORDER BY version_id, id) FROM $2"
    ' sh "$database" "$table"
}

identity_migrations="$(database_migration_set identity_auth goose_db_version_identity_auth)"
card_vault_migrations="$(database_migration_set card_vault goose_db_version_card_vault)"
ebs_adapter_migrations="$(database_migration_set ebs_adapter goose_db_version_ebs_adapter)"
admin_reporting_migrations="$(database_migration_set admin_reporting goose_db_version_admin_reporting)"
notification_chat_migrations="$(database_migration_set notification_chat goose_db_version_notification_chat)"
wallet_ledger_migrations="$(database_migration_set wallet_ledger goose_db_version_wallet_ledger)"
workload_auth_migrations="$(database_migration_set workload_auth goose_db_version_workload_auth)"
gateway_auth_migrations="$(database_migration_set gateway_auth goose_db_version_gateway_auth)"
authority_marker_status="$("${kubectl_cmd[@]}" -n "$namespace" exec postgres-0 -- sh -ceu '
  test -f /var/lib/postgresql/data/.noebs-postgres-authority
  printf current
')"
topology_drift_count="$("${kubectl_cmd[@]}" -n "$namespace" exec postgres-0 -- sh -ceu '
  exec gosu postgres psql -U postgres -d postgres -XAtqc \
    "WITH expected_roles(name) AS (VALUES
       ('\''admin_reporting_migrate'\''), ('\''admin_reporting_projector'\''), ('\''admin_reporting_runtime'\''),
       ('\''card_vault_migrate'\''), ('\''card_vault_runtime'\''),
       ('\''ebs_adapter_events'\''), ('\''ebs_adapter_migrate'\''), ('\''ebs_adapter_runtime'\''),
       ('\''gateway_auth_cleanup'\''), ('\''gateway_auth_migrate'\''), ('\''gateway_auth_runtime'\''),
       ('\''identity_auth_migrate'\''), ('\''identity_auth_runtime'\''),
       ('\''notification_chat_migrate'\''), ('\''notification_chat_runtime'\''),
       ('\''wallet_ledger_migrate'\''), ('\''wallet_ledger_runtime'\''), ('\''wallet_ledger_webhook'\''), ('\''wallet_ledger_worker'\''),
       ('\''workload_auth_cleanup'\''), ('\''workload_auth_migrate'\''), ('\''workload_auth_runtime'\'')),
     actual_roles(name) AS (
       SELECT rolname::text
       FROM pg_roles
       WHERE rolname <> '\''postgres'\'' AND rolname !~ '\''^pg_'\''
     ),
     expected_databases(name) AS (VALUES
       ('\''admin_reporting'\''), ('\''card_vault'\''), ('\''ebs_adapter'\''), ('\''gateway_auth'\''),
       ('\''identity_auth'\''), ('\''notification_chat'\''), ('\''wallet_ledger'\''), ('\''workload_auth'\'')),
     actual_databases(name) AS (
       SELECT datname::text FROM pg_database
       WHERE datallowconn AND NOT datistemplate AND datname <> '\''postgres'\''
     )
     SELECT
       (SELECT count(*) FROM (
          (SELECT name FROM expected_roles EXCEPT SELECT name FROM actual_roles)
          UNION ALL
          (SELECT name FROM actual_roles EXCEPT SELECT name FROM expected_roles)
        ) role_drift) +
       (SELECT count(*) FROM (
          (SELECT name FROM expected_databases EXCEPT SELECT name FROM actual_databases)
          UNION ALL
          (SELECT name FROM actual_databases EXCEPT SELECT name FROM expected_databases)
        ) database_drift)"
')"
[[ "$authority_marker_status" == current ]] || fail "Postgres authority marker is missing"
[[ "$topology_drift_count" == 0 ]] || fail "Postgres role or service-database topology drift count is $topology_drift_count"
for actual_expected_label in \
    "$identity_migrations|0:true,1:true|identity-auth" \
    "$card_vault_migrations|0:true,1:true|card-vault" \
    "$ebs_adapter_migrations|0:true,1:true|ebs-adapter" \
    "$admin_reporting_migrations|0:true,1:true|admin-reporting" \
    "$notification_chat_migrations|0:true,1:true|notification-chat" \
    "$wallet_ledger_migrations|0:true,1:true,2:true|wallet-ledger" \
    "$workload_auth_migrations|0:true,1:true|workload-auth" \
    "$gateway_auth_migrations|0:true,1:true|gateway-auth"
do
    IFS='|' read -r actual expected label <<<"$actual_expected_label"
    [[ "$actual" == "$expected" ]] || fail "$label migration set is $actual, want exactly $expected"
done

printf 'alpha post-deploy smoke (cluster): PASS revision=%s image=%s migrations=%s/%s/%s/%s/%s/%s/%s/%s roles=22 databases=8\n' \
    "$expected_revision" "$expected_digest" "$identity_migrations" "$card_vault_migrations" "$ebs_adapter_migrations" \
    "$admin_reporting_migrations" "$notification_chat_migrations" "$wallet_ledger_migrations" \
    "$workload_auth_migrations" "$gateway_auth_migrations"
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
serialized = json.dumps(payload).lower()
for forbidden in ("jwt", "admin_key", "password", "secret", "private_key"):
    assert forbidden not in serialized
' <<<"$app_config" || die "/app/config is malformed or exposes a private field"

python3 - "$api_origin" <<'PY' || die "Android App Links configuration is invalid"
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

PY

python3 - "$api_origin" <<'PY' || die "Keycloak discovery or JWKS contract is invalid"
import json
import sys
import urllib.request

origin = sys.argv[1].rstrip("/")
issuer = origin + "/auth/realms/noebs"
with urllib.request.urlopen(issuer + "/.well-known/openid-configuration", timeout=15) as response:
    assert response.status == 200
    assert response.headers.get_content_type() == "application/json"
    discovery = json.load(response)

assert discovery["issuer"] == issuer
assert discovery["authorization_endpoint"] == issuer + "/protocol/openid-connect/auth"
assert discovery["token_endpoint"] == issuer + "/protocol/openid-connect/token"
assert discovery["jwks_uri"] == issuer + "/protocol/openid-connect/certs"
assert "RS256" in discovery["id_token_signing_alg_values_supported"]

with urllib.request.urlopen(discovery["jwks_uri"], timeout=15) as response:
    assert response.status == 200
    assert response.headers.get_content_type() == "application/json"
    jwks = json.load(response)

assert any(
    key.get("kty") == "RSA"
    and key.get("use") == "sig"
    and key.get("alg") == "RS256"
    and isinstance(key.get("kid"), str)
    and key["kid"]
    for key in jwks.get("keys", [])
)
PY

python3 - "$api_origin" <<'PY' || die "Keycloak authorization-code browser surface is invalid"
import http.cookiejar
import sys
import urllib.error
import urllib.parse
import urllib.request


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, file_pointer, code, message, headers, new_url):
        return None


def open_without_redirect(opener, request):
    try:
        return opener.open(request, timeout=15)
    except urllib.error.HTTPError as error:
        return error


origin = sys.argv[1].rstrip("/")
issuer = origin + "/auth/realms/noebs"
authorization = issuer + "/protocol/openid-connect/auth?" + urllib.parse.urlencode({
    "client_id": "noebs-mobile",
    "redirect_uri": origin + "/mobile/oauth/callback",
    "response_type": "code",
    "response_mode": "query",
    "scope": "openid organization:*",
    "state": "post-deploy-state",
    "nonce": "post-deploy-nonce",
    "code_challenge": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
    "code_challenge_method": "S256",
})

opener = urllib.request.build_opener(
    urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),
    NoRedirect(),
)
response = open_without_redirect(opener, authorization)
assert response.status in (302, 303)
assert b'name="username"' not in response.read(1024 * 1024)
broker_login = urllib.parse.urljoin(authorization, response.headers["Location"])
parsed_broker = urllib.parse.urlparse(broker_login)
assert parsed_broker.scheme + "://" + parsed_broker.netloc == origin
assert parsed_broker.path == "/auth/realms/noebs/broker/google/login"

provider_response = open_without_redirect(opener, broker_login)
assert provider_response.status in (302, 303)
assert b'name="password"' not in provider_response.read(1024 * 1024)
provider_url = urllib.parse.urlparse(provider_response.headers["Location"])
assert provider_url.scheme == "https" and provider_url.netloc == "accounts.google.com"
provider_query = urllib.parse.parse_qs(provider_url.query)
assert provider_query["redirect_uri"] == [issuer + "/broker/google/endpoint"]

required_action = issuer + "/login-actions/required-action"
assert open_without_redirect(opener, required_action).status == 400
required_post = urllib.request.Request(required_action, data=b"", method="POST")
assert open_without_redirect(opener, required_post).status == 400
PY

http_status() {
    local method="$1"
    local url="$2"
    curl --silent --show-error --output /dev/null --max-time 15 \
        --request "$method" --write-out '%{http_code}' "$url"
}

[[ "$(http_status GET "$api_origin/consumer/user")" == 401 ]] || die "protected user route did not reject an anonymous request"
[[ "$(http_status GET "$api_origin/metrics")" == 404 ]] || die "gateway exposed a metrics route"
[[ "$(http_status POST "$api_origin/consumer/payment_request")" == 404 ]] || die "removed payment_request route is still exposed"
[[ "$(http_status GET "$api_origin/auth/admin/")" == 404 ]] || die "Keycloak Admin API is publicly reachable"
[[ "$(http_status GET "$api_origin/auth/realms/master/.well-known/openid-configuration")" == 404 ]] || die "non-noebs Keycloak realm is publicly reachable"
[[ "$(http_status POST "$api_origin/auth/realms/noebs/clients-registrations/openid-connect")" == 404 ]] || die "Keycloak client registration is publicly reachable"
[[ "$(http_status POST "$api_origin/auth/realms/noebs/clients-registrations/default")" == 404 ]] || die "Keycloak default client registration is publicly reachable"
[[ "$(http_status GET "$api_origin/auth/realms/noebs/clients-registrations/saml2-entity-descriptor")" == 404 ]] || die "Keycloak SAML client registration is publicly reachable"
[[ "$(http_status GET "$api_origin/auth/realms/noebs/protocol/saml")" == 404 ]] || die "unused Keycloak SAML endpoint is publicly reachable"
[[ "$(http_status GET "$api_origin/auth/realms/noebs/account")" == 404 ]] || die "Keycloak account console is publicly reachable"
[[ "$(http_status GET "$api_origin/auth/realms/noebs/.well-known/uma2-configuration")" == 404 ]] || die "unused Keycloak UMA discovery is publicly reachable"
[[ "$(http_status GET "$api_origin/auth/realms/noebs/protocol/openid-connect/token")" == 404 ]] || die "Keycloak token endpoint accepts a public GET route"
[[ "$(http_status POST "$api_origin/auth/realms/noebs/protocol/openid-connect/auth")" == 404 ]] || die "Keycloak authorization endpoint accepts a public POST route"
[[ "$(http_status GET "$api_origin/auth/realms/noebs/protocol/openid-connect/userinfo")" == 404 ]] || die "unused Keycloak userinfo endpoint is publicly reachable"
[[ "$(http_status POST "$api_origin/auth/realms/noebs/protocol/openid-connect/token/introspect")" == 404 ]] || die "unused Keycloak introspection endpoint is publicly reachable"
[[ "$(http_status POST "$api_origin/auth/realms/noebs/protocol/openid-connect/revoke")" == 404 ]] || die "unused Keycloak revocation endpoint is publicly reachable"
[[ "$(http_status POST "$api_origin/auth/realms/noebs/protocol/openid-connect/auth/device")" == 404 ]] || die "unused Keycloak device authorization endpoint is publicly reachable"
[[ "$(http_status POST "$api_origin/auth/realms/noebs/protocol/openid-connect/ext/par/request")" == 404 ]] || die "unused Keycloak pushed authorization endpoint is publicly reachable"
[[ "$(http_status POST "$api_origin/auth/realms/noebs/protocol/openid-connect/ext/ciba/auth")" == 404 ]] || die "unused Keycloak CIBA endpoint is publicly reachable"
[[ "$(http_status GET "$api_origin/auth/realms/noebs/protocol/openid-connect/login-status-iframe.html")" == 404 ]] || die "unused Keycloak login-status endpoint is publicly reachable"
[[ "$(http_status GET "$api_origin/auth/realms/noebs/login-actions/registration")" == 404 ]] || die "Keycloak self-registration action is publicly reachable"
[[ "$(http_status GET "$api_origin/auth/realms/noebs/login-actions/reset-credentials")" == 404 ]] || die "Keycloak password-reset action is publicly reachable"
[[ "$(http_status GET "$api_origin/auth/realms/noebs/broker/google/link")" == 404 ]] || die "Keycloak broker account-linking endpoint is publicly reachable"
[[ "$(http_status GET "$api_origin/auth/realms/noebs/broker/google/token")" == 404 ]] || die "Keycloak broker external-token endpoint is publicly reachable"

printf 'alpha post-deploy smoke: PASS revision=%s image=%s edge=%s\n' \
    "$expected_revision" "$expected_digest" "$api_origin"
