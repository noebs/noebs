#!/bin/bash
set -euo pipefail

# Entrypoint for noebs with SOPS + Litestream
# Config is merged from config.yaml + secrets.yaml at runtime

CONFIG_FILE="/app/config.yaml"
SECRETS_FILE="/app/secrets.yaml"
SECRETS_JSON="/app/.secrets.json"
DB_PATH_FILE="/app/.db_path"
LITESTREAM_CONFIG="/etc/litestream.yml"
AGE_KEY_FILE="/app/.sops/age-key.txt"
SOPS_KEY_FILE="/root/.config/sops/age/keys.txt"

if [[ -f "$SECRETS_FILE" ]]; then
    if [[ ! -f "$AGE_KEY_FILE" ]]; then
        echo "Missing age key at $AGE_KEY_FILE" >&2
        exit 1
    fi
    echo "Preparing SOPS age key..."
    mkdir -p "$(dirname "$SOPS_KEY_FILE")"
    chmod 700 "$(dirname "$SOPS_KEY_FILE")"
    cp "$AGE_KEY_FILE" "$SOPS_KEY_FILE"
    chmod 600 "$SOPS_KEY_FILE"
fi

echo "Rendering config + secrets..."
/usr/local/bin/render-configs "$CONFIG_FILE" "$SECRETS_FILE" "$SECRETS_JSON" "$LITESTREAM_CONFIG" "$DB_PATH_FILE"

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
