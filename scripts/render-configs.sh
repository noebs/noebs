#!/bin/bash
set -euo pipefail

CONFIG_FILE="${1:-/app/config.yaml}"
SECRETS_FILE="${2:-/app/secrets.yaml}"
OUTPUT_JSON="${3:-/app/.secrets.json}"
OUTPUT_LITESTREAM="${4:-/etc/litestream.yml}"

if [[ ! -f "$CONFIG_FILE" ]]; then
    echo "Missing config file: $CONFIG_FILE" >&2
    exit 1
fi

mkdir -p "$(dirname "$OUTPUT_JSON")" "$(dirname "$OUTPUT_LITESTREAM")"
umask 077

rm -f "$OUTPUT_LITESTREAM"

TEMP_SECRETS=""
cleanup() {
    if [[ -n "$TEMP_SECRETS" ]]; then
        rm -f "$TEMP_SECRETS"
    fi
}
trap cleanup EXIT
if [[ -f "$SECRETS_FILE" ]]; then
    TEMP_SECRETS="$(mktemp)"
    sops -d "$SECRETS_FILE" > "$TEMP_SECRETS"
fi

python3 - "$CONFIG_FILE" "$OUTPUT_JSON" "$OUTPUT_LITESTREAM" "$TEMP_SECRETS" <<'PY'
import json
import sys

import yaml

config_path, output_json, output_litestream, secrets_path = sys.argv[1:5]

with open(config_path, "r", encoding="utf-8") as config_file:
    config = yaml.safe_load(config_file) or {}

secrets = {}
if secrets_path:
    with open(secrets_path, "r", encoding="utf-8") as secrets_file:
        secrets = yaml.safe_load(secrets_file) or {}


def merge(base, override):
    if isinstance(base, dict) and isinstance(override, dict):
        result = dict(base)
        for key, value in override.items():
            if key in result:
                result[key] = merge(result[key], value)
            else:
                result[key] = value
        return result
    if isinstance(override, str) and override == "":
        return base
    if isinstance(override, list) and len(override) == 0:
        return base
    if override is None:
        return base
    return override


merged = merge(config, secrets)

noebs = merged.get("noebs", {}) or {}
if not noebs.get("db_path"):
    noebs["db_path"] = "/data/noebs.db"

with open(output_json, "w", encoding="utf-8") as json_file:
    json.dump(noebs, json_file, indent=2)

litestream = merged.get("litestream", {}) or {}
dbs = litestream.get("dbs")
if isinstance(dbs, list) and dbs:
    r2 = merged.get("cloudflare_r2", merged.get("cloudflare", {})) or {}
    access_key = r2.get("access_key_id") or r2.get("access-key-id")
    secret_key = r2.get("secret_access_key") or r2.get("secret-access-key")
    endpoint = r2.get("endpoint")

    if access_key and secret_key:
        for db in dbs:
            if not db.get("path"):
                db["path"] = noebs["db_path"]
            replicas = db.get("replicas") or []
            for replica in replicas:
                if replica.get("type") == "s3":
                    replica.setdefault("access-key-id", access_key)
                    replica.setdefault("secret-access-key", secret_key)
                    if endpoint:
                        replica.setdefault("endpoint", endpoint)
        litestream_out = {"dbs": dbs}
        with open(output_litestream, "w", encoding="utf-8") as ls_file:
            yaml.safe_dump(litestream_out, ls_file, sort_keys=False)
    else:
        print("Litestream config present but missing access keys; skipping litestream.yml", file=sys.stderr)
PY
