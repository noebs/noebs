"""Stage and transfer the existing authenticated Mojaloop peer between hosts."""
import hashlib
import io
import ipaddress
import json
import re
import shlex
import tarfile

from reconcile import ROOT, run, ssh, ssh_args

UNITS = ['noebs-mojaloop-relay.service', 'noebs-mojaloop-firewall.service',
         'wg-quick@noebsml.service', 'noebs-mojaloop-tunnel.service']
CREDENTIALS = {'wireguard_config': '/etc/wireguard/noebsml.conf',
               'exe_headers': '/etc/noebs-mojaloop/exe-headers',
               'callback_relay_token': '/etc/noebs-mojaloop/callback-relay-token'}


class Host:
    def __init__(self, command):
        self.command = command

    def run(self, command, payload=None):
        return run(self.command + [command], input=payload, capture_output=True).stdout


def unit_states(host):
    states = {}
    for unit in UNITS:
        states[unit] = host.run('sudo systemctl show ' + unit + ' -p ActiveState --value').decode().strip()
    return states


def credential_hash(host):
    payload = host.run('sudo sha256sum ' + shlex.join(CREDENTIALS.values()))
    return hashlib.sha256(payload).hexdigest()


def peer_ready(host):
    command = '''set -eu
for attempt in $(seq 1 10); do
  if ping -c 1 -W 2 172.30.250.1 >/dev/null 2>&1; then
    code=$(curl --silent --show-error --max-time 10 -o /dev/null -w '%{http_code}' http://172.30.250.1:4040/)
    test "$code" = 403
    exit 0
  fi
  sleep 2
done
exit 1
'''
    host.run('bash -c ' + shlex.quote(command))


def callback_ready(worker):
    unit = worker.run('sudo cat /etc/systemd/system/noebs-mojaloop-relay.service').decode()
    match = re.search(r'--als ([0-9.]+):4000 ', unit)
    if not match:
        raise ValueError('Native callback unit has no explicit SDK IPv4 endpoint')
    address = str(ipaddress.IPv4Address(match.group(1)))
    # The pinned SDK returns 404 on this read-only unregistered path.
    code = worker.run('curl --silent --show-error --max-time 10 -o /dev/null -w "%{http_code}" http://' + address + ':4000/health').decode()
    if code != '404':
        raise ValueError('Worker host cannot reach the expected SDK callback listener')


def verify_native_relay(source, worker, *, check_callback=True):
    if any(state != 'active' for state in unit_states(source).values()):
        raise ValueError('Source native transport is not fully active')
    if any(state == 'active' for state in unit_states(worker).values()):
        raise ValueError('Worker transport must remain stopped before peer handoff')
    worker.run('sudo test -s /etc/noebs-mojaloop/staged.sha256 && sudo /usr/local/bin/wstunnel --version >/dev/null && sudo wg --version >/dev/null')
    if credential_hash(source) != credential_hash(worker):
        raise ValueError('Worker native peer credentials differ from the source')
    peer_ready(source)
    if check_callback:
        callback_ready(worker)


def verify_native_handoff(source, worker):
    if any(state != 'inactive' for state in unit_states(source).values()):
        raise ValueError('Source native peer has not fully stopped')
    source.run('! ip link show dev noebsml >/dev/null 2>&1')
    enabled = source.run('sudo systemctl show ' + shlex.join(UNITS) + ' -p UnitFileState --value').decode().splitlines()
    if len(enabled) != len(UNITS) or any(state != 'disabled' for state in enabled):
        raise ValueError('Source native peer must remain disabled across restart')
    if any(state != 'active' for state in unit_states(worker).values()):
        raise ValueError('Worker native transport is not fully active')
    if credential_hash(source) != credential_hash(worker):
        raise ValueError('Native peer identity changed during handoff')
    peer_ready(worker)


def switch_native_relay(source, worker):
    marker = json.loads(source.run('sudo k3s kubectl -n noebs get configmap noebs-migration -o json'))['data']
    deployments = json.loads(source.run('sudo k3s kubectl -n noebs get deployments -o json'))['items']
    if marker.get('state') != 'source-fenced' or any(item['spec'].get('replicas', 1) != 0 for item in deployments):
        raise ValueError('Native peer transfer requires fenced source writers')
    if (all(state == 'inactive' for state in unit_states(source).values())
            and all(state == 'active' for state in unit_states(worker).values())):
        verify_native_handoff(source, worker)
        return
    verify_native_relay(source, worker, check_callback=False)
    source.run('sudo systemctl disable --now ' + shlex.join(UNITS))
    if any(state != 'inactive' for state in unit_states(source).values()):
        raise ValueError('Source native peer did not stop')
    source.run('! ip link show dev noebsml >/dev/null 2>&1')
    # There is deliberately no automatic failback after the identity moves.
    order = list(reversed(UNITS))
    worker.run('sudo systemctl enable ' + shlex.join(order) + ' && sudo systemctl start ' + shlex.join(order))
    verify_native_handoff(source, worker)


def stage_native_relay(key, worker, sdk_address):
    data = json.loads(run(['sops', '--decrypt', '--output-type', 'json',
                          str(ROOT / 'deploy/exe/native.secrets.yaml')], capture_output=True).stdout)
    if set(data) != set(CREDENTIALS) or any(not isinstance(value, str) or not value for value in data.values()):
        raise ValueError('Native transport credentials are incomplete')
    files = {path.lstrip('/'): data[name].encode() for name, path in CREDENTIALS.items()}
    files.update({
        'usr/local/lib/noebs-mojaloop-relay.py': (ROOT / 'deploy/exe/native/relay.py').read_bytes(),
        'usr/local/sbin/noebs-mojaloop-firewall': (ROOT / 'deploy/exe/native/firewall.sh').read_bytes(),
        'etc/systemd/system/noebs-mojaloop-tunnel.service': (ROOT / 'deploy/exe/native/tunnel.service').read_bytes(),
        'etc/systemd/system/noebs-mojaloop-firewall.service': (ROOT / 'deploy/exe/native/firewall.service').read_bytes(),
    })
    relay = '''[Unit]
Description=noebs authenticated Mojaloop callback relay
After=network-online.target wg-quick@noebsml.service
Requires=wg-quick@noebsml.service
[Service]
DynamicUser=yes
LoadCredential=relay-token:/etc/noebs-mojaloop/callback-relay-token
ExecStart=/usr/bin/python3 /usr/local/lib/noebs-mojaloop-relay.py --listen 172.30.250.7 --direction sdk --als ADDRESS --quotes ADDRESS --transfers ADDRESS --accept-auth-file %d/relay-token
Restart=always
RestartSec=3
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
[Install]
WantedBy=multi-user.target
'''.replace('ADDRESS', sdk_address)
    files['etc/systemd/system/noebs-mojaloop-relay.service'] = relay.encode()
    fingerprint = hashlib.sha256(b''.join(name.encode() + value for name, value in sorted(files.items()))).hexdigest()
    files['etc/noebs-mojaloop/staged.sha256'] = (fingerprint + '\n').encode()
    archive = io.BytesIO()
    with tarfile.open(fileobj=archive, mode='w:gz') as bundle:
        for name, payload in files.items():
            entry = tarfile.TarInfo(name)
            entry.size = len(payload)
            entry.mode = 0o600 if name.startswith(('etc/wireguard/', 'etc/noebs-mojaloop/')) else 0o755 if name.startswith('usr/') else 0o644
            bundle.addfile(entry, io.BytesIO(payload))
    install = '''set -eu
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq wireguard-tools=1.0.20210914-1ubuntu4
if ! test -x /usr/local/bin/wstunnel || ! /usr/local/bin/wstunnel --version | grep -qx 'wstunnel-cli 10.7.1'; then
  work=$(mktemp -d)
  trap 'rm -rf "$work"' EXIT
  curl -fsSL https://github.com/erebe/wstunnel/releases/download/v10.7.1/wstunnel_10.7.1_linux_amd64.tar.gz -o "$work/wstunnel.tar.gz"
  printf 'fa842ed53fbb14b1c69cd98829f9895d7f8a6b0d562c57c1175851a52cea9ea2  %s\\n' "$work/wstunnel.tar.gz" | sha256sum -c - >/dev/null
  tar xzf "$work/wstunnel.tar.gz" -C "$work"
  install -m 0755 "$work/wstunnel" /usr/local/bin/wstunnel
fi
install -d -m 0700 /etc/noebs-mojaloop /etc/wireguard
'''
    ssh(key, worker, 'sudo bash -c ' + shlex.quote(install))
    changed = ssh(key, worker, 'sudo cat /etc/noebs-mojaloop/staged.sha256 2>/dev/null || test ! -f /etc/noebs-mojaloop/staged.sha256', capture_output=True).stdout.strip() != fingerprint.encode()
    active = unit_states(Host(ssh_args(key, worker)))['wg-quick@noebsml.service'] == 'active'
    if changed:
        ssh(key, worker, 'sudo tar xz -C / && sudo systemctl daemon-reload', input=archive.getvalue())
        if active:
            ssh(key, worker, 'sudo systemctl restart ' + shlex.join(list(reversed(UNITS))))
            peer_ready(Host(ssh_args(key, worker)))
