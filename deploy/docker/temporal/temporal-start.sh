#!/usr/bin/env bash
set -euo pipefail

password_source="/opt/temporal/secrets/postgres-password"
broadcast_address_source="/opt/temporal/runtime/broadcast-address"
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
if [ ! -s "$broadcast_address_source" ]; then
  echo "missing Temporal broadcast address file: $broadcast_address_source" >&2
  exit 1
fi
if [ ! -s "$template_source" ]; then
  echo "missing Temporal config template: $template_source" >&2
  exit 1
fi

install -d -m 0700 "$runtime_config_dir"
password="$(sed_escape "$(tr -d '\r\n' < "$password_source")")"
broadcast_address="$(sed_escape "$(tr -d '\r\n' < "$broadcast_address_source")")"
sed \
  -e "s|__DATABASE_PASSWORD_FROM_FILE__|$password|g" \
  -e "s|__BROADCAST_ADDRESS_FROM_FILE__|$broadcast_address|g" \
  "$template_source" > "$runtime_config"
chmod 0600 "$runtime_config"

exec temporal-server --root "$runtime_root" --config config --allow-no-auth start
