#!/usr/bin/env bash
set -euo pipefail

password_source="/opt/temporal/secrets/postgres-password"
template_source="/opt/temporal/config/temporal.yaml"
runtime_root="/tmp/temporal"
runtime_config_dir="$runtime_root/config"
runtime_config="$runtime_config_dir/development.yaml"
export SSL_CERT_FILE="/opt/temporal/secrets/keycloak-ca.pem"

read_required_file() {
  local label="$1"
  local path="$2"
  local value

  if [ ! -s "$path" ]; then
    echo "missing $label file: $path" >&2
    exit 1
  fi

  value="$(tr -d '\r\n' < "$path")"
  if [ -z "$value" ]; then
    echo "empty $label file: $path" >&2
    exit 1
  fi

  printf '%s' "$value"
}

sed_escape() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//&/\\&}"
  value="${value//|/\\|}"
  printf '%s' "$value"
}

if [ ! -s "$template_source" ]; then
  echo "missing Temporal config template: $template_source" >&2
  exit 1
fi

install -d -m 0700 "$runtime_config_dir"
password="$(read_required_file "Temporal Postgres password" "$password_source")"
sed \
  -e "s|__DATABASE_PASSWORD_FROM_FILE__|$(sed_escape "$password")|g" \
  "$template_source" > "$runtime_config"
chmod 0600 "$runtime_config"

exec temporal-server --root "$runtime_root" --config config start \
  --service frontend \
  --service internal-frontend \
  --service history \
  --service matching \
  --service worker
