#!/bin/bash
set -euo pipefail

# Entrypoint for noebs service workloads.
# Config is mounted and merged from config.yaml + service.yaml + secrets.yaml.

CONFIG_FILE="/app/config.yaml"
SERVICE_FILE="/app/service.yaml"
SECRETS_FILE="/app/secrets.yaml"
AGE_KEY_FILE="/app/.sops/age-key.txt"

require_file() {
    local label="$1"
    local path="$2"
    if [[ ! -f "$path" ]]; then
        echo "Missing $label at $path" >&2
        exit 1
    fi
}

require_file "config" "$CONFIG_FILE"
require_file "service config" "$SERVICE_FILE"
require_file "secrets" "$SECRETS_FILE"
require_file "age key" "$AGE_KEY_FILE"

echo "Starting noebs service runtime..."
exec noebs
