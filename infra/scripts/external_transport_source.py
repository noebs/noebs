"""Preserve the authenticated socket peer of one configured tailnet callback."""
import ipaddress
import shlex

CHAIN = 'NOEBS_CALLBACK_SOURCE'
SCRIPT = '/usr/local/sbin/noebs-callback-source'
UNIT = 'noebs-callback-source.service'
NODE_PORT = 30402
TAILNET = ipaddress.IPv4Network('100.64.0.0/10')


def callback_tuple(settings, worker_ip):
    if not settings.get('interop_tenant'):
        return None
    try:
        worker = ipaddress.IPv4Address(worker_ip)
        peers = settings['interop_backend_allowed_peers']
        if not isinstance(peers, list) or len(peers) != 1:
            raise ValueError('exactly one callback peer is required')
        peer = ipaddress.IPv4Address(peers[0])
        address = settings['interop_backend_listen_address']
        if not isinstance(address, str):
            raise ValueError('explicit IPv4 callback listener is required')
        listen = address.split(':')
        if len(listen) != 2 or listen[0] != '0.0.0.0' or not listen[1].isdigit():
            raise ValueError('explicit IPv4 callback listener is required')
        port = int(listen[1])
        if not 1 <= port <= 65535 or str(port) != listen[1]:
            raise ValueError('invalid callback port')
        if worker not in TAILNET or peer not in TAILNET or worker == peer:
            raise ValueError('distinct tailnet IPv4 addresses are required')
        return str(peer), str(worker), port
    except (KeyError, TypeError, ValueError) as error:
        raise ValueError('Invalid callback source-preservation configuration: ' + str(error)) from None


def firewall_script(connection=None):
    # All address/port values originate from callback_tuple, never shell input.
    preflight = rules = ''
    if connection:
        peer, worker, port = connection
        preflight = f'''
    [ "$(tailscale ip -4)" = {shlex.quote(worker)} ] || {{ echo 'Worker tailnet identity changed' >&2; exit 1; }}
    iptables -w 10 -t filter -C ts-forward -i tailscale0 -j MARK --set-xmark 0x40000/0xff0000
    iptables -w 10 -t nat -C ts-postrouting -m mark --mark 0x40000/0xff0000 -j MASQUERADE
'''
        rules = f'''
    iptables -w 10 -t mangle -A {CHAIN} -p tcp -s {peer}/32 --dport {port} \\
      -m mark --mark 0x40000/0xff0000 -m conntrack --ctstate DNAT --ctdir ORIGINAL \\
      --ctorigsrc {peer}/32 --ctorigdst {worker}/32 --ctorigdstport {NODE_PORT} \\
      -j MARK --set-xmark 0x0/0xff0000
    iptables -w 10 -t mangle -A POSTROUTING -p tcp -j {CHAIN}
'''
    return f'''#!/bin/sh
# Managed by Noebs promotion. Never changes a Tailscale or Kubernetes chain.
set -eu
remove() {{
    while iptables -w 10 -t mangle -C POSTROUTING -p tcp -j {CHAIN} 2>/dev/null; do
        iptables -w 10 -t mangle -D POSTROUTING -p tcp -j {CHAIN}
    done
    if iptables -w 10 -t mangle -S {CHAIN} >/dev/null 2>&1; then
        iptables -w 10 -t mangle -F {CHAIN}
        iptables -w 10 -t mangle -X {CHAIN}
    fi
}}
verify() {{
{preflight}    :
}}
case "${{1:-}}" in
verify) verify ;;
remove) remove ;;
apply)
    verify
    remove
    iptables -w 10 -t mangle -N {CHAIN}
{rules}    ;;
*) echo 'Expected verify, apply or remove' >&2; exit 2 ;;
esac
'''


def systemd_unit():
    return f'''[Unit]
Description=Preserve the configured Noebs callback socket peer
Wants=network-online.target
After=network-online.target tailscaled.service k3s.service k3s-agent.service
Requires=tailscaled.service
PartOf=tailscaled.service
StartLimitIntervalSec=120
StartLimitBurst=12

[Service]
Type=oneshot
RemainAfterExit=yes
Restart=on-failure
RestartSec=5
ExecStart={SCRIPT} apply
ExecStop={SCRIPT} remove

[Install]
WantedBy=multi-user.target
'''


def install_script(connection):
    if not connection:
        return f'''set -eu
if [ -f /etc/systemd/system/{UNIT} ]; then
    systemctl disable --now {UNIT}
fi
sh -s remove <<'NOEBS_FIREWALL'
{firewall_script()}NOEBS_FIREWALL
rm -f {SCRIPT} /etc/systemd/system/{UNIT}
systemctl daemon-reload
'''
    return f'''set -eu
sh -s verify <<'NOEBS_VERIFY'
{firewall_script(connection)}NOEBS_VERIFY
install -d -m 0755 /usr/local/sbin
cat > {SCRIPT}.new <<'NOEBS_FIREWALL'
{firewall_script(connection)}NOEBS_FIREWALL
chmod 0755 {SCRIPT}.new
mv {SCRIPT}.new {SCRIPT}
cat > /etc/systemd/system/{UNIT}.new <<'NOEBS_UNIT'
{systemd_unit()}NOEBS_UNIT
chmod 0644 /etc/systemd/system/{UNIT}.new
mv /etc/systemd/system/{UNIT}.new /etc/systemd/system/{UNIT}
systemctl daemon-reload
systemctl enable {UNIT}
systemctl restart {UNIT}
systemctl is-active --quiet {UNIT}
'''


def reconcile_callback_source(settings, key, worker, lease, ssh):
    """Called only after runtime validation and while holding the promote lease."""
    if lease is None:
        raise RuntimeError('Callback source reconciliation requires the release lease')
    lease.check()
    worker_ip = None
    if settings.get('interop_tenant'):
        worker_ip = ssh(key, worker, 'tailscale ip -4', capture_output=True).stdout.decode().strip()
    connection = callback_tuple(settings, worker_ip)
    lease.check()
    ssh(key, worker, 'sudo sh -s', input=install_script(connection).encode(), capture_output=True)
    lease.check()
