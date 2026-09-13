#!/usr/bin/env python3
"""Reconcile mail HTTPS routes in an existing Caddy ConfigMap and running server."""
import argparse
import copy
import json
from pathlib import Path
import re
import shlex
import subprocess
import sys
from urllib.error import HTTPError, URLError
from urllib.request import ProxyHandler, Request, build_opener


class MailProxyError(RuntimeError):
    pass


CONFIG_PATH = '/etc/caddy/migration.json'
ADMIN_URL = 'http://127.0.0.1:2019/config/'
TARGET_FIELDS = {'namespace', 'deployment', 'container', 'configmap', 'config_key', 'server', 'routes'}
DNS_NAME = re.compile(r'(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}')


def validate_target(target):
    if not isinstance(target, dict) or set(target) != TARGET_FIELDS:
        raise MailProxyError('Mail proxy requires every explicit Kubernetes target setting')
    for field in TARGET_FIELDS - {'routes', 'config_key'}:
        if not isinstance(target[field], str) or not re.fullmatch(r'[a-z0-9][a-z0-9.-]{0,252}', target[field]):
            raise MailProxyError('Mail proxy target identifiers must be explicit Kubernetes names')
    if not isinstance(target['config_key'], str) or not re.fullmatch(r'[A-Za-z0-9_.-]{1,253}', target['config_key']):
        raise MailProxyError('Mail proxy requires an explicit ConfigMap data key')
    routes = target['routes']
    if not isinstance(routes, list) or not routes:
        raise MailProxyError('Mail proxy requires explicit HTTPS routes')
    identifiers = set()
    hostnames = set()
    for route in routes:
        if not isinstance(route, dict) or set(route) != {'id', 'hostname', 'upstream'}:
            raise MailProxyError('Each mail HTTPS route requires an explicit id, hostname and upstream')
        if not isinstance(route['id'], str) or not re.fullmatch(r'[a-z][a-z0-9-]{2,63}', route['id']):
            raise MailProxyError('Mail proxy route ID must be a stable explicit identifier')
        if not isinstance(route['hostname'], str) or not DNS_NAME.fullmatch(route['hostname']):
            raise MailProxyError('Mail proxy hostname must be a canonical DNS name')
        upstream = re.fullmatch(r'127\.0\.0\.1:([1-9][0-9]{0,4})', route['upstream']) if isinstance(route['upstream'], str) else None
        if upstream is None or int(upstream[1]) > 65535:
            raise MailProxyError('Mail HTTPS upstream must be an explicit loopback IPv4 address and TCP port')
        if route['id'] in identifiers or route['hostname'] in hostnames:
            raise MailProxyError('Mail proxy route IDs and hostnames must be unique')
        identifiers.add(route['id'])
        hostnames.add(route['hostname'])


def proxy_route(route):
    # Stalwart uses forwarded headers on its loopback-only HTTP listener.
    # Never forward a client's supplied address or scheme as trusted metadata.
    return {
        '@id': route['id'],
        'match': [{'host': [route['hostname']]}],
        'handle': [{
            'handler': 'reverse_proxy', 'upstreams': [{'dial': route['upstream']}],
            'headers': {'request': {
                'delete': ['Forwarded', 'X-Real-IP'],
                'set': {
                    'Host': [route['hostname']], 'X-Forwarded-Host': [route['hostname']],
                    'X-Forwarded-Proto': ['https'],
                    'X-Forwarded-For': ['{http.request.remote.host}'],
                },
            }},
        }],
        'terminal': True,
    }


def walk_objects(value):
    if isinstance(value, dict):
        yield value
        for child in value.values():
            yield from walk_objects(child)
    elif isinstance(value, list):
        for child in value:
            yield from walk_objects(child)


def host_matches(pattern, hostname):
    # Caddy host wildcards occupy one DNS label. Treat a global wildcard as a
    # collision too, rather than silently taking over an existing catch-all.
    if pattern == '*':
        return True
    return len(pattern.split('.')) == len(hostname.split('.')) and all(
        left == '*' or left.lower() == right for left, right in zip(pattern.split('.'), hostname.split('.')))


def desired_config(current, target):
    validate_target(target)
    if not isinstance(current, dict):
        raise MailProxyError('Caddy configuration must be a JSON object')
    try:
        servers = current['apps']['http']['servers']
        server = servers[target['server']]
        routes = server['routes']
        if (not isinstance(servers, dict) or not isinstance(server, dict) or not isinstance(routes, list)
                or server['listen'] != [':443']
                or server.get('automatic_https', {}).get('disable')
                or server.get('automatic_https', {}).get('disable_certificates')
                or any(route['hostname'] in server.get('automatic_https', {}).get('skip', [])
                       or route['hostname'] in server.get('automatic_https', {}).get('skip_certificates', [])
                       for route in target['routes'])):
            raise MailProxyError('Mail proxy requires the existing automatic HTTPS server on :443')
    except (KeyError, TypeError, AttributeError):
        raise MailProxyError('Mail proxy requires the explicit existing Caddy HTTPS server and routes') from None
    owned = {}
    for route in target['routes']:
        occurrences = [node for node in walk_objects(current) if node.get('@id') == route['id']]
        if len(occurrences) > 1 or (occurrences and not any(node is occurrences[0] for node in routes)):
            raise MailProxyError('Managed mail route ID collides with existing Caddy configuration')
        if occurrences:
            if occurrences[0].get('match') != [{'host': [route['hostname']]}]:
                raise MailProxyError('Managed mail route ID belongs to a different hostname')
            owned[route['id']] = occurrences[0]
    for name, other in servers.items():
        if not isinstance(other, dict) or not isinstance(other.get('routes', []), list):
            raise MailProxyError('Existing Caddy server routes are invalid')
        for route in other.get('routes', []):
            if not isinstance(route, dict):
                raise MailProxyError('Existing Caddy route is invalid')
            if any(route is node for node in owned.values()):
                continue
            matches = route.get('match')
            if not isinstance(matches, list) or not matches:
                raise MailProxyError('Existing unscoped Caddy route could intercept the mail hostname')
            for match in matches:
                hosts = match.get('host') if isinstance(match, dict) else None
                if not isinstance(hosts, list) or not hosts or any(not isinstance(host, str) for host in hosts):
                    raise MailProxyError('Existing unscoped Caddy route could intercept the mail hostname')
                if any(host_matches(host, spec['hostname']) for host in hosts for spec in target['routes']):
                    raise MailProxyError('Mail hostname collides with an existing unmanaged Caddy route')
    desired = copy.deepcopy(current)
    output_routes = desired['apps']['http']['servers'][target['server']]['routes']
    for spec in target['routes']:
        if spec['id'] in owned:
            index = next(index for index, route in enumerate(routes) if route is owned[spec['id']])
            output_routes[index] = proxy_route(spec)
        else:
            output_routes.append(proxy_route(spec))
    return desired


def routes_from_config(config, compose):
    # Read the actual runtime's published HTTP listener, rather than inventing
    # a second configuration authority for the same service endpoint.
    bindings = [binding for binding in compose['services']['stalwart']['ports']
                if isinstance(binding, str) and binding.endswith(':8080')]
    if len(bindings) != 1 or not re.fullmatch(r'127\.0\.0\.1:[1-9][0-9]{0,4}:8080', bindings[0]):
        raise MailProxyError('Stalwart must publish exactly one loopback HTTP listener')
    routes = [{'id': 'noebs-mail-https', 'hostname': config['hostname'], 'upstream': bindings[0].rsplit(':', 1)[0]}]
    if 'webmail' in config:
        try:
            webmail = config['webmail']
            if type(webmail['loopback_port']) is not int:
                raise MailProxyError('Webmail requires an explicit integer loopback port')
            routes.append({'id': 'noebs-webmail-https', 'hostname': webmail['hostname'],
                           'upstream': '127.0.0.1:' + str(webmail['loopback_port'])})
        except (TypeError, KeyError):
            raise MailProxyError('Webmail requires an explicit hostname and loopback port') from None
    return routes


def validate_deployment(deployment, target):
    try:
        pod = deployment['spec']['template']['spec']
        containers = [container for container in pod['containers'] if container['name'] == target['container']]
        if deployment['spec']['replicas'] != 1 or pod['hostNetwork'] is not True or len(containers) != 1:
            raise MailProxyError('Mail proxy requires the existing single host-network Caddy instance')
        container = containers[0]
        command = container.get('command', []) + container.get('args', [])
        if command != ['caddy', 'run', '--config', CONFIG_PATH]:
            raise MailProxyError('Caddy startup must read the explicit mounted JSON configuration')
        mounts = [mount for mount in container['volumeMounts'] if mount['mountPath'] == CONFIG_PATH]
        if len(mounts) != 1 or mounts[0].get('subPath') != target['config_key'] or mounts[0].get('readOnly') is not True:
            raise MailProxyError('Caddy must mount the explicit ConfigMap JSON key read-only')
        volumes = [volume for volume in pod['volumes'] if volume['name'] == mounts[0]['name']]
        if len(volumes) != 1 or volumes[0]['configMap']['name'] != target['configmap']:
            raise MailProxyError('Caddy configuration volume differs from the explicit ConfigMap')
        items = volumes[0]['configMap'].get('items')
        if items is not None and not any(item == {'key': target['config_key'], 'path': target['config_key']} for item in items):
            raise MailProxyError('Caddy ConfigMap key projection does not match its subPath')
    except (KeyError, TypeError):
        raise MailProxyError('Existing Caddy deployment does not match the explicit proxy target') from None


class RemoteProxy:
    def __init__(self, target):
        self.target = target
        self.opener = build_opener(ProxyHandler({}))

    def kubectl(self, arguments, payload=None, timeout=60):
        result = subprocess.run(['k3s', 'kubectl', '-n', self.target['namespace'], *arguments],
                                input=payload, capture_output=True, timeout=timeout)
        if result.returncode:
            raise MailProxyError('Caddy Kubernetes operation failed; configuration output was withheld')
        return result.stdout

    def deployment(self):
        return json.loads(self.kubectl(['get', 'deployment', self.target['deployment'], '-o', 'json']))

    def configmap(self):
        return json.loads(self.kubectl(['get', 'configmap', self.target['configmap'], '-o', 'json']))

    def live(self):
        try:
            with self.opener.open(ADMIN_URL, timeout=30) as response:
                etag = response.headers.get('Etag')
                if not etag:
                    raise MailProxyError('Caddy admin API must provide an ETag for concurrency protection')
                return json.load(response), etag
        except (HTTPError, URLError, TimeoutError):
            raise MailProxyError('Cannot read the local Caddy admin configuration') from None

    def validate(self, candidate):
        self.kubectl(['exec', '-i', 'deployment/' + self.target['deployment'], '-c', self.target['container'],
                      '--', 'caddy', 'validate', '--config', '/dev/stdin'], json.dumps(candidate).encode())

    def persist(self, configmap, expected_text, desired_text):
        key = self.target['config_key'].replace('~', '~0').replace('/', '~1')
        patch = [
            {'op': 'test', 'path': '/metadata/resourceVersion', 'value': configmap['metadata']['resourceVersion']},
            {'op': 'test', 'path': '/data/' + key, 'value': expected_text},
            {'op': 'replace', 'path': '/data/' + key, 'value': desired_text},
        ]
        return json.loads(self.kubectl(['patch', 'configmap', self.target['configmap'], '--type=json',
                                       '--patch-file=/dev/stdin', '-o', 'json'], json.dumps(patch).encode()))

    def reload(self, candidate, etag):
        request = Request(ADMIN_URL, data=json.dumps(candidate).encode(), method='POST',
                          headers={'Content-Type': 'application/json', 'If-Match': etag})
        try:
            with self.opener.open(request, timeout=45) as response:
                response.read()
        except (HTTPError, URLError, TimeoutError):
            raise MailProxyError('Caddy reload failed or changed concurrently') from None


    def restart(self):
        deployment = self.deployment()
        validate_deployment(deployment, self.target)
        if deployment['spec'].get('strategy', {}).get('type') != 'Recreate':
            raise MailProxyError('Explicit proxy restart requires the existing Recreate deployment strategy')
        self.kubectl(['rollout', 'restart', 'deployment/' + self.target['deployment']])
        self.kubectl(['rollout', 'status', 'deployment/' + self.target['deployment'], '--timeout=180s'], timeout=210)


def reconcile_proxy(action, target, remote, restart_proxy):
    validate_target(target)
    if action not in {'check', 'apply'}:
        raise MailProxyError('Mail proxy action must be check or apply')
    if type(restart_proxy) is not bool:
        raise MailProxyError('Mail proxy restart must be an explicit boolean option')
    deployment = remote.deployment()
    validate_deployment(deployment, target)
    if restart_proxy and deployment['spec'].get('strategy', {}).get('type') != 'Recreate':
        raise MailProxyError('Explicit proxy restart requires the existing Recreate deployment strategy')
    configmap = remote.configmap()
    try:
        original_text = configmap['data'][target['config_key']]
        current = json.loads(original_text)
    except (KeyError, TypeError, ValueError):
        raise MailProxyError('The explicit ConfigMap key must contain valid Caddy JSON') from None
    candidate = desired_config(current, target)
    live, etag = remote.live()
    if live != current:
        raise MailProxyError('Live Caddy configuration differs from its ConfigMap; reconcile the existing drift first')
    changed = candidate != current
    if changed:
        remote.validate(candidate)
    if action == 'check':
        return {'status': 'validated' if changed else 'unchanged', 'hostnames': [route['hostname'] for route in target['routes']]}
    if changed:
        desired_text = json.dumps(candidate, indent=2) + '\n'
        updated = remote.persist(configmap, original_text, desired_text)
        try:
            remote.reload(candidate, etag)
        except MailProxyError:
            # A timeout can hide a successful reload. Inspect the active config
            # before rolling back persistent state; never revert another writer.
            after, _ = remote.live()
            if after != candidate:
                remote.persist(updated, desired_text, original_text)
                raise MailProxyError('Caddy reload did not apply the mail route; ConfigMap update was rolled back') from None
    if restart_proxy:
        # ConfigMap subPath mounts do not update inside existing pods. This
        # explicit operation causes a brief interruption on the shared proxy,
        # refreshing mounted bytes as well as the already reloaded live state.
        remote.restart()
    after, _ = remote.live()
    if after != candidate:
        raise MailProxyError('Caddy does not serve the expected complete configuration after the update')
    result = {'status': 'applied' if changed else 'unchanged', 'hostnames': [route['hostname'] for route in target['routes']],
              'proxy_restarted': restart_proxy}
    if changed and not restart_proxy:
        result['notice'] = 'Live config and ConfigMap updated; the existing subPath mount refreshes on pod replacement. Use --restart-proxy to refresh it now.'
    return result


def ssh_command(args, destination):
    command = [args.ssh, '-o', 'BatchMode=yes', '-o', 'StrictHostKeyChecking=yes',
               '-o', 'ConnectTimeout=20', '-o', 'ServerAliveInterval=15', '-o', 'ServerAliveCountMax=2']
    if args.identity:
        command += ['-i', args.identity, '-o', 'IdentitiesOnly=yes']
    if args.known_hosts:
        command += ['-o', 'UserKnownHostsFile=' + args.known_hosts]
    command += [destination, 'sudo -n python3 -c ' + shlex.quote(Path(__file__).read_text()) + ' --remote']
    return command


def main():
    if sys.argv[1:] == ['--remote']:
        payload = json.load(sys.stdin)
        target = payload['target']
        result = reconcile_proxy(payload['action'], target, RemoteProxy(target), payload['restart_proxy'])
        print(json.dumps(result))
        return
    import yaml
    from runtime import render_compose, validate_runtime
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['check', 'apply'])
    parser.add_argument('--config', type=Path, required=True)
    parser.add_argument('--ssh', required=True, help='Existing strict SSH client or .state/bin/ssh wrapper')
    parser.add_argument('--identity')
    parser.add_argument('--known-hosts')
    parser.add_argument('--restart-proxy', action='store_true',
                        help='Restart the shared Recreate proxy after reload to refresh its subPath mount; briefly interrupts existing websites')
    for field in TARGET_FIELDS - {'routes'}:
        parser.add_argument('--' + field.replace('_', '-'), required=True)
    args = parser.parse_args()
    config = validate_runtime(yaml.safe_load(args.config.read_text()))
    if 'webmail' in config:
        from webmail import validate as validate_webmail
        validate_webmail(config)
    target = {field: getattr(args, field) for field in TARGET_FIELDS - {'routes'}}
    target['routes'] = routes_from_config(config, render_compose(config))
    validate_target(target)
    payload = {'action': args.action, 'target': target, 'restart_proxy': args.restart_proxy}
    result = subprocess.run(ssh_command(args, config['ssh_destination']), input=json.dumps(payload).encode(),
                            capture_output=True, timeout=420)
    if result.returncode:
        # The remote runner emits only this module's sanitized errors.
        message = result.stderr.decode(errors='replace').strip()
        raise MailProxyError(message or 'Mail HTTPS reconciliation failed on the existing host')
    response = json.loads(result.stdout)
    if (response.get('status') not in {'unchanged', 'validated', 'applied'}
            or response.get('hostnames') != [route['hostname'] for route in target['routes']]):
        raise MailProxyError('Mail proxy returned an invalid reconciliation result')
    print(json.dumps(response))


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print(str(error) if isinstance(error, MailProxyError) else 'Mail HTTPS configuration failed', file=sys.stderr)
        sys.exit(1)
