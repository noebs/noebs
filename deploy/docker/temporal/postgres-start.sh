#!/usr/bin/env bash
set -euo pipefail

pgdata="/var/lib/postgresql/data"
postgres_user="temporal"
postgres_db="temporal"
visibility_db="temporal_visibility"
password_source="/opt/temporal-postgres/secrets/password"
password_runtime="/tmp/temporal-postgres-password"
pgpass="/var/lib/postgresql/.pgpass"
tls_certificate_source="/opt/temporal-postgres/secrets/tls.crt"
tls_private_key_source="/opt/temporal-postgres/secrets/tls.key"
tls_certificate_runtime="/tmp/temporal-postgres-tls.crt"
tls_private_key_runtime="/tmp/temporal-postgres-tls.key"

cleanup() {
  rm -f "$password_runtime" "$pgpass" "$tls_certificate_runtime" "$tls_private_key_runtime"
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
for tls_source in "$tls_certificate_source" "$tls_private_key_source"; do
  if [ ! -s "$tls_source" ]; then
    echo "missing Temporal Postgres TLS input: $tls_source" >&2
    exit 1
  fi
done

install -d -o postgres -g postgres -m 0700 "$pgdata"
install -o postgres -g postgres -m 0600 "$tls_certificate_source" "$tls_certificate_runtime"
install -o postgres -g postgres -m 0600 "$tls_private_key_source" "$tls_private_key_runtime"

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

cat > "$pgdata/pg_hba.conf" <<'EOF'
local all all scram-sha-256
hostssl all all all scram-sha-256
hostnossl all all all reject
host all all all reject
EOF
chown postgres:postgres "$pgdata/pg_hba.conf"
chmod 0600 "$pgdata/pg_hba.conf"

exec gosu postgres postgres \
  -D "$pgdata" \
  -c listen_addresses='*' \
  -c ssl=on \
  -c ssl_min_protocol_version=TLSv1.3 \
  -c ssl_max_protocol_version=TLSv1.3 \
  -c "ssl_cert_file=$tls_certificate_runtime" \
  -c "ssl_key_file=$tls_private_key_runtime"
