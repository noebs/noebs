"""Declare and verify a private backoffice origin on the worker's own tailnet."""
import ipaddress
import json
import re
import shlex

PRIVATE_UPSTREAM = 'http://127.0.0.1:8082'


def private_host(origin):
    if not isinstance(origin, str):
        raise ValueError('backoffice_origin requires an explicit HTTPS tailnet origin')
    match = re.fullmatch(r'https://([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.ts\.net)', origin)
    if match is None:
        raise ValueError('backoffice_origin requires a canonical HTTPS machine.ts.net origin without port or path')
    return match[1]


def serve_configuration(origin):
    host = private_host(origin)
    return {'TCP': {'443': {'HTTPS': True}},
            'Web': {host + ':443': {'Handlers': {'/': {'Proxy': PRIVATE_UPSTREAM}}}}}


def validate_worker(status, origin):
    host = private_host(origin)
    if (status.get('BackendState') != 'Running' or status.get('Self', {}).get('DNSName') != host + '.'
            or host not in status.get('CertDomains', []) or not status.get('CurrentTailnet', {}).get('MagicDNSEnabled')):
        raise ValueError('Private backoffice origin is not the running worker HTTPS tailnet identity')


def private_peer_address(status, origin):
    """Resolve the exact private host through the verifier's authenticated netmap.

    Fleet servers deliberately retain their own DNS configuration. Resolve only
    this TLS probe from Tailscale state, without changing their system resolver.
    """
    host = private_host(origin) + '.'
    if status.get('BackendState') != 'Running' or status.get('Self', {}).get('DNSName') == host:
        raise ValueError('Private verification requires a different running tailnet peer')
    peers = status.get('Peer')
    if not isinstance(peers, dict):
        raise ValueError('Private verification requires an authenticated tailnet peer map')
    targets = [peer for peer in peers.values() if isinstance(peer, dict) and peer.get('DNSName') == host]
    if len(targets) != 1 or targets[0].get('Online') is not True:
        raise ValueError('Private backoffice host is not one online authenticated tailnet peer')
    addresses = []
    for raw in targets[0].get('TailscaleIPs', []):
        address = ipaddress.ip_address(raw)
        if address.version == 4:
            if address not in ipaddress.ip_network('100.64.0.0/10'):
                raise ValueError('Private backoffice peer has a non-tailnet IPv4 address')
            addresses.append(str(address))
    if len(addresses) != 1:
        raise ValueError('Private backoffice peer requires one explicit tailnet IPv4 address')
    return addresses[0]


def validate_serve(current, desired, *, require_enabled=False):
    # This deploy owns only this exact HTTPS proxy. Refuse to overwrite other
    # Serve handlers or Funnel exposure; never reset the worker's shared state.
    if not isinstance(current, dict):
        raise ValueError('Worker returned an invalid Tailscale Serve configuration')
    current = dict(current)
    funnel = current.pop('AllowFunnel', {})
    if not isinstance(funnel, dict) or any(funnel.values()):
        raise ValueError('Private backoffice refuses public Funnel exposure')
    if current != desired and (require_enabled or current != {}):
        raise ValueError('Worker has an unexpected Tailscale Serve configuration')
    return current


def check_private_backoffice_target(origin, key, worker, lease, ssh):
    if lease is None:
        raise RuntimeError('Private backoffice configuration requires the release lease')
    lease.check()
    status = json.loads(ssh(key, worker, 'tailscale status --json', capture_output=True).stdout)
    validate_worker(status, origin)
    desired = serve_configuration(origin)
    current = json.loads(ssh(key, worker, 'tailscale serve status --json', capture_output=True).stdout)
    current = validate_serve(current, desired)
    return desired, current


def reconcile_private_backoffice(origin, key, worker, lease, ssh):
    desired, current = check_private_backoffice_target(origin, key, worker, lease, ssh)
    lease.check()
    if current != desired:
        ssh(key, worker, 'sudo tailscale serve --bg --https=443 --yes ' + shlex.quote(PRIVATE_UPSTREAM), capture_output=True)
    lease.check()
    actual = json.loads(ssh(key, worker, 'tailscale serve status --json', capture_output=True).stdout)
    validate_serve(actual, desired, require_enabled=True)
