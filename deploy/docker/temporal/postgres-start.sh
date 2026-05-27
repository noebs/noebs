#!/usr/bin/env bash
set -euo pipefail

pgdata="/var/lib/postgresql/data"
postgres_user="temporal"
postgres_db="temporal"
visibility_db="temporal_visibility"
password_source="/opt/temporal-postgres/secrets/password"
password_runtime="/tmp/temporal-postgres-password"
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
  echo "missing Temporal Postgres password file: $password_source" >&2
  exit 1
fi

install -d -o postgres -g postgres -m 0700 "$pgdata"

if [ ! -s "$pgdata/PG_VERSION" ]; then
  password="$(tr -d '\r\n' < "$password_source")"
  printf '%s\n' "$password" > "$password_runtime"
  chown postgres:postgres "$password_runtime"
  chmod 0600 "$password_runtime"

  gosu postgres initdb \
    -D "$pgdata" \
    --auth-host=scram-sha-256 \
    --auth-local=scram-sha-256 \
    --username="$postgres_user" \
    --pwfile="$password_runtime"

  {
    echo
    echo "host all all all scram-sha-256"
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
    --command="CREATE DATABASE $postgres_db"
  gosu postgres psql \
    "host=/tmp dbname=postgres user=$postgres_user passfile=$pgpass" \
    --set=ON_ERROR_STOP=1 \
    --command="CREATE DATABASE $visibility_db"
  gosu postgres pg_ctl -D "$pgdata" -m fast -w stop
fi

exec gosu postgres postgres -D "$pgdata" -c listen_addresses='*'
