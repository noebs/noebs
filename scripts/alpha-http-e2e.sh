#!/usr/bin/env bash
set -Eeuo pipefail

umask 077

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
harness_dir="$root/scripts/alpha-http-e2e"
compose_file="$harness_dir/compose.yaml"
runtime=""
image=""
project=""
HTTP_BODY=""

die() {
    printf 'alpha HTTP E2E: %s\n' "$1" >&2
    exit 1
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

cleanup() {
    local status=$?
    trap - EXIT INT TERM

    if [[ -n "$runtime" && -n "$project" && -f "$compose_file" ]]; then
        COMPOSE_PROJECT_NAME="$project" \
        NOEBS_ALPHA_E2E_IMAGE="$image" \
        NOEBS_ALPHA_E2E_RUNTIME="$runtime" \
            docker compose --project-directory "$harness_dir" -f "$compose_file" \
            down --volumes --remove-orphans --timeout 5 >/dev/null 2>&1 || true
    fi
    if [[ -n "$image" ]]; then
        docker image rm --force "$image" >/dev/null 2>&1 || true
    fi
    if [[ -n "$runtime" && "$runtime" == /dev/shm/noebs-alpha-e2e-* ]]; then
        rm -rf -- "$runtime"
    fi

    if ((status != 0)); then
        printf 'alpha HTTP E2E: failed; isolated containers, databases, and in-memory secrets were removed\n' >&2
    fi
    exit "$status"
}

trap cleanup EXIT
trap 'exit 130' INT TERM

for command in base64 docker git jq openssl python3 sops; do
    require_command "$command"
done
docker compose version >/dev/null 2>&1 || die "Docker Compose is unavailable"
docker info >/dev/null 2>&1 || die "Docker daemon is unavailable"
[[ -d /dev/shm && -w /dev/shm ]] || die "a writable memory-backed /dev/shm is required"
[[ -f "$compose_file" ]] || die "missing harness compose file"

cd "$root"
[[ -z "$(git status --porcelain --untracked-files=normal)" ]] || \
    die "run from a clean checkout so the tested revision is reproducible"

commit="$(git rev-parse --verify HEAD)"
run_id="$(openssl rand -hex 6)"
project="noebs-alpha-$run_id"
image="noebs-alpha-e2e:${commit:0:12}-$run_id"
runtime="/dev/shm/noebs-alpha-e2e-$run_id"
mkdir -m 0700 "$runtime" "$runtime/services"

compose=(
    docker compose
    --project-directory "$harness_dir"
    -f "$compose_file"
)
export COMPOSE_PROJECT_NAME="$project"
export NOEBS_ALPHA_E2E_IMAGE="$image"
export NOEBS_ALPHA_E2E_RUNTIME="$runtime"

quiet_compose() {
    if ! "${compose[@]}" "$@" >"$runtime/compose-operation.log" 2>&1; then
        die "Docker Compose operation failed: $*"
    fi
    : >"$runtime/compose-operation.log"
}

printf 'alpha HTTP E2E: building revision %s\n' "${commit:0:12}"
if ! docker build --quiet \
    --label "org.opencontainers.image.revision=$commit" \
    --tag "$image" "$root" >"$runtime/build.log" 2>&1; then
    die "image build failed"
fi
: >"$runtime/build.log"

host_uid="$(id -u)"
host_gid="$(id -g)"
if ! docker run --rm \
    --user "$host_uid:$host_gid" \
    --volume "$runtime:/runtime" \
    --entrypoint age-keygen \
    "$image" -o /runtime/age-key.txt >"$runtime/age-keygen.log" 2>&1; then
    die "age key generation failed"
fi
recipient="$(docker run --rm \
    --user "$host_uid:$host_gid" \
    --volume "$runtime:/runtime:ro" \
    --entrypoint age-keygen \
    "$image" -y /runtime/age-key.txt 2>/dev/null)"
[[ "$recipient" == age1* ]] || die "age recipient derivation failed"
: >"$runtime/age-keygen.log"

postgres_password="$(openssl rand -hex 24)"
sms_key="$(openssl rand -hex 24)"
otp_read_token="$(openssl rand -hex 32)"
jwt_secret="$(openssl rand -hex 48)"
admin_key="$(openssl rand -hex 24)"
admin_password="$(openssl rand -hex 24)"
data_key="$(openssl rand -hex 32)"
google_secret="$(openssl rand -hex 24)"
old_password="A1!$(openssl rand -hex 6)"
new_password="B2@$(openssl rand -hex 6)"

printf '%s' "$postgres_password" >"$runtime/postgres-password.txt"
printf '%s' "$sms_key" >"$runtime/otp-sms-key.txt"
printf '%s' "$otp_read_token" >"$runtime/otp-read-token.txt"

cat >"$runtime/config.yaml" <<'YAML'
noebs:
  sops_age_key_file: /app/.sops/age-key.txt
  runtime_dir: /tmp/noebs
  port: ":8080"
  default_tenant_id: alpha-e2e
  payment_link_base: "https://pay.alpha.invalid/"
  is_debug: false
  otel_enabled: false
  cors:
    - "http://127.0.0.1"
  service_discovery:
    identity-auth: "http://identity-auth:8080"
    keycloak: "http://capture:8080/guard/keycloak"
    card-vault: "http://card-vault:8080"
    ebs-adapter: "http://capture:8080/guard/ebs-adapter"
    psp-webhook: "http://capture:8080/guard/psp-webhook"
    admin-reporting: "http://capture:8080/guard/admin-reporting"
    notification-chat: "http://capture:8080/guard/notification-chat"
    consumer-beneficiary: "http://consumer-beneficiary:8080"
    wallet-api: "http://capture:8080/guard/wallet-api"
  grpc_service_discovery:
    wallet-ledger: "capture:9090"
YAML

write_service_config() {
    local role="$1"
    local db_driver="${2:-}"
    {
        printf 'noebs:\n'
        printf '  service_role: %s\n' "$role"
        printf '  otel_service_name: %s\n' "$role"
        if [[ -n "$db_driver" ]]; then
            printf '  db_driver: %s\n' "$db_driver"
        fi
    } >"$runtime/services/$role.yaml"
}

write_service_config api-gateway
write_service_config identity-auth pgx
write_service_config identity-auth-migrate pgx
write_service_config card-vault pgx
write_service_config card-vault-migrate pgx
write_service_config consumer-beneficiary pgx
write_service_config consumer-beneficiary-migrate pgx

encrypt_yaml() {
    local target="$1"
    # The disposable recipient must not be replaced by release creation rules.
    if ! sops --config /dev/null --encrypt \
        --age "$recipient" \
        --input-type yaml \
        --output-type yaml \
        /dev/stdin >"$target" 2>"$runtime/sops.log"; then
        die "SOPS secret encryption failed"
    fi
    : >"$runtime/sops.log"
}

{
    printf 'noebs:\n'
    printf '  jwt_secret: "%s"\n' "$jwt_secret"
    printf '  admin_key: "%s"\n' "$admin_key"
    printf '  admin_user: "alpha-e2e"\n'
    printf '  admin_password: "%s"\n' "$admin_password"
} | encrypt_yaml "$runtime/api-gateway.secrets.yaml"

{
    printf 'noebs:\n'
    printf '  service_databases:\n'
    printf '    identity-auth: "postgres://noebs:%s@postgres:5432/identity_auth?sslmode=disable"\n' "$postgres_password"
    printf '  jwt_secret: "%s"\n' "$jwt_secret"
    printf '  sms_key: "%s"\n' "$sms_key"
    printf '  sms_sender: "alpha-e2e"\n'
    printf '  sms_gateway: "http://capture:8080/sms?"\n'
    printf '  sms_message: "Alpha test only."\n'
    printf '  google_client_id: "alpha-e2e-client"\n'
    printf '  google_client_secret: "%s"\n' "$google_secret"
    printf '  google_redirect_url: "https://auth.alpha.invalid/callback"\n'
} | encrypt_yaml "$runtime/identity-auth.secrets.yaml"

{
    printf 'noebs:\n'
    printf '  service_databases:\n'
    printf '    card-vault: "postgres://noebs:%s@postgres:5432/card_vault?sslmode=disable"\n' "$postgres_password"
    printf '  data_key: "%s"\n' "$data_key"
} | encrypt_yaml "$runtime/card-vault.secrets.yaml"

{
    printf 'noebs:\n'
    printf '  service_databases:\n'
    printf '    consumer-beneficiary: "postgres://noebs:%s@postgres:5432/consumer_beneficiary?sslmode=disable"\n' "$postgres_password"
} | encrypt_yaml "$runtime/consumer-beneficiary.secrets.yaml"

# Compose file-backed secrets retain their source permissions on some engines.
# The containing tmpfs directory is 0700; read-only mounts are limited per service.
chmod 0444 \
    "$runtime/age-key.txt" \
    "$runtime/config.yaml" \
    "$runtime/postgres-password.txt" \
    "$runtime/otp-sms-key.txt" \
    "$runtime/otp-read-token.txt" \
    "$runtime"/*.secrets.yaml \
    "$runtime"/services/*.yaml

printf 'alpha HTTP E2E: starting isolated services\n'
quiet_compose config --quiet
quiet_compose up --detach --wait --wait-timeout 120 postgres capture
for migration in identity-auth-migrate card-vault-migrate consumer-beneficiary-migrate; do
    quiet_compose run --rm --no-deps "$migration"
done
quiet_compose up --detach --wait --wait-timeout 120 \
    identity-auth card-vault consumer-beneficiary
quiet_compose up --detach --wait --wait-timeout 120 api-gateway

private_key="$runtime/user-private.pem"
public_key_file="$runtime/user-public.pem"
openssl genpkey \
    -algorithm RSA \
    -pkeyopt rsa_keygen_bits:2048 \
    -out "$private_key" 2>"$runtime/openssl.log"
openssl pkey -in "$private_key" -pubout -out "$public_key_file" 2>>"$runtime/openssl.log"
: >"$runtime/openssl.log"
public_key="$(<"$public_key_file")"

json_object() {
    (($# > 0 && $# % 2 == 0)) || die "internal JSON object error"
    local -a keys=()
    local -a values=()
    while (($#)); do
        keys+=("$1")
        values+=("$2")
        shift 2
    done
    {
        local value
        for value in "${values[@]}"; do
            printf '%s\0' "$value"
        done
    } | python3 -c '
import json
import sys

values = sys.stdin.buffer.read().split(b"\0")
if values and values[-1] == b"":
    values.pop()
if len(values) != len(sys.argv) - 1:
    raise SystemExit(1)
print(json.dumps(dict(zip(sys.argv[1:], (value.decode() for value in values))), separators=(",", ":")))
' "${keys[@]}"
}

request() {
    local method="$1"
    local path="$2"
    local mode="$3"
    local expected="$4"
    local body="${5-}"
    local tenant=""
    local bearer=""
    local relay_payload
    local response
    local status

    case "$mode" in
    public) tenant="alpha-e2e" ;;
    auth)
        [[ -n "${authorization:-}" ]] || die "internal missing authorization token"
        bearer="$authorization"
        ;;
    none) ;;
    *) die "internal unknown HTTP auth mode" ;;
    esac

    relay_payload="$(json_object \
        method "$method" \
        path "$path" \
        tenant "$tenant" \
        authorization "$bearer" \
        body "$body")"
    if ! response="$(printf '%s' "$relay_payload" | "${compose[@]}" exec -T capture \
        python /opt/noebs-e2e/capture.py relay 2>/dev/null)"; then
        die "HTTP transport failure for $method $path"
    fi
    status="$(printf '%s' "$response" | jq -er '.status' 2>/dev/null)" || \
        die "HTTP relay returned an invalid status for $method $path"
    HTTP_BODY="$(printf '%s' "$response" | jq -er '.body' 2>/dev/null)" || \
        die "HTTP relay returned an invalid body for $method $path"
    [[ "$status" == "$expected" ]] || \
        die "unexpected HTTP $status for $method $path (wanted $expected)"
}

assert_json() {
    local label="$1"
    local filter="$2"
    shift 2
    printf '%s' "$HTTP_BODY" | jq -e "$@" "$filter" >/dev/null 2>&1 || \
        die "response assertion failed: $label"
}

json_value() {
    local filter="$1"
    printf '%s' "$HTTP_BODY" | jq -er "$filter" 2>/dev/null || \
        die "response value missing"
}

mobile="0990000000"
pan_original="4111111111111111"
pan_updated="4242424242424242"

printf 'alpha HTTP E2E: registration, OTP, login, and token rotation\n'
body="$(json_object \
    mobile "$mobile" \
    password "$old_password" \
    fullname "Alpha Tester" \
    username "$mobile" \
    user_pubkey "$public_key")"
request POST /consumer/register public 201 "$body"
assert_json registration \
    '.details.mobile == "0990000000" and (.details.password // "") == "" and (.details.user_pubkey // "") == ""'

body="$(json_object mobile "$mobile" password "$old_password")"
request POST /consumer/login public 403 "$body"
assert_json unverified-login-rejected '.code == "user_not_verified"'

body="$(json_object mobile "$mobile")"
request POST /consumer/otp/generate public 201 "$body"
if ! otp="$("${compose[@]}" exec -T capture \
    python /opt/noebs-e2e/capture.py read 2>/dev/null)"; then
    die "OTP was not delivered to the isolated capture sink"
fi
[[ "$otp" =~ ^[0-9]{6}$ ]] || die "captured OTP was malformed"
"${compose[@]}" exec -T capture \
    python /opt/noebs-e2e/capture.py expect-empty >/dev/null 2>&1 || \
    die "OTP capture was not one-time"

body="$(json_object mobile "$mobile" otp "$otp")"
request POST /consumer/otp/verify public 200 "$body"
assert_json otp-verification '.result == "ok" and .user.is_verified == true'
request POST /consumer/otp/verify public 400 "$body"
assert_json otp-replay '.code == "invalid_otp"'
unset otp

body="$(json_object mobile "$mobile" password "$old_password")"
request POST /consumer/login public 200 "$body"
assert_json login '.authorization | type == "string" and length > 20'
authorization="$(json_value '.authorization')"
original_authorization="$authorization"

request GET /consumer/auth/me auth 200
assert_json auth-me '.user.mobile == "0990000000" and (.user.password // "") == ""'
request GET /consumer/user auth 200
assert_json user-profile '.username == "0990000000" and .fullname == "Alpha Tester"'

refresh_message="alpha-refresh-$run_id"
signature="$(printf '%s' "$refresh_message" | \
    openssl dgst -sha256 -sign "$private_key" 2>/dev/null | base64 | tr -d '\n')"
body="$(json_object \
    authorization "$original_authorization" \
    signature "$signature" \
    message "$refresh_message" \
    mobile "$mobile")"
request POST /consumer/refresh public 200 "$body"
assert_json refresh '.authorization | type == "string" and length > 20'
authorization="$(json_value '.authorization')"
[[ "$authorization" != "$original_authorization" ]] || die "refresh did not rotate the token"
request POST /consumer/refresh public 401 "$body"
assert_json refresh-replay '.code == "refresh_replay"'
unset original_authorization signature refresh_message

printf 'alpha HTTP E2E: password change, authenticated reads, and synthetic KYC\n'
body="$(json_object new_password "$new_password")"
request POST /consumer/change_password auth 200 "$body"
assert_json password-change '.result == "ok"'

body="$(json_object mobile "$mobile" password "$old_password")"
request POST /consumer/login public 400 "$body"
assert_json old-password-rejected '.code == "wrong_password"'
body="$(json_object mobile "$mobile" password "$new_password")"
request POST /consumer/login public 200 "$body"
authorization="$(json_value '.authorization')"
unset old_password new_password

body='{
  "selfie":"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
  "passport_image":"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
  "birth_date":"1990-01-02T00:00:00Z",
  "issue_date":"2025-01-02T00:00:00Z",
  "expiration_date":"2035-01-02T00:00:00Z",
  "national_number":"00000000000",
  "passport_number":"ALPHA0001",
  "gender":"unspecified",
  "nationality":"synthetic",
  "holder_name":"Alpha Tester"
}'
request POST /consumer/kyc auth 200 "$body"
assert_json kyc '.code == "ok"'

printf 'alpha HTTP E2E: card and zero-fund payment-link lifecycle\n'
body='[{"pan":"4111111111111111","exp_date":"3012","name":"Alpha E2E","ipin":"1234","is_main":true}]'
request POST /consumer/add_card auth 200 "$body"
assert_json card-create '.code == "ok"'
request GET /consumer/get_cards auth 200
assert_json card-read '.cards | length == 1 and .[0].pan == "4111111111111111"'

body='{"card_index":"4111111111111111","pan":"4242424242424242","exp_date":"3112","name":"Alpha E2E Updated","ipin":"5678"}'
request PUT /consumer/edit_card auth 201 "$body"
assert_json card-update '.result == "ok"'
body='{"PAN":"4242424242424242"}'
request POST /consumer/cards/set_main auth 200 "$body"
assert_json card-main '.result == "ok"'
request GET /consumer/get_cards auth 200
assert_json card-updated-read \
    '.cards | length == 1 and .[0].pan == "4242424242424242" and .[0].name == "Alpha E2E Updated" and .[0].is_main == true'

body='{"amount":0,"note":"alpha e2e"}'
request POST /consumer/payment_token auth 201 "$body"
assert_json payment-link-create \
    '.uuid | type == "string" and length > 20'
payment_uuid="$(json_value '.uuid')"
payment_token="$(json_value '.token')"
payment_link="$(json_value '.payment_link')"
[[ "$payment_link" == "https://pay.alpha.invalid/$payment_uuid" ]] || \
    die "payment link did not use the canonical isolated base"
if ! decoded_token="$(printf '%s' "$payment_token" | base64 --decode 2>/dev/null)"; then
    die "payment token was not valid base64"
fi
[[ "$decoded_token" != *"$pan_original"* && "$decoded_token" != *"$pan_updated"* ]] || \
    die "payment token exposed a raw PAN"
printf '%s' "$decoded_token" | \
    jq -e '.toCard == "424242*****4242"' >/dev/null 2>&1 || \
    die "payment token did not contain the expected masked destination"
unset decoded_token payment_link payment_token

request GET /consumer/payment_token auth 200
assert_json payment-link-list '.count == 1 and (.token | length == 1)'
request GET "/consumer/payment_token?uuid=$payment_uuid" auth 200
assert_json payment-link-read \
    '.uuid == $uuid and .amount == 0 and .toCard == "424242*****4242"' \
    --arg uuid "$payment_uuid"
request POST /consumer/payment_request auth 404 '{}'

body='{"card_index":"4242424242424242"}'
request DELETE /consumer/delete_card auth 200 "$body"
assert_json card-delete '.result == "ok"'
request GET /consumer/get_cards auth 404
assert_json card-delete-read '.cards == null and .main_card == null'

printf 'alpha HTTP E2E: beneficiary create, update, read, and delete\n'
body='{"data":"0991111111","bill_type":"p2p","name":"Alpha beneficiary"}'
request POST /consumer/beneficiary auth 201 "$body"
body='{"data":"0991111111","bill_type":"p2p","name":"Alpha beneficiary updated"}'
request POST /consumer/beneficiary auth 201 "$body"
request GET /consumer/beneficiary auth 200
assert_json beneficiary-read \
    'length == 1 and .[0].data == "0991111111" and .[0].name == "Alpha beneficiary updated"'
body='{"data":"0991111111"}'
request DELETE /consumer/beneficiary auth 204 "$body"
request GET /consumer/beneficiary auth 200
assert_json beneficiary-delete 'length == 0'

sql_scalar() {
    local database="$1"
    local query="$2"
    local expected="$3"
    local label="$4"
    local actual
    if ! actual="$("${compose[@]}" exec -T postgres \
        psql -U noebs -d "$database" -Atqc "$query" 2>/dev/null)"; then
        die "database assertion failed: $label"
    fi
    [[ "$actual" == "$expected" ]] || die "database assertion failed: $label"
}

printf 'alpha HTTP E2E: verifying persistence boundaries and zero side effects\n'
sql_scalar postgres \
    "SELECT count(*) FROM pg_database WHERE datname IN ('ebs_adapter','wallet_ledger','psp_webhook','admin_reporting')" \
    0 forbidden-service-databases
sql_scalar identity_auth "SELECT count(*) FROM users WHERE tenant_id='alpha-e2e'" 1 identity-user
sql_scalar identity_auth "SELECT count(*) FROM kyc WHERE tenant_id='alpha-e2e'" 1 identity-kyc
sql_scalar identity_auth "SELECT count(*) FROM used_refresh_tokens WHERE tenant_id='alpha-e2e'" 1 refresh-consumption
sql_scalar card_vault "SELECT count(*) FROM tokens WHERE tenant_id='alpha-e2e' AND amount=0" 1 payment-token
sql_scalar card_vault "SELECT count(*) FROM cards WHERE tenant_id='alpha-e2e' AND deleted_at IS NULL" 0 active-cards
sql_scalar consumer_beneficiary "SELECT count(*) FROM beneficiaries WHERE tenant_id='alpha-e2e'" 0 beneficiaries-cleaned

"${compose[@]}" exec -T capture \
    python /opt/noebs-e2e/capture.py assert-zero >/dev/null 2>&1 || \
    die "an EBS, wallet, PSP, reporting, notification, or Keycloak boundary was contacted"

printf 'alpha HTTP E2E: PASS revision %s; cleanup follows automatically\n' "${commit:0:12}"
