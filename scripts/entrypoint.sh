#!/bin/bash
set -euo pipefail

# Entrypoint for noebs with SOPS + Litestream
# Config is merged from config.yaml + secrets.yaml at runtime

CONFIG_FILE="/app/config.yaml"
SECRETS_FILE="/app/secrets.yaml"
SECRETS_JSON="/app/.secrets.json"
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
/usr/local/bin/render-configs "$CONFIG_FILE" "$SECRETS_FILE" "$SECRETS_JSON" "$LITESTREAM_CONFIG"

if [[ -f "$LITESTREAM_CONFIG" ]]; then
    DB_PATH="$(python3 - <<'PY'
import yaml

db_path = "/data/noebs.db"
try:
    with open("/etc/litestream.yml", "r", encoding="utf-8") as f:
        data = yaml.safe_load(f) or {}
    dbs = data.get("dbs", [])
    if isinstance(dbs, list) and dbs and dbs[0].get("path"):
        db_path = dbs[0]["path"]
except Exception:
    pass

print(db_path)
PY
)"

    echo "Checking for existing database backup in R2..."
    litestream restore -if-replica-exists -config "$LITESTREAM_CONFIG" "$DB_PATH" || true

    echo "Starting noebs with Litestream replication..."
    exec litestream replicate -exec "noebs" -config "$LITESTREAM_CONFIG"
fi

echo "Starting noebs without Litestream replication..."
exec noebs
