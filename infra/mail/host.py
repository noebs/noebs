"""Scoped remote mail maintenance, invoked over SSH by deploy.py."""
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import platform
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request


class MailMaintenanceError(RuntimeError):
    pass


def run(command, **kwargs):
    result = subprocess.run(command, capture_output=True, **kwargs)
    if result.returncode:
        # Configuration and native API errors can contain credentials. Never
        # forward their process output to terminal logs.
        raise MailMaintenanceError('Mail maintenance command failed: ' + command[0])
    return result


def compose(path, *arguments):
    return ['docker', 'compose', '--project-name', 'noebs-mail', '--file', str(path), *arguments]


def write_private(path, value, owner=None):
    with tempfile.NamedTemporaryFile(mode='w', prefix='.incoming-', dir=path.parent, delete=False) as output:
        temporary = Path(output.name)
        try:
            output.write(value)
            output.flush()
            os.fsync(output.fileno())
            if owner is not None:
                os.chown(temporary, owner, owner)
            temporary.replace(path)
        finally:
            temporary.unlink(missing_ok=True)


FIREWALL_UNIT = Path('/etc/systemd/system/noebs-mail-firewall.service')
FIREWALL_DESCRIPTION = 'Description=Allow the configured Stalwart mail ports through Docker ingress policy'


def check_firewall_unit(path):
    if path.is_symlink() or (path.exists() and (
            not path.is_file() or FIREWALL_DESCRIPTION not in path.read_text().splitlines())):
        raise MailMaintenanceError('Mail firewall unit path belongs to another service')


def preflight(config, root):
    if os.geteuid() != 0 or platform.system() != 'Linux' or platform.machine() != 'x86_64':
        raise MailMaintenanceError('Mail maintenance requires root on Linux amd64')
    for tool in ['docker', 'ip', 'tar', 'openssl', 'iptables', 'systemctl']:
        if not shutil.which(tool):
            raise MailMaintenanceError('Required mail host command is unavailable: ' + tool)
    check_firewall_unit(FIREWALL_UNIT)
    run(['docker', 'compose', 'version'])
    run(['docker', 'info'])
    addresses = json.loads(run(['ip', '-json', 'address', 'show']).stdout)
    local = {address['local'] for interface in addresses for address in interface.get('addr_info', [])}
    public = {address['local'] for interface in addresses if interface['ifname'] == config['public_interface'] for address in interface.get('addr_info', [])}
    if config['public_ipv4'] not in public:
        raise MailMaintenanceError('The configured public address must belong to the explicit public interface')
    if not {config['public_ipv4'], config['tailscale_ipv4']} <= local:
        raise MailMaintenanceError('The configured public and Tailscale addresses must belong to this host')
    if root.is_symlink() or (root.exists() and not root.is_dir()):
        raise MailMaintenanceError('Mail state must be a dedicated directory')
    marker = root / '.managed-by-noebs'
    if root.exists() and any(root.iterdir()) and not marker.is_file():
        raise MailMaintenanceError('Refusing to adopt an existing unmanaged mail directory')
    if marker.is_file() and marker.read_text() != 'noebs.mail/v1\n':
        raise MailMaintenanceError('Mail state has an unsupported ownership marker')
    for name in ['config', 'data', 'compose.json', 'deployment.json', 'recovery.env', 'deploy.lock', '.managed-by-noebs',
                 'webmail', 'webmail/config', 'webmail/db', 'webmail/roundcube_des_key', 'webmail/php.ini']:
        if (root / name).is_symlink():
            raise MailMaintenanceError('Managed mail paths cannot be symbolic links')
    if (root / 'deployment.json').is_file() and (
            not (root / 'config/config.json').is_file()
            or not (root / 'data').is_dir() or not any((root / 'data').iterdir())):
        raise MailMaintenanceError('Existing mail authority is missing; restore its encrypted backup before setup')
    if (root / 'deployment.json').is_file():
        previous = json.loads((root / 'deployment.json').read_text())
        if 'webmail' in previous:
            if 'webmail' not in config:
                raise MailMaintenanceError('Removing an existing webmail installation requires an explicit migration')
            if not (root / 'webmail/db/sqlite.db').is_file() or not (root / 'webmail/roundcube_des_key').is_file():
                raise MailMaintenanceError('Existing webmail metadata is missing; restore its encrypted backup before setup')
    identifiers = run(['docker', 'ps', '--all', '--quiet', '--filter',
                       'label=com.docker.compose.project=noebs-mail']).stdout.decode().split()
    containers = json.loads(run(['docker', 'inspect', *identifiers]).stdout) if identifiers else []
    if containers and not marker.is_file():
        raise MailMaintenanceError('The noebs-mail Compose project exists without its managed state')
    owned = set()
    for container in containers:
        service = container['Config'].get('Labels', {}).get('com.docker.compose.service')
        if service not in ({'stalwart', 'webmail'} if 'webmail' in config else {'stalwart'}):
            raise MailMaintenanceError('The mail Compose project contains an unexpected service')
        mounts = {mount['Destination']: mount['Source'] for mount in container['Mounts']}
        expected = ({'/etc/stalwart': root / 'config', '/var/lib/stalwart': root / 'data'} if service == 'stalwart'
                    else {'/var/roundcube/db': root / 'webmail/db', '/var/roundcube/config': root / 'webmail/config',
                          '/run/secrets/roundcube_des_key': root / 'webmail/roundcube_des_key',
                          '/usr/local/etc/php/conf.d/zzz-noebs-webmail.ini': root / 'webmail/php.ini'})
        if any(mounts.get(destination) != str(source) for destination, source in expected.items()):
            raise MailMaintenanceError('The mail container storage does not match its managed state directory')
        if container['State']['Running']:
            for bindings in (container['HostConfig']['PortBindings'] or {}).values():
                owned.update((binding['HostIp'], int(binding['HostPort'])) for binding in (bindings or []))
    endpoints = [(config['public_ipv4'], port) for port in [25, 465, 587, 993]] + [('127.0.0.1', 18080)]
    if 'webmail' in config:
        endpoints.append(('127.0.0.1', config['webmail']['loopback_port']))
    for endpoint in endpoints:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
            try:
                listener.bind(endpoint)
            except OSError:
                if endpoint not in owned:
                    raise MailMaintenanceError('A required mail port belongs to another service') from None


def wait_for_api(timeout=90):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen('http://127.0.0.1:18080/healthz/live', timeout=2) as response:
                if response.status == 200:
                    return
        except (OSError, urllib.error.URLError):
            pass
        time.sleep(1)
    raise MailMaintenanceError('The private Stalwart management listener did not become ready')


def prepare_webmail(payload, root):
    key = payload['secrets']['webmail_des_key']
    key_path = root / 'webmail/roundcube_des_key'
    if key_path.exists() and key_path.read_text() != key:
        raise MailMaintenanceError('Changing the webmail session encryption key requires an explicit rotation')
    for name in ['webmail', 'webmail/config', 'webmail/db']:
        path = root / name
        path.mkdir(mode=0o700, exist_ok=True)
        path.chmod(0o700)
        os.chown(path, 33, 33)
    if not key_path.exists():
        write_private(key_path, key, owner=33)
    write_private(root / 'webmail/config/noebs.php', payload['webmail_php'], owner=33)
    write_private(root / 'webmail/php.ini', payload['webmail_php_ini'], owner=33)


def wait_for_webmail(config, timeout=120):
    webmail = config['webmail']
    request = urllib.request.Request('http://127.0.0.1:' + str(webmail['loopback_port']) + '/?_task=login',
                                     headers={'Host': webmail['hostname']})
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(request, timeout=3) as response:
                if response.status == 200 and b'name="_user"' in response.read():
                    return
        except (OSError, urllib.error.URLError):
            pass
        time.sleep(1)
    raise MailMaintenanceError('The private webmail login page did not become ready')


def install_firewall(payload, root):
    config = payload['config']
    check_firewall_unit(FIREWALL_UNIT)
    command = '/usr/bin/python3 ' + str(root / 'firewall.py')
    arguments = ' --interface ' + config['public_interface'] + ' --ipv4 ' + config['public_ipv4']
    unit = ('[Unit]\nDescription=Allow the configured Stalwart mail ports through Docker ingress policy\n'
            'Requires=docker.service\nAfter=docker.service network-online.target noebs-public-docker-firewall.service\n'
            'PartOf=docker.service\n\n[Service]\nType=oneshot\n'
            'ExecStart=' + command + ' apply' + arguments + '\n'
            'ExecStop=' + command + ' remove' + arguments + '\n'
            'RemainAfterExit=yes\n\n[Install]\nWantedBy=docker.service\n')
    path = FIREWALL_UNIT
    # Stop using the old unit parameters before changing an existing endpoint.
    if path.exists():
        run(['systemctl', 'stop', 'noebs-mail-firewall.service'])
    write_private(root / 'firewall.py', payload['firewall_module'])
    write_private(path, unit)
    run(['systemctl', 'daemon-reload'])
    run(['systemctl', 'enable', '--now', 'noebs-mail-firewall.service'])


def apply(payload, root):
    with tempfile.TemporaryDirectory(prefix='incoming-', dir=root) as directory:
        incoming = Path(directory)
        module_path = incoming / 'stalwart_config.py'
        write_private(module_path, payload['native_module'])
        spec = importlib.util.spec_from_file_location('stalwart_config', module_path)
        native = importlib.util.module_from_spec(spec)
        sys.modules[spec.name] = native
        spec.loader.exec_module(native)
        config, secrets = payload['config'], payload['secrets']
        native.validate(config, secrets)
        startup = native.render_startup(config, secrets)
        current = root / 'config/config.json'
        if current.exists():
            try:
                same_store = json.loads(current.read_text()) == json.loads(startup)
            except ValueError:
                same_store = False
            if not same_store:
                raise MailMaintenanceError('Existing mail storage configuration differs; use an explicit storage migration')
        for name in ['config', 'data']:
            path = root / name
            path.mkdir(mode=0o700, exist_ok=True)
            path.chmod(0o700)
            os.chown(path, 2000, 2000)
        if not current.exists():
            write_private(current, startup, owner=2000)
        normal = incoming / 'compose.json'
        recovery = incoming / 'recovery.json'
        write_private(normal, json.dumps(payload['normal_compose']))
        write_private(recovery, json.dumps(payload['recovery_compose']))
        if 'webmail' in config:
            prepare_webmail(payload, root)
        credential_path = root / 'recovery.env'
        write_private(credential_path, 'STALWART_RECOVERY_ADMIN=admin:' + secrets['admin_password'] + '\n')
        try:
            run(compose(normal, 'config', '--quiet'))
            run(compose(recovery, 'config', '--quiet'))
            run(['docker', 'pull', config['image']])
            if 'webmail' in config:
                run(['docker', 'pull', config['webmail']['image']])
                # The client must not run while its account authority is in
                # private recovery. First deployment has no client to stop.
                previous_manifest = root / 'compose.json'
                if previous_manifest.is_file() and 'webmail' in json.loads(previous_manifest.read_text()).get('services', {}):
                    run(compose(previous_manifest, 'stop', 'webmail'))
            run(compose(recovery, 'up', '--detach', '--force-recreate', '--no-deps', 'stalwart'))
            wait_for_api()
            summary = native.reconcile(config, secrets, base_url='http://127.0.0.1:18080')
            # Keep the production manifest only after its native objects were
            # accepted. A failed reconciliation stays private in recovery mode.
            write_private(root / 'compose.json', normal.read_text())
            run(compose(root / 'compose.json', 'up', '--detach', '--force-recreate', '--no-deps', 'stalwart'))
            wait_for_api()
            install_firewall(payload, root)
            if 'webmail' in config:
                run(compose(root / 'compose.json', 'up', '--detach', '--force-recreate', '--no-deps', 'webmail'))
                wait_for_webmail(config)
            write_private(root / 'deployment.json', json.dumps(config, indent=2) + '\n')
            return summary
        finally:
            credential_path.unlink(missing_ok=True)


def backup(root):
    manifest = root / 'compose.json'
    if not manifest.is_file():
        raise MailMaintenanceError('Mail backup requires an applied runtime manifest')
    # The management database contains mailbox data, native credentials, DKIM
    # keys and certificates. Capture it with the process stopped, then resume
    # only a service that was running when this operation began.
    services = list(json.loads(manifest.read_text())['services'])
    if 'stalwart' not in services or not set(services) <= {'stalwart', 'webmail'}:
        raise MailMaintenanceError('Mail backup requires the managed mail services')
    # Stop the client before the server; resume the server before the client.
    services = [service for service in ['webmail', 'stalwart'] if service in services]
    running = []
    for service in services:
        identifiers = run(compose(manifest, 'ps', '--all', '--quiet', service)).stdout.decode().split()
        if len(identifiers) != 1:
            raise MailMaintenanceError('Mail backup requires exactly one container for each managed service')
        state = json.loads(run(['docker', 'inspect', identifiers[0]]).stdout)[0]['State']
        if state['Paused']:
            raise MailMaintenanceError('Resume paused mail services before taking a backup')
        if state['Running'] or state['Restarting']:
            running.append(service)
    try:
        for service in services:
            run(compose(manifest, 'stop', service))
        paths = ['config', 'data', 'compose.json', 'deployment.json', 'firewall.py', '.managed-by-noebs']
        if 'webmail' in services:
            paths.append('webmail')
        result = subprocess.run(['tar', '--directory', str(root), '--create', '--file', '-',
                                 *paths],
                                stdout=sys.stdout.buffer, stderr=subprocess.DEVNULL)
        if result.returncode:
            raise MailMaintenanceError('Mail state archive failed')
    finally:
        for service in reversed(running):
            run(compose(manifest, 'start', service))


def main():
    os.umask(0o077)
    payload = json.load(sys.stdin)
    config = payload['config']
    root = Path(config['state_directory'])
    if payload['action'] not in {'apply', 'backup'}:
        raise MailMaintenanceError('Unsupported mail maintenance action')
    if payload['action'] == 'backup' and not (root / '.managed-by-noebs').is_file():
        raise MailMaintenanceError('Mail backup requires an existing managed runtime')
    preflight(config, root)
    root.mkdir(mode=0o700, exist_ok=True)
    root.chmod(0o700)
    with (root / 'deploy.lock').open('a') as lock:
        try:
            fcntl.flock(lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise MailMaintenanceError('Another mail deployment or backup holds the host lease') from None
        write_private(root / '.managed-by-noebs', 'noebs.mail/v1\n')
        if payload['action'] == 'apply':
            result = apply(payload, root)
            print(json.dumps({'status': 'applied', 'mail': result}))
        elif payload['action'] == 'backup':
            backup(root)
        else:
            raise MailMaintenanceError('Unsupported mail maintenance action')


if __name__ == '__main__':
    def interrupted(signum, frame):
        raise MailMaintenanceError('Mail maintenance was interrupted')

    for signum in [signal.SIGHUP, signal.SIGTERM]:
        signal.signal(signum, interrupted)
    try:
        main()
    except Exception as error:
        # The full native exception can contain credentials or message data.
        message = str(error) if isinstance(error, MailMaintenanceError) else 'Native mail maintenance failed'
        print(message, file=sys.stderr)
        sys.exit(1)
