#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
runtime="$(mktemp -d /dev/shm/noebs-alpha-e2e-static.XXXXXX)"
trap 'rm -rf -- "$runtime"' EXIT

mkdir "$runtime/services"
touch \
    "$runtime/age-key.txt" \
    "$runtime/postgres-password.txt" \
    "$runtime/otp-sms-key.txt" \
    "$runtime/otp-read-token.txt" \
    "$runtime/api-gateway.secrets.yaml" \
    "$runtime/identity-auth.secrets.yaml" \
    "$runtime/card-vault.secrets.yaml" \
    "$runtime/consumer-beneficiary.secrets.yaml"

bash -n "$root/scripts/alpha-http-e2e.sh"
"$root/scripts/alpha-device-fixture-test.sh"
python3 -B "$root/scripts/alpha-http-e2e/capture_test.py"

if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    COMPOSE_PROJECT_NAME=noebs-alpha-e2e-static \
    NOEBS_ALPHA_E2E_IMAGE=noebs-alpha-e2e:static \
    NOEBS_ALPHA_E2E_RUNTIME="$runtime" \
    NOEBS_ALPHA_DEVICE_PORT=18080 \
        docker compose \
        --project-directory "$root/scripts/alpha-http-e2e" \
        -f "$root/scripts/alpha-http-e2e/compose.yaml" \
        -f "$root/scripts/alpha-http-e2e/device-fixture.compose.yaml" \
        config --quiet
fi
