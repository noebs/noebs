#!/usr/bin/env bash
set -euo pipefail
# Native isolated PostgreSQL: no Docker and no existing application databases.
# PG_BIN must name a directory containing PostgreSQL 18 initdb/pg_ctl binaries.
: "${PG_BIN:?set PG_BIN to the PostgreSQL 18 bin directory}"
if [[ -n "${PG_LIBDIR:-}" ]]; then export LD_LIBRARY_PATH="$PG_LIBDIR${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"; fi
fixture_dir=$(mktemp -d "${TMPDIR:-/tmp}/noebs-tenant-access.XXXXXXXX")
chmod 700 "$fixture_dir"
cleanup() {
 "$PG_BIN/pg_ctl" -D "$fixture_dir/data" -m immediate -w stop > /dev/null 2>&1 || true
 rm -rf -- "$fixture_dir"
}
trap cleanup EXIT
mkdir -m 700 "$fixture_dir/socket"
"$PG_BIN/initdb" -D "$fixture_dir/data" --auth=trust --no-locale -U access_fixture > "$fixture_dir/init.log"
fixture_port=$(python3 - <<'PY'
import socket
with socket.socket() as s:
    s.bind(('127.0.0.1',0))
    print(s.getsockname()[1])
PY
)
"$PG_BIN/pg_ctl" -D "$fixture_dir/data" -l "$fixture_dir/postgres.log" -o "-p $fixture_port -h 127.0.0.1 -k $fixture_dir/socket" -w start > /dev/null
TEST_TENANT_ACCESS_POSTGRES_URL="postgres://access_fixture@127.0.0.1:$fixture_port/postgres?sslmode=disable" go test ./internal/tenantaccess -race -count=1 -v
