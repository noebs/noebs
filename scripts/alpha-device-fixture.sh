#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
matcher_template="$root/deploy/kubernetes/edge/alpha-device-fixture.Caddyfile.example"

usage() {
    printf 'usage:\n' >&2
    printf '  %s check <tenant> <ghcr-image@sha256:digest> [port]\n' "$0" >&2
    printf '  %s matcher <tenant> [port]\n' "$0" >&2
    printf '  %s start <tenant> <ghcr-image@sha256:digest> [port]\n' "$0" >&2
    exit 2
}

validate_tenant() {
    [[ "$1" =~ ^alpha-device-[a-z0-9][a-z0-9-]{0,62}$ ]] || {
        printf 'alpha device fixture: tenant must match alpha-device-[a-z0-9-] and be at most 76 characters\n' >&2
        exit 2
    }
}

validate_image() {
    [[ "$1" =~ ^ghcr\.io/noebs/noebs@sha256:[0-9a-f]{64}$ ]] || {
        printf 'alpha device fixture: image must be ghcr.io/noebs/noebs@sha256:<64 lowercase hex>\n' >&2
        exit 2
    }
}

validate_port() {
    [[ "$1" =~ ^[0-9]+$ ]] && (($1 >= 1024 && $1 <= 65535)) || {
        printf 'alpha device fixture: port must be between 1024 and 65535\n' >&2
        exit 2
    }
}

command="${1:-}"
tenant="${2:-}"
port="${4:-${3:-18080}}"

case "$command" in
check | start)
    (($# == 3 || $# == 4)) || usage
    image="$3"
    port="${4:-18080}"
    validate_tenant "$tenant"
    validate_image "$image"
    validate_port "$port"
    if [[ "$command" == check ]]; then
        printf 'alpha device fixture: inputs valid tenant=%s port=%s image=%s\n' "$tenant" "$port" "$image"
        exit 0
    fi
    export NOEBS_ALPHA_DEVICE_TENANT="$tenant"
    export NOEBS_ALPHA_DEVICE_PORT="$port"
    export NOEBS_ALPHA_E2E_PREBUILT_IMAGE="$image"
    exec "$root/scripts/alpha-http-e2e.sh" device
    ;;
matcher)
    (($# == 2 || $# == 3)) || usage
    port="${3:-18080}"
    validate_tenant "$tenant"
    validate_port "$port"
    [[ -f "$matcher_template" ]] || {
        printf 'alpha device fixture: missing Caddy matcher template\n' >&2
        exit 1
    }
    template="$(<"$matcher_template")"
    template="${template//alpha-device-REPLACE_WITH_RUN/$tenant}"
    template="${template//REPLACE_WITH_PORT/$port}"
    printf '%s\n' "$template"
    ;;
*)
    usage
    ;;
esac
