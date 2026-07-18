#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$root/scripts/alpha-device-fixture.sh"
harness="$root/scripts/alpha-http-e2e.sh"
tenant="alpha-device-unit-a"
digest="ghcr.io/noebs/noebs@sha256:$(printf 'a%.0s' {1..64})"

bash -n "$script" "$root/scripts/alpha-http-e2e.sh"

"$script" check "$tenant" "$digest" 18080 >/dev/null
matcher="$($script matcher "$tenant" 18080)"

[[ "$matcher" == *"header X-Tenant-ID $tenant"* ]]
[[ "$matcher" == *"path /test /app/config /consumer/*"* ]]
[[ "$matcher" == *"reverse_proxy @alpha_device_fixture 127.0.0.1:18080"* ]]
[[ "$matcher" == *"not path /test /app/config /consumer/*"* ]]
[[ "$matcher" == *"respond @alpha_device_fixture_block 404"* ]]
[[ "$matcher" != *capture* ]]
[[ "$matcher" != *"/otp"* ]]
[[ "$matcher" != *"0.0.0.0"* ]]

grep -Fq 'label=com.docker.compose.project=$project' "$harness"
grep -Fq 'retained runtime %s for operator recovery' "$harness"
grep -Fq 'NOEBS_ALPHA_E2E_ALLOW_LOCAL_BUILD=true' "$harness"
grep -Fq 'password "$current_password"' "$harness"
grep -Fq 'password change did not rotate the token' "$harness"
grep -Fq 'password-session-revoked' "$harness"
if grep -Eq 'down .*\|\| true' "$harness"; then
    printf 'alpha device fixture test: Compose teardown failure is ignored\n' >&2
    exit 1
fi

if "$script" matcher tenant-cutover 18080 >/dev/null 2>&1; then
    printf 'alpha device fixture test: production tenant was accepted\n' >&2
    exit 1
fi
if "$script" check "$tenant" 'ghcr.io/noebs/noebs:master' 18080 >/dev/null 2>&1; then
    printf 'alpha device fixture test: mutable image was accepted\n' >&2
    exit 1
fi
if "$script" matcher "$tenant" 80 >/dev/null 2>&1; then
    printf 'alpha device fixture test: privileged port was accepted\n' >&2
    exit 1
fi

grep -Fq "TUTIPAY_TELEMETRY_ENABLED = 'false'" "$root/docs/alpha-device-fixture.md"
grep -Fq 'git merge-base --is-ancestor 872f12d5 HEAD' "$root/docs/alpha-device-fixture.md"
grep -Fq 'gradlew.bat :app:assembleDebug --no-daemon' "$root/docs/alpha-device-fixture.md"
grep -Fq 'adb shell pm clear com.tutipay.app.alpha' "$root/docs/alpha-device-fixture.md"
grep -Fq 'adb install --replace --grant-all' "$root/docs/alpha-device-fixture.md"
grep -Fq 'adb uninstall com.tutipay.app.alpha' "$root/docs/alpha-device-fixture.md"

printf 'alpha device fixture test: PASS\n'
