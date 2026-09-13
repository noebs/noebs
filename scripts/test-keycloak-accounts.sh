#!/usr/bin/env bash
# Run native account and Google-broker integration tests against an isolated
# Keycloak 26.7.0. Requires Java 21, Go, Python 3, curl, tar, and OpenSSL.
# Optional: NOEBS_KEYCLOAK_ARCHIVE=/path/to/keycloak-26.7.0.tar.gz avoids download.
# Optional: NOEBS_KEEP_KEYCLOAK_FIXTURE=1 preserves logs and data for inspection.
set -euo pipefail

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"
fixture_java=${JAVA_HOME:+$JAVA_HOME/bin/}java
for dependency in "$fixture_java" go python3 curl tar openssl; do
    command -v "$dependency" >/dev/null || { printf 'Missing dependency: %s\n' "$dependency" >&2; exit 1; }
done

fixture_dir=$(mktemp -d "${TMPDIR:-/tmp}/noebs-keycloak-accounts.XXXXXXXX")
fixture_pids=()
cleanup() {
    fixture_status=$?
    trap - EXIT
    if (( ${#fixture_pids[@]} )); then
        kill "${fixture_pids[@]}" 2>/dev/null || true
        wait "${fixture_pids[@]}" 2>/dev/null || true
    fi
    if (( fixture_status != 0 )); then
        for fixture_log in "$fixture_dir"/*.log; do
            if [[ -f "$fixture_log" ]]; then
                printf '\n%s\n' "$fixture_log" >&2
                tail -n 60 "$fixture_log" >&2
            fi
        done
    fi
    if [[ ${NOEBS_KEEP_KEYCLOAK_FIXTURE:-0} == 1 ]]; then
        printf 'Fixture retained at %s\n' "$fixture_dir"
    else
        rm -rf -- "$fixture_dir"
    fi
    exit "$fixture_status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

fixture_archive=${NOEBS_KEYCLOAK_ARCHIVE:-$fixture_dir/keycloak-26.7.0.tar.gz}
if [[ -z ${NOEBS_KEYCLOAK_ARCHIVE:-} ]]; then
    printf 'Downloading Keycloak 26.7.0...\n'
    curl --fail --location --retry 3 --max-time 300 \
        https://github.com/keycloak/keycloak/releases/download/26.7.0/keycloak-26.7.0.tar.gz \
        --output "$fixture_archive"
fi
# Digest published on the official GitHub release asset for version 26.7.0.
python3 - "$fixture_archive" <<'PY'
import hashlib
import sys
digest = hashlib.sha256()
with open(sys.argv[1], 'rb') as archive:
    for chunk in iter(lambda: archive.read(1024 * 1024), b''):
        digest.update(chunk)
if digest.hexdigest() != 'f771df0aa1e4820f57d56f7d6d015beb6415487b43f8de7e5a6d48f8a7fe118a':
    sys.exit('Keycloak archive checksum does not match the pinned 26.7.0 release')
PY
tar -xzf "$fixture_archive" -C "$fixture_dir"
fixture_kc="$fixture_dir/keycloak-26.7.0/bin/kc.sh"
fixture_db_url="jdbc:h2:file:$fixture_dir/keycloakdb;NON_KEYWORDS=VALUE"

read -r fixture_kc_port fixture_google_port fixture_proxy_port < <(python3 - <<'PY'
import socket
sockets = [socket.socket() for _ in range(3)]
for sock in sockets:
    sock.bind(('127.0.0.1', 0))
print(*(sock.getsockname()[1] for sock in sockets))
PY
)
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=localhost \
    -addext 'basicConstraints=critical,CA:TRUE' \
    -addext 'subjectAltName=DNS:localhost,IP:127.0.0.1,DNS:accounts.google.com,DNS:oauth2.googleapis.com,DNS:www.googleapis.com,DNS:openidconnect.googleapis.com' \
    -keyout "$fixture_dir/tls.key" -out "$fixture_dir/ca.pem" >"$fixture_dir/certificate.log" 2>&1

go build -o "$fixture_dir/googlemock" ./internal/keycloakadmin/testdata/googlemock
"$fixture_dir/googlemock" --listen "127.0.0.1:$fixture_google_port" \
    --certificate "$fixture_dir/ca.pem" --private-key "$fixture_dir/tls.key" >"$fixture_dir/googlemock.log" 2>&1 &
fixture_pids+=("$!")

# Only the mock receives Google HTTPS requests. This CONNECT proxy does not
# resolve public hosts and rejects destinations outside the four fixture hosts.
python3 -u - "$fixture_proxy_port" "$fixture_google_port" >"$fixture_dir/proxy.log" 2>&1 <<'PY' &
import http.server
import select
import socket
import sys

class GoogleProxy(http.server.BaseHTTPRequestHandler):
    def do_CONNECT(self):
        if self.path not in {'accounts.google.com:443', 'oauth2.googleapis.com:443',
                             'www.googleapis.com:443', 'openidconnect.googleapis.com:443'}:
            self.send_error(403)
            return
        with socket.create_connection(('127.0.0.1', int(sys.argv[2])), timeout=10) as upstream:
            self.send_response(200)
            self.end_headers()
            while True:
                ready, _, _ = select.select([self.connection, upstream], [], [], 30)
                if not ready:
                    return
                for source in ready:
                    data = source.recv(65536)
                    if not data:
                        return
                    destination = upstream if source is self.connection else self.connection
                    destination.sendall(data)

    def log_message(self, *_):
        pass

http.server.ThreadingHTTPServer(('127.0.0.1', int(sys.argv[1])), GoogleProxy).serve_forever()
PY
fixture_pids+=("$!")

export NOEBS_TEST_KEYCLOAK_BOOTSTRAP_SECRET
NOEBS_TEST_KEYCLOAK_BOOTSTRAP_SECRET=$(openssl rand -hex 24)
"$fixture_kc" bootstrap-admin service --db=dev-file --db-url="$fixture_db_url" \
    --client-id=noebs-keycloak-bootstrap \
    --client-secret:env NOEBS_TEST_KEYCLOAK_BOOTSTRAP_SECRET --no-prompt >"$fixture_dir/bootstrap.log" 2>&1
"$fixture_kc" start --http-enabled=false --http-host=127.0.0.1 \
    --https-port="$fixture_kc_port" --https-certificate-file="$fixture_dir/ca.pem" \
    --https-certificate-key-file="$fixture_dir/tls.key" --hostname="https://localhost:$fixture_kc_port" \
    --truststore-paths="$fixture_dir/ca.pem" --db=dev-file --db-url="$fixture_db_url" --cache=local --features=organization \
    "--spi-connections-http-client--default--proxy-mappings=.*google.*;http://127.0.0.1:$fixture_proxy_port" \
    >"$fixture_dir/keycloak.log" 2>&1 &
fixture_pids+=("$!")

printf 'Starting isolated Keycloak at https://localhost:%s...\n' "$fixture_kc_port"
fixture_deadline=$((SECONDS + 120))
fixture_ready=0
while (( SECONDS < fixture_deadline )); do
    for fixture_pid in "${fixture_pids[@]}"; do
        if ! kill -0 "$fixture_pid" 2>/dev/null; then
            printf 'Fixture process exited before startup completed.\n' >&2
            exit 1
        fi
    done
    if curl --silent --fail --noproxy '*' --max-time 2 --cacert "$fixture_dir/ca.pem" \
        "https://localhost:$fixture_kc_port/realms/master/.well-known/openid-configuration" >/dev/null; then
        fixture_ready=1
        break
    fi
    sleep 1
done
if (( fixture_ready == 0 )); then
    printf 'Keycloak did not become ready within 120 seconds.\n' >&2
    exit 1
fi

NOEBS_TEST_KEYCLOAK_URL="https://localhost:$fixture_kc_port" \
NOEBS_TEST_KEYCLOAK_CA="$fixture_dir/ca.pem" \
NOEBS_TEST_GOOGLE_ADDRESS="127.0.0.1:$fixture_google_port" \
    go test -race ./internal/keycloakadmin -run '^TestRealKeycloak26_7' -count=1 -timeout=8m -v
