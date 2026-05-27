#!/usr/bin/env bash
set -euo pipefail

password_source="/opt/temporal/secrets/postgres-password"
postgres_host="temporal-postgres"
postgres_port="5432"
postgres_user="temporal"
temporal_db="temporal"
visibility_db="temporal_visibility"
schema_root="/etc/temporal/schema/postgresql/v12"

if [ ! -s "$password_source" ]; then
  echo "missing Temporal Postgres password file: $password_source" >&2
  exit 1
fi

password="$(tr -d '\r\n' < "$password_source")"

until nc -z "$postgres_host" "$postgres_port"; do
  echo "waiting for Temporal Postgres at $postgres_host:$postgres_port"
  sleep 1
done

temporal-sql-tool \
  --plugin postgres12 \
  --endpoint "$postgres_host" \
  --port "$postgres_port" \
  --user "$postgres_user" \
  --password "$password" \
  --database "$temporal_db" \
  setup-schema -v 0.0
temporal-sql-tool \
  --plugin postgres12 \
  --endpoint "$postgres_host" \
  --port "$postgres_port" \
  --user "$postgres_user" \
  --password "$password" \
  --database "$temporal_db" \
  update-schema -d "$schema_root/temporal/versioned"

temporal-sql-tool \
  --plugin postgres12 \
  --endpoint "$postgres_host" \
  --port "$postgres_port" \
  --user "$postgres_user" \
  --password "$password" \
  --database "$visibility_db" \
  setup-schema -v 0.0
temporal-sql-tool \
  --plugin postgres12 \
  --endpoint "$postgres_host" \
  --port "$postgres_port" \
  --user "$postgres_user" \
  --password "$password" \
  --database "$visibility_db" \
  update-schema -d "$schema_root/visibility/versioned"
