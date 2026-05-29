#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 <postgres-host> <postgres-port> <postgres-user> <password-file> <service-database-sql>" >&2
}

if [ "$#" -ne 5 ]; then
  usage
  exit 2
fi

postgres_host="$1"
postgres_port="$2"
postgres_user="$3"
password_file="$4"
service_database_sql="$5"
pgpass="$(mktemp)"

cleanup() {
  rm -f "$pgpass"
}
trap cleanup EXIT

pgpass_escape() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//:/\\:}"
  printf '%s' "$value"
}

for label in postgres_host postgres_port postgres_user password_file service_database_sql; do
  value="${!label}"
  if [ -z "$value" ]; then
    echo "missing ${label}" >&2
    usage
    exit 2
  fi
done

case "$postgres_port" in
  ''|*[!0-9]*)
    echo "postgres_port must be numeric" >&2
    exit 2
    ;;
esac

if ! command -v psql >/dev/null 2>&1; then
  echo "missing psql; install postgresql-client before running this offline reset" >&2
  exit 1
fi
if [ ! -s "$password_file" ]; then
  echo "missing postgres password file: $password_file" >&2
  exit 1
fi
if [ ! -s "$service_database_sql" ]; then
  echo "missing service database SQL file: $service_database_sql" >&2
  exit 1
fi

password="$(tr -d '\r\n' < "$password_file")"
if [ -z "$password" ]; then
  echo "postgres password file is empty: $password_file" >&2
  exit 1
fi

printf '%s:%s:*:%s:%s\n' \
  "$(pgpass_escape "$postgres_host")" \
  "$(pgpass_escape "$postgres_port")" \
  "$(pgpass_escape "$postgres_user")" \
  "$(pgpass_escape "$password")" > "$pgpass"
chmod 0600 "$pgpass"

connection="host=$postgres_host port=$postgres_port dbname=postgres user=$postgres_user passfile=$pgpass"

psql "$connection" --set=ON_ERROR_STOP=1 <<'SQL'
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE datname IN (
  'noebs',
  'identity_auth',
  'card_vault',
  'ebs_adapter',
  'psp_webhook',
  'admin_reporting',
  'notification_chat',
  'consumer_beneficiary',
  'wallet_ledger'
)
AND pid <> pg_backend_pid();

DROP DATABASE IF EXISTS noebs WITH (FORCE);
DROP DATABASE IF EXISTS identity_auth WITH (FORCE);
DROP DATABASE IF EXISTS card_vault WITH (FORCE);
DROP DATABASE IF EXISTS ebs_adapter WITH (FORCE);
DROP DATABASE IF EXISTS psp_webhook WITH (FORCE);
DROP DATABASE IF EXISTS admin_reporting WITH (FORCE);
DROP DATABASE IF EXISTS notification_chat WITH (FORCE);
DROP DATABASE IF EXISTS consumer_beneficiary WITH (FORCE);
DROP DATABASE IF EXISTS wallet_ledger WITH (FORCE);
SQL

psql "$connection" --set=ON_ERROR_STOP=1 --file="$service_database_sql"
