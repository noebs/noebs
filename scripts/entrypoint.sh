#!/bin/bash
set -euo pipefail

# Entrypoint for noebs with SOPS + Litestream
# Config is merged from config.yaml + secrets.yaml at runtime

SECRETS_FILE="/app/secrets.yaml"
RUNTIME_DIR="/app/runtime"
DB_PATH_FILE="${RUNTIME_DIR}/.db_path"
AGE_KEY_FILE="/app/.sops/age-key.txt"

if [[ -f "$SECRETS_FILE" ]]; then
    if [[ ! -f "$AGE_KEY_FILE" ]]; then
        echo "Missing age key at $AGE_KEY_FILE" >&2
        exit 1
    fi
fi

echo "Rendering config + secrets..."
mkdir -p "$RUNTIME_DIR"
noebs render-config

LITESTREAM_CONFIG=""
if [[ -f "${RUNTIME_DIR}/litestream.yml" ]]; then
    LITESTREAM_CONFIG="${RUNTIME_DIR}/litestream.yml"
fi

if [[ -f "$LITESTREAM_CONFIG" ]]; then
    if [[ ! -f "$DB_PATH_FILE" ]]; then
        echo "Missing rendered database path at $DB_PATH_FILE" >&2
        exit 1
    fi
    DB_PATH="$(cat "$DB_PATH_FILE")"

    echo "Checking for existing database backup in R2..."
    litestream restore -if-replica-exists -config "$LITESTREAM_CONFIG" "$DB_PATH" || true

    echo "Starting noebs with Litestream replication..."
    exec litestream replicate -exec "noebs" -config "$LITESTREAM_CONFIG"
fi

echo "Starting noebs without Litestream replication..."
exec noebs
