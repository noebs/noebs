#!/usr/bin/env bash
set -euo pipefail

pgdata="/var/lib/postgresql/data"
postgres_user="noebs"
password_source="/opt/noebs-postgres/secrets/password"
tls_certificate_source="/opt/noebs-postgres/secrets/tls.crt"
tls_private_key_source="/opt/noebs-postgres/secrets/tls.key"
migrate_password_source="/run/secrets/workload-auth-migrate-password"
runtime_password_source="/run/secrets/workload-auth-runtime-password"
cleanup_password_source="/run/secrets/workload-auth-cleanup-password"
gateway_migrate_password_source="/run/secrets/gateway-auth-migrate-password"
gateway_runtime_password_source="/run/secrets/gateway-auth-runtime-password"
gateway_cleanup_password_source="/run/secrets/gateway-auth-cleanup-password"
password_runtime="/tmp/noebs-postgres-password"
service_database_sql="/opt/noebs-postgres/init/001-service-databases.sql"
pgpass="/var/lib/postgresql/.pgpass"

cleanup() {
  rm -f "$password_runtime" "$pgpass"
}
trap cleanup EXIT

pgpass_escape() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//:/\\:}"
  printf '%s' "$value"
}

if [ ! -s "$password_source" ]; then
  echo "missing Noebs Postgres password file: $password_source" >&2
  exit 1
fi
for role_password_source in \
  "$migrate_password_source" \
  "$runtime_password_source" \
  "$cleanup_password_source" \
  "$gateway_migrate_password_source" \
  "$gateway_runtime_password_source" \
  "$gateway_cleanup_password_source"; do
  if [ ! -s "$role_password_source" ]; then
    echo "missing Postgres role password file: $role_password_source" >&2
    exit 1
  fi
done
if [ ! -s "$service_database_sql" ]; then
  echo "missing Noebs service database SQL file: $service_database_sql" >&2
  exit 1
fi

tls_enabled=false
if [ -s "$tls_certificate_source" ] || [ -s "$tls_private_key_source" ]; then
  if [ ! -s "$tls_certificate_source" ] || [ ! -s "$tls_private_key_source" ]; then
    echo "Noebs Postgres TLS certificate and private key must be supplied together" >&2
    exit 1
  fi
  tls_enabled=true
fi

install -d -o postgres -g postgres -m 0700 "$pgdata"
password="$(tr -d '\r\n' < "$password_source")"
export NOEBS_WORKLOAD_AUTH_MIGRATE_PASSWORD="$(tr -d '\r\n' < "$migrate_password_source")"
export NOEBS_WORKLOAD_AUTH_RUNTIME_PASSWORD="$(tr -d '\r\n' < "$runtime_password_source")"
export NOEBS_WORKLOAD_AUTH_CLEANUP_PASSWORD="$(tr -d '\r\n' < "$cleanup_password_source")"
export NOEBS_GATEWAY_AUTH_MIGRATE_PASSWORD="$(tr -d '\r\n' < "$gateway_migrate_password_source")"
export NOEBS_GATEWAY_AUTH_RUNTIME_PASSWORD="$(tr -d '\r\n' < "$gateway_runtime_password_source")"
export NOEBS_GATEWAY_AUTH_CLEANUP_PASSWORD="$(tr -d '\r\n' < "$gateway_cleanup_password_source")"

if [ ! -s "$pgdata/PG_VERSION" ]; then
  printf '%s\n' "$password" > "$password_runtime"
  chown postgres:postgres "$password_runtime"
  chmod 0600 "$password_runtime"

  gosu postgres initdb \
    -D "$pgdata" \
    --auth-host=scram-sha-256 \
    --auth-local=scram-sha-256 \
    --username="$postgres_user" \
    --pwfile="$password_runtime"

fi

sed -i \
  -e '/^# noebs managed host auth begin$/,/^# noebs managed host auth end$/d' \
  -e '/^host all all all scram-sha-256$/d' \
  "$pgdata/pg_hba.conf"
{
  echo "# noebs managed host auth begin"
  if [ "$tls_enabled" = true ]; then
    echo "hostssl all all all scram-sha-256"
    echo "hostnossl all all all reject"
  else
    echo "host all all all scram-sha-256"
  fi
  echo "# noebs managed host auth end"
} >> "$pgdata/pg_hba.conf"

printf '/tmp:5432:*:%s:%s\n' "$(pgpass_escape "$postgres_user")" "$(pgpass_escape "$password")" > "$pgpass"
chown postgres:postgres "$pgpass"
chmod 0600 "$pgpass"

gosu postgres pg_ctl \
  -D "$pgdata" \
  -o "-c listen_addresses='' -c unix_socket_directories=/tmp" \
  -w start
gosu postgres psql \
  "host=/tmp dbname=postgres user=$postgres_user passfile=$pgpass" \
  --set=ON_ERROR_STOP=1 \
  --file="$service_database_sql"
gosu postgres pg_ctl -D "$pgdata" -m fast -w stop

unset NOEBS_WORKLOAD_AUTH_MIGRATE_PASSWORD
unset NOEBS_WORKLOAD_AUTH_RUNTIME_PASSWORD
unset NOEBS_WORKLOAD_AUTH_CLEANUP_PASSWORD
unset NOEBS_GATEWAY_AUTH_MIGRATE_PASSWORD
unset NOEBS_GATEWAY_AUTH_RUNTIME_PASSWORD
unset NOEBS_GATEWAY_AUTH_CLEANUP_PASSWORD

postgres_args=(-D "$pgdata" -c "listen_addresses=*")
if [ "$tls_enabled" = true ]; then
  postgres_args+=(
    -c "ssl=on"
    -c "ssl_min_protocol_version=TLSv1.3"
    -c "ssl_cert_file=$tls_certificate_source"
    -c "ssl_key_file=$tls_private_key_source"
  )
fi

exec gosu postgres postgres "${postgres_args[@]}"
