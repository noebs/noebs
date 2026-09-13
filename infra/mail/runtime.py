"""Validate and render the existing-host Stalwart container contract."""
import ipaddress
from pathlib import Path
import re

import yaml


class InvalidMailConfiguration(ValueError):
    """An explicit mail deployment setting is missing or invalid."""


RUNTIME_FIELDS = {
    'api_version', 'ssh_destination', 'hostname', 'public_ipv4', 'tailscale_ipv4',
    'image', 'state_directory', 'resources', 'public_interface',
}
HOSTNAME = re.compile(r'(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}')


def validate_runtime(config):
    """Validate runtime fields; the native mail reconciler validates domain state."""
    if not isinstance(config, dict) or not RUNTIME_FIELDS <= config.keys():
        raise InvalidMailConfiguration('Mail deployment requires every explicit runtime setting')
    if config['api_version'] != 'noebs.mail/v1':
        raise InvalidMailConfiguration('Unsupported mail deployment api_version')
    if not isinstance(config['hostname'], str) or not HOSTNAME.fullmatch(config['hostname']):
        raise InvalidMailConfiguration('Mail hostname must be a canonical DNS name')
    if not isinstance(config['public_interface'], str) or not re.fullmatch(r'[a-zA-Z0-9_-]{1,15}', config['public_interface']):
        raise InvalidMailConfiguration('Mail requires an explicit Linux public interface')
    destination = config['ssh_destination']
    if not isinstance(destination, str) or not re.fullmatch(r'[a-z_][a-z0-9_-]*@[a-z0-9][a-z0-9.-]*', destination):
        raise InvalidMailConfiguration('Mail SSH destination requires an explicit user and host')
    for name in ['public_ipv4', 'tailscale_ipv4']:
        try:
            address = ipaddress.IPv4Address(config[name])
        except (ValueError, TypeError) as error:
            raise InvalidMailConfiguration('Mail endpoints must be explicit IPv4 addresses') from error
        if not isinstance(config[name], str) or str(address) != config[name]:
            raise InvalidMailConfiguration('Mail endpoints must be canonical IPv4 strings')
        if name == 'public_ipv4' and not address.is_global:
            raise InvalidMailConfiguration('The public mail listener requires a public IPv4 address')
        if name == 'tailscale_ipv4' and address not in ipaddress.IPv4Network('100.64.0.0/10'):
            raise InvalidMailConfiguration('The management host requires a Tailscale IPv4 address')
    if not isinstance(config['image'], str) or not re.fullmatch(
            r'stalwartlabs/stalwart:v[0-9]+\.[0-9]+\.[0-9]+@sha256:[0-9a-f]{64}', config['image']):
        raise InvalidMailConfiguration('Stalwart requires an official versioned image pinned by SHA256')
    if not isinstance(config['state_directory'], str) or not re.fullmatch(
            r'/var/lib/noebs-mail(?:-[a-z0-9-]+)?', config['state_directory']):
        raise InvalidMailConfiguration('Mail state requires a dedicated /var/lib/noebs-mail directory')
    resources = config['resources']
    if not isinstance(resources, dict) or set(resources) != {'cpus', 'memory_mib', 'pids'}:
        raise InvalidMailConfiguration('Mail resources require explicit CPU, memory and process limits')
    if (type(resources['cpus']) not in (int, float) or not 0 < resources['cpus'] <= 8
            or type(resources['memory_mib']) is not int or not 512 <= resources['memory_mib'] <= 16384
            or type(resources['pids']) is not int or not 64 <= resources['pids'] <= 4096):
        raise InvalidMailConfiguration('Mail resource limits are outside the supported bounds')
    if 'webmail' in config:
        import webmail
        webmail.validate(config)
    return config


def compose_environment(config):
    return {
        'STALWART_IMAGE': config['image'],
        'MAIL_HOSTNAME': config['hostname'],
        'MAIL_PUBLIC_IPV4': config['public_ipv4'],
        'MAIL_STATE_DIRECTORY': config['state_directory'],
        'MAIL_CPUS': str(config['resources']['cpus']),
        'MAIL_MEMORY_MIB': str(config['resources']['memory_mib']),
        'MAIL_PIDS': str(config['resources']['pids']),
    }


def render_compose(config, recovery=False):
    """Render validated runtime settings without consulting ambient variables."""
    values = compose_environment(config)
    template = Path(__file__).with_name('compose.yaml').read_text()
    content = re.sub(r'\$\{([A-Z_][A-Z_0-9]*):\?[^}]+\}', lambda match: values[match[1]], template)
    if '${' in content:
        raise InvalidMailConfiguration('Mail Compose contains an unresolved runtime setting')
    result = yaml.safe_load(content)
    if 'webmail' in config and not recovery:
        import webmail
        result['services']['webmail'] = webmail.render_service(config)
    if recovery:
        service = result['services']['stalwart']
        service['ports'] = ['127.0.0.1:18080:8080']
        service['environment']['STALWART_RECOVERY_MODE'] = '1'
        service['environment']['STALWART_RECOVERY_MODE_PORT'] = '8080'
        service['env_file'] = [{'path': str(Path(config['state_directory']) / 'recovery.env'), 'format': 'raw'}]
    return result
