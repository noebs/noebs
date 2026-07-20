#!/usr/bin/env bash
set -euo pipefail

pgdata="/var/lib/postgresql/data"
authority_marker="$pgdata/.noebs-postgres-authority"
tls_certificate_source="/opt/noebs-postgres/secrets/tls.crt"
tls_private_key_source="/opt/noebs-postgres/secrets/tls.key"
tls_ca_source="/opt/noebs-postgres/secrets/ca.pem"
tls_certificate_runtime="/tmp/noebs-postgres-tls.crt"
tls_private_key_runtime="/tmp/noebs-postgres-tls.key"
role_passwords_source="/run/secrets/service-role-passwords"
service_database_sql="/opt/noebs-postgres/init/001-service-databases.sql"

database_role_bindings=(
  "admin_reporting admin_reporting_migrate"
  "admin_reporting admin_reporting_projector"
  "admin_reporting admin_reporting_runtime"
  "card_vault card_vault_migrate"
  "card_vault card_vault_runtime"
  "ebs_adapter ebs_adapter_events"
  "ebs_adapter ebs_adapter_migrate"
  "ebs_adapter ebs_adapter_runtime"
  "gateway_auth gateway_auth_cleanup"
  "gateway_auth gateway_auth_migrate"
  "gateway_auth gateway_auth_runtime"
  "identity_auth identity_auth_migrate"
  "identity_auth identity_auth_runtime"
  "notification_chat notification_chat_migrate"
  "notification_chat notification_chat_runtime"
  "wallet_ledger wallet_ledger_migrate"
  "wallet_ledger wallet_ledger_runtime"
  "wallet_ledger wallet_ledger_worker"
  "wallet_ledger wallet_ledger_webhook"
  "workload_auth workload_auth_cleanup"
  "workload_auth workload_auth_migrate"
  "workload_auth workload_auth_runtime"
)
database_roles=()

cleanup() {
  rm -f "$tls_certificate_runtime" "$tls_private_key_runtime"
}
trap cleanup EXIT

if [ ! -s "$role_passwords_source" ]; then
  echo "missing Postgres role password catalog: $role_passwords_source" >&2
  exit 1
fi
if [ ! -s "$service_database_sql" ]; then
  echo "missing Noebs service database SQL file: $service_database_sql" >&2
  exit 1
fi
for tls_source in "$tls_ca_source" "$tls_certificate_source" "$tls_private_key_source"; do
  if [ ! -s "$tls_source" ]; then
    echo "missing Noebs Postgres TLS input: $tls_source" >&2
    exit 1
  fi
done

declare -A expected_roles=()
declare -A configured_roles=()
declare -A configured_passwords=()
for binding in "${database_role_bindings[@]}"; do
  read -r database role <<< "$binding"
  database_roles+=("$role")
  expected_roles["$role"]=1
done
while IFS= read -r record; do
  if [[ "$record" != *=* ]]; then
    echo "invalid Postgres role password record" >&2
    exit 1
  fi
  role="${record%%=*}"
  password="${record#*=}"
  if [ -z "${expected_roles[$role]:-}" ] || [ -n "${configured_roles[$role]:-}" ]; then
    echo "unexpected or repeated Postgres role password: $role" >&2
    exit 1
  fi
  if [ "${#password}" -ne 43 ] || [[ "$password" == *[!A-Za-z0-9_-]* ]] || [[ "${password: -1}" != [AEIMQUYcgkosw048] ]]; then
    echo "invalid Postgres role password: $role" >&2
    exit 1
  fi
  if [ -n "${configured_passwords[$password]:-}" ]; then
    echo "Postgres role passwords must be globally distinct" >&2
    exit 1
  fi
  variable="NOEBS_${role^^}_PASSWORD"
  export "$variable=$password"
  configured_roles["$role"]=1
  configured_passwords["$password"]=1
done < "$role_passwords_source"
for role in "${database_roles[@]}"; do
  if [ -z "${configured_roles[$role]:-}" ]; then
    echo "missing Postgres role password: $role" >&2
    exit 1
  fi
done

install -d -o postgres -g postgres -m 0700 "$pgdata"
install -o postgres -g postgres -m 0600 "$tls_certificate_source" "$tls_certificate_runtime"
install -o postgres -g postgres -m 0600 "$tls_private_key_source" "$tls_private_key_runtime"

if [ ! -s "$pgdata/PG_VERSION" ]; then
  gosu postgres initdb \
    -D "$pgdata" \
    --auth-host=scram-sha-256 \
    --auth-local=peer \
    --username=postgres
  install -o postgres -g postgres -m 0600 /dev/null "$authority_marker"
elif [ ! -f "$authority_marker" ]; then
  echo "existing Postgres data lacks the Noebs authority marker; recreate the data volume" >&2
  exit 1
fi

install -o postgres -g postgres -m 0600 /dev/null "$pgdata/pg_hba.conf"
{
  echo "local all all peer"
  echo "host all postgres all reject"
  for binding in "${database_role_bindings[@]}"; do
    read -r database role <<< "$binding"
    echo "hostssl $database $role all scram-sha-256"
  done
  echo "hostnossl all all all reject"
  echo "host all all all reject"
} > "$pgdata/pg_hba.conf"

gosu postgres pg_ctl \
  -D "$pgdata" \
  -o "-c listen_addresses='' -c unix_socket_directories=/tmp -c hba_file=$pgdata/pg_hba.conf -c password_encryption=scram-sha-256" \
  -w start
if ! gosu postgres psql \
  "host=/tmp dbname=postgres user=postgres" \
  --set=ON_ERROR_STOP=1 \
  --command='SELECT 1' >/dev/null; then
  gosu postgres pg_ctl -D "$pgdata" -m immediate -w stop
  echo "existing Postgres data does not have the passwordless local postgres authority; recreate the retired data volume" >&2
  exit 1
fi
unexpected_authority="$(gosu postgres psql \
  "host=/tmp dbname=postgres user=postgres" \
  --set=ON_ERROR_STOP=1 \
  --tuples-only \
  --no-align \
  --command="
    SELECT COALESCE(string_agg(authority_kind || ':' || authority_name, ',' ORDER BY authority_kind, authority_name), '')
    FROM (
      SELECT 'role' AS authority_kind, rolname AS authority_name
      FROM pg_roles
      WHERE rolname <> 'postgres'
        AND rolname !~ '^pg_'
        AND rolname NOT IN (
          'admin_reporting_migrate', 'admin_reporting_projector', 'admin_reporting_runtime',
          'card_vault_migrate', 'card_vault_runtime',
          'ebs_adapter_events', 'ebs_adapter_migrate', 'ebs_adapter_runtime',
          'gateway_auth_cleanup', 'gateway_auth_migrate', 'gateway_auth_runtime',
          'identity_auth_migrate', 'identity_auth_runtime',
          'notification_chat_migrate', 'notification_chat_runtime',
          'wallet_ledger_migrate', 'wallet_ledger_runtime', 'wallet_ledger_worker', 'wallet_ledger_webhook',
          'workload_auth_cleanup', 'workload_auth_migrate', 'workload_auth_runtime'
        )
      UNION ALL
      SELECT 'database', datname
      FROM pg_database
      WHERE datname NOT IN (
        'postgres', 'template0', 'template1',
        'admin_reporting', 'card_vault', 'ebs_adapter', 'gateway_auth',
        'identity_auth', 'notification_chat', 'wallet_ledger', 'workload_auth'
      )
    ) unexpected
  ")"
if [ -n "$unexpected_authority" ]; then
  gosu postgres pg_ctl -D "$pgdata" -m immediate -w stop
  echo "existing Postgres data contains unexpected authority ($unexpected_authority); recreate the retired data volume" >&2
  exit 1
fi
gosu postgres psql \
  "host=/tmp dbname=postgres user=postgres" \
  --set=ON_ERROR_STOP=1 \
  --file="$service_database_sql"
gosu postgres pg_ctl -D "$pgdata" -m fast -w stop

for role in "${database_roles[@]}"; do
  variable="NOEBS_${role^^}_PASSWORD"
  unset "$variable"
done
unset binding database password record role unexpected_authority variable

exec gosu postgres postgres \
  -D "$pgdata" \
  -c "listen_addresses=*" \
  -c "hba_file=$pgdata/pg_hba.conf" \
  -c "password_encryption=scram-sha-256" \
  -c "ssl=on" \
  -c "ssl_min_protocol_version=TLSv1.3" \
  -c "ssl_max_protocol_version=TLSv1.3" \
  -c "ssl_cert_file=$tls_certificate_runtime" \
  -c "ssl_key_file=$tls_private_key_runtime"
