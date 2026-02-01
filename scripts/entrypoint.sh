#!/bin/bash
set -euo pipefail

# Entrypoint for noebs with SOPS + Litestream
# Config is merged from config.yaml + secrets.yaml at runtime

SECRETS_FILE="/app/secrets.yaml"
RUNTIME_DIR="${NOEBS_RUNTIME_DIR:-/app}"
DB_PATH_FILE="${RUNTIME_DIR}/.db_path"
AGE_KEY_FILE="${SOPS_AGE_KEY_FILE:-/app/.sops/age-key.txt}"
DEFAULT_AGE_KEY_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/sops/age/keys.txt"

if [[ -n "${NOEBS_RUNTIME_DIR:-}" ]]; then
    mkdir -p "$RUNTIME_DIR"
fi

if [[ -f "$SECRETS_FILE" ]]; then
    if [[ -n "${SOPS_AGE_KEY:-}" ]]; then
        : # key provided via env
    elif [[ -f "$AGE_KEY_FILE" ]]; then
        export SOPS_AGE_KEY_FILE="$AGE_KEY_FILE"
    elif [[ -f "$DEFAULT_AGE_KEY_FILE" ]]; then
        export SOPS_AGE_KEY_FILE="$DEFAULT_AGE_KEY_FILE"
    else
        echo "Missing age key at $AGE_KEY_FILE" >&2
        exit 1
    fi
fi

echo "Rendering config + secrets..."
export NOEBS_RUNTIME_DIR="$RUNTIME_DIR"
noebs render-config

LITESTREAM_CONFIG=""
if [[ -f "${RUNTIME_DIR}/litestream.yml" ]]; then
    LITESTREAM_CONFIG="${RUNTIME_DIR}/litestream.yml"
elif [[ -n "${NOEBS_LITESTREAM_CONFIG:-}" ]]; then
    LITESTREAM_CONFIG="${NOEBS_LITESTREAM_CONFIG}"
fi

if [[ -f "$LITESTREAM_CONFIG" ]]; then
    if [[ -f "$DB_PATH_FILE" ]]; then
        DB_PATH="$(cat "$DB_PATH_FILE")"
    else
        DB_PATH="/data/noebs.db"
    fi

    echo "Checking for existing database backup in R2..."
    litestream restore -if-replica-exists -config "$LITESTREAM_CONFIG" "$DB_PATH" || true

    echo "Starting noebs with Litestream replication..."
    exec litestream replicate -exec "noebs" -config "$LITESTREAM_CONFIG"
fi

echo "Starting noebs without Litestream replication..."
exec noebs
