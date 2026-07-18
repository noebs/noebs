# Alpha auth E2E runbook

Run this only after the release image is pinned by immutable digest or commit tag, identity migrations (including `102_auth_hardening.sql`) have completed, and the API gateway and identity-auth rollout both report the same image digest. Do not create a disposable user in the live tenant while an older image is serving traffic.

## Synthetic OTP delivery

OTP plaintext is deliberately not stored or logged by Noebs; the database contains only an HMAC digest. Retrieve test OTPs at the delivery boundary using one of these approaches:

1. Use a team-owned SMS-capable virtual number and read its authenticated provider inbox/API. This exercises the production SMS route without involving a person's phone.
2. For repeatable automation, use a dedicated test tenant and an isolated identity-auth deployment whose `sms_gateway` points to an authenticated, short-retention in-cluster capture sink. Route only a test hostname to that deployment. Never point the shared production identity-auth instance at the sink, expose the sink publicly, or add an OTP read endpoint/database bypass.

The sink/provider account should redact recipient numbers, restrict reads to the release operator, delete messages after the run, and never export OTPs to CI logs.

## No-funds flow

Use a unique ten-digit virtual mobile, a disposable test tenant, and a fresh RSA key. The password must satisfy the current password policy. Commands below assume `jq`, OpenSSL, and an authenticated TLS endpoint.

```sh
export BASE_URL=https://api.noebs.sd
export TENANT_ID=alpha-e2e
export MOBILE=09XXXXXXXX
export PASSWORD='Replace9!Strong'

openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out /tmp/noebs-e2e-private.pem
PUBLIC_KEY=$(openssl pkey -in /tmp/noebs-e2e-private.pem -pubout \
  | sed '1d;$d' | tr -d '\n')

curl --fail-with-body "$BASE_URL/consumer/register" \
  -H "X-Tenant-ID: $TENANT_ID" -H 'Content-Type: application/json' \
  --data "$(jq -n --arg mobile "$MOBILE" --arg password "$PASSWORD" --arg key "$PUBLIC_KEY" \
    '{mobile:$mobile,username:$mobile,email:($mobile+"@example.invalid"),password:$password,user_pubkey:$key}')"

curl --fail-with-body "$BASE_URL/consumer/otp/generate" \
  -H "X-Tenant-ID: $TENANT_ID" -H 'Content-Type: application/json' \
  --data "$(jq -n --arg mobile "$MOBILE" '{mobile:$mobile}')"

# Read the six-digit code from the protected virtual-number inbox or capture sink.
read -rs OTP
curl --fail-with-body "$BASE_URL/consumer/otp/verify" \
  -H "X-Tenant-ID: $TENANT_ID" -H 'Content-Type: application/json' \
  --data "$(jq -n --arg mobile "$MOBILE" --arg otp "$OTP" '{mobile:$mobile,otp:$otp}')"

LOGIN=$(curl --fail-with-body "$BASE_URL/consumer/login" \
  -H "X-Tenant-ID: $TENANT_ID" -H 'Content-Type: application/json' \
  --data "$(jq -n --arg mobile "$MOBILE" --arg password "$PASSWORD" '{mobile:$mobile,password:$password}')")
OLD_TOKEN=$(jq -er .authorization <<<"$LOGIN")

curl --fail-with-body "$BASE_URL/consumer/auth/me" -H "Authorization: Bearer $OLD_TOKEN"
curl --fail-with-body "$BASE_URL/consumer/user" -H "Authorization: Bearer $OLD_TOKEN"
curl --fail-with-body "$BASE_URL/consumer/user/lang" -H "Authorization: Bearer $OLD_TOKEN"
curl --fail-with-body "$BASE_URL/consumer/get_cards" -H "Authorization: Bearer $OLD_TOKEN"
curl --fail-with-body "$BASE_URL/consumer/transactions" -H "Authorization: Bearer $OLD_TOKEN"

SIGNATURE=$(printf %s "$MOBILE" \
  | openssl dgst -sha256 -sign /tmp/noebs-e2e-private.pem -binary \
  | base64 -w0)
REFRESH_BODY=$(jq -n --arg token "$OLD_TOKEN" --arg signature "$SIGNATURE" --arg mobile "$MOBILE" \
  '{authorization:$token,signature:$signature,message:$mobile,mobile:$mobile}')
REFRESH=$(curl --fail-with-body "$BASE_URL/consumer/refresh" \
  -H "X-Tenant-ID: $TENANT_ID" -H 'Content-Type: application/json' --data "$REFRESH_BODY")
NEW_TOKEN=$(jq -er .authorization <<<"$REFRESH")
test "$NEW_TOKEN" != "$OLD_TOKEN"

# Replay must return HTTP 401 with code refresh_replay; do not use --fail here.
curl -i "$BASE_URL/consumer/refresh" \
  -H "X-Tenant-ID: $TENANT_ID" -H 'Content-Type: application/json' --data "$REFRESH_BODY"

curl --fail-with-body "$BASE_URL/consumer/auth/me" -H "Authorization: Bearer $NEW_TOKEN"
rm -f /tmp/noebs-e2e-private.pem
unset OTP SIGNATURE PASSWORD OLD_TOKEN NEW_TOKEN LOGIN REFRESH REFRESH_BODY PUBLIC_KEY
```

Expected results:

- registration returns 201; OTP generation returns 201; verification and password login succeed;
- the OTP succeeds once and the same OTP is rejected on reuse;
- read-only profile, empty card, and empty transaction calls complete without invoking EBS, PSP payout, card registration, wallet creation, or any money-moving endpoint;
- refresh returns a different JWT, the old JWT refresh replay returns `401 refresh_replay`, and the rotated JWT authenticates normally;
- `429 rate_limited` includes `Retry-After`; do not deliberately exhaust live limits during the smoke run.

## Cleanup

Prefer dropping the isolated test tenant databases/namespace after retaining redacted evidence. If a shared database must be used, have the release operator run a reviewed, tenant-scoped cleanup transaction across identity tables (`auth_accounts`, `otp_challenges`, `used_refresh_tokens`, `login_metrics`, `users`, and `auth_rate_limits`) and confirm that the synthetic user never acquired cards, wallets, funding sources, KYC, beneficiaries, or transactions. Remove the capture-sink messages and virtual-number inbox contents according to the test retention policy.
