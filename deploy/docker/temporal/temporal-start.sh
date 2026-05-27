#!/usr/bin/env bash
set -euo pipefail

password_source="/opt/temporal/secrets/postgres-password"
template_source="/opt/temporal/config/temporal.yaml"
runtime_root="/tmp/temporal"
runtime_config_dir="$runtime_root/config"
runtime_config="$runtime_config_dir/development.yaml"

sed_escape() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//&/\\&}"
  value="${value//|/\\|}"
  printf '%s' "$value"
}

if [ ! -s "$password_source" ]; then
  echo "missing Temporal Postgres password file: $password_source" >&2
  exit 1
fi
if [ ! -s "$template_source" ]; then
  echo "missing Temporal config template: $template_source" >&2
  exit 1
fi

install -d -m 0700 "$runtime_config_dir"
password="$(sed_escape "$(tr -d '\r\n' < "$password_source")")"
broadcast_address="$(getent hosts "$(hostname)" | awk 'NR == 1 { print $1 }')"
if [ -z "$broadcast_address" ]; then
  echo "unable to resolve Temporal broadcast address for hostname: $(hostname)" >&2
  exit 1
fi
broadcast_address="$(sed_escape "$broadcast_address")"
sed \
  -e "s|__DATABASE_PASSWORD_FROM_FILE__|$password|g" \
  -e "s|__BROADCAST_ADDRESS_FROM_HOSTNAME__|$broadcast_address|g" \
  "$template_source" > "$runtime_config"
chmod 0600 "$runtime_config"

exec temporal-server --root "$runtime_root" --config config --allow-no-auth start
