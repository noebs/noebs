"""Validate deployment inputs and render the noebs fleet configuration."""
import copy
import ipaddress
from pathlib import Path
import re
import shutil

import yaml

from reconcile import ROOT
from private_backoffice import private_host


class InvalidDeployment(ValueError):
    pass


class DeploymentLoader(yaml.SafeLoader):
    pass


def unique_mapping(loader, node, deep=False):
    mapping = {}
    for key_node, value_node in node.value:
        key = loader.construct_object(key_node, deep=deep)
        if not isinstance(key, str) or key in mapping:
            raise InvalidDeployment('Deployment mappings require distinct string keys')
        mapping[key] = loader.construct_object(value_node, deep=deep)
    return mapping


DeploymentLoader.add_constructor(yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, unique_mapping)


def load_document(path):
    try:
        return yaml.load(Path(path).read_text(), Loader=DeploymentLoader)
    except yaml.YAMLError:
        raise InvalidDeployment('Deployment configuration must be valid YAML') from None


def load_config(path):
    value = load_document(path)
    fields = {'api_version', 'public_host', 'backoffice_origin', 'trusted_proxy_cidrs', 'service_config'}
    if not isinstance(value, dict) or set(value) != fields:
        raise InvalidDeployment('Deployment requires exactly: ' + ', '.join(sorted(fields)))
    if value['api_version'] != 'noebs.infrastructure/v1':
        raise InvalidDeployment('Unsupported deployment api_version')
    if value['public_host'] != 'api.noebs.sd':
        raise InvalidDeployment('public_host must match the application and OIDC authority: api.noebs.sd')
    try:
        private_host(value['backoffice_origin'])
    except ValueError as error:
        raise InvalidDeployment(str(error)) from None
    peers = value['trusted_proxy_cidrs']
    if (not isinstance(peers, list) or not peers or any(not isinstance(peer, str) for peer in peers)
            or len(peers) != len(set(peers))):
        raise InvalidDeployment('trusted_proxy_cidrs must explicitly identify the ingress proxy peers')
    for peer in peers:
        try:
            network = ipaddress.ip_network(peer, strict=True)
        except (ValueError, TypeError):
            raise InvalidDeployment('Invalid trusted proxy CIDR') from None
        if str(network) != peer or network.prefixlen == 0:
            raise InvalidDeployment('Trusted proxy CIDRs must be canonical and scoped')
    if not isinstance(value['service_config'], dict):
        raise InvalidDeployment('service_config must be an explicit mapping, including when empty')
    data = yaml.safe_load((ROOT / 'infra/kubernetes/base/configmap.yaml').read_text())['data']
    for role, settings in value['service_config'].items():
        if not isinstance(role, str) or role + '.service.yaml' not in data or not isinstance(settings, dict):
            raise InvalidDeployment('Unknown service_config role or invalid settings')
        reserved = {'service_role', 'db_url', 'db_driver', 'oidc', 'keycloak_proxy_trusted_addresses',
                    'backoffice_origin', 'backoffice_redirect_url', 'backoffice_post_logout_url', 'wallet_authorizer_redirect_url',
                    'mobile_redirect_url', 'web_redirect_url', 'web_post_logout_url',
                    'service_discovery', 'grpc_service_discovery'}
        if reserved & settings.keys():
            raise InvalidDeployment('service_config cannot replace workload, database or public authentication authority')
    return value


def validate_fleet(path):
    fleet = load_document(path)
    if not isinstance(fleet, dict) or set(fleet) != {'machines'} or not isinstance(fleet['machines'], dict):
        raise InvalidDeployment('Fleet configuration requires an explicit machines mapping')
    fields = {'role', 'image', 'cpus', 'memory_gib', 'disk_gib', 'public_http'}
    counts = {role: 0 for role in ('control', 'data', 'workers', 'backup', 'telegram')}
    for name, machine in fleet['machines'].items():
        if not re.fullmatch(r'noebs-[a-z0-9-]+', name) or not isinstance(machine, dict) or set(machine) != fields:
            raise InvalidDeployment('Fleet machines require noebs- names and explicit image, capacity and role')
        if not isinstance(machine['role'], str) or machine['role'] not in counts:
            raise InvalidDeployment('Fleet machine role is unsupported')
        if not isinstance(machine['image'], str) or not re.fullmatch(r'[^\s]+@sha256:[0-9a-f]{64}', machine['image']):
            raise InvalidDeployment('Fleet machine images require immutable SHA256 digests')
        if any(type(machine[key]) is not int or machine[key] <= 0 for key in ('cpus', 'memory_gib', 'disk_gib')):
            raise InvalidDeployment('Fleet machine capacities must be positive integers')
        if type(machine['public_http']) is not bool or machine['public_http'] != (name == 'noebs-workers'):
            raise InvalidDeployment('Only the noebs-workers ingress VM must set public_http=true')
        counts[machine['role']] += 1
    if counts['control'] != 1 or counts['data'] != 1 or counts['workers'] < 1 or counts['backup'] != 1:
        raise InvalidDeployment('Fleet requires one control, data and backup host and at least one worker')
    if fleet['machines'].get('noebs-workers', {}).get('role') != 'workers':
        raise InvalidDeployment('Fleet requires the noebs-workers ingress VM with role workers')
    return fleet


def prepare_source(destination, config, keycloak_peers=None):
    """Apply explicit operator settings at the deployment boundary."""
    shutil.copytree(ROOT / 'infra/kubernetes', destination / 'infra/kubernetes')
    postgres_path = Path('deploy/docker/postgres/001-service-databases.sql')
    (destination / postgres_path).parent.mkdir(parents=True)
    shutil.copyfile(ROOT / postgres_path, destination / postgres_path)
    path = destination / 'infra/kubernetes/base/configmap.yaml'
    manifest = yaml.safe_load(path.read_text())
    data = manifest['data']
    common = yaml.safe_load(data['config.yaml'])
    backoffice_origin = config['backoffice_origin']
    host = private_host(backoffice_origin)
    common['noebs']['backoffice_origin'] = backoffice_origin
    common['noebs']['backoffice_redirect_url'] = backoffice_origin + '/backoffice/oauth/callback'
    common['noebs']['backoffice_post_logout_url'] = backoffice_origin + '/backoffice/oauth/logout/callback'
    if keycloak_peers is not None:
        common['noebs']['keycloak_proxy_trusted_addresses'] = ','.join(keycloak_peers)
    data['config.yaml'] = yaml.safe_dump(common, sort_keys=False)
    for role, settings in config['service_config'].items():
        name = role + '.service.yaml'
        service = yaml.safe_load(data[name])
        service['noebs'].update(settings)
        data[name] = yaml.safe_dump(service, sort_keys=False)
    path.write_text(yaml.safe_dump(manifest, sort_keys=False))
    authority_path = destination / 'infra/kubernetes/keycloak-authority/keycloak-desired-state.yaml'
    authority = yaml.safe_load(authority_path.read_text())
    authority['backoffice_origin'] = backoffice_origin
    for client in authority['interactive_clients']:
        if client['client_id'] == 'noebs-backoffice':
            client['redirect_uris'] = [common['noebs']['backoffice_redirect_url'],
                                       backoffice_origin + '/backoffice/setup-complete']
            client['post_logout_redirect_uris'] = [common['noebs']['backoffice_post_logout_url']]
    authority_path.write_text(yaml.safe_dump(authority, sort_keys=False))
    private_path = destination / 'infra/kubernetes/ingress/backoffice.yaml'
    private_objects = list(yaml.safe_load_all(private_path.read_text()))
    private_objects[0]['spec']['routes'][0]['match'] = 'Host(`' + host + '`) && PathRegexp(`(?i)^/backoffice(?:/|$)`) '
    private_objects[1]['spec']['headers']['customRequestHeaders']['X-Forwarded-Host'] = host
    private_path.write_text(yaml.safe_dump_all(private_objects, sort_keys=False))


def render_ingress_config(config):
    manifest = yaml.safe_load((ROOT / 'infra/kubernetes/ingress/traefik-config.yaml').read_text())
    values = yaml.safe_load(manifest['spec']['valuesContent'])
    values['ports']['web']['forwardedHeaders']['trustedIPs'] = config['trusted_proxy_cidrs']
    manifest['spec']['valuesContent'] = yaml.safe_dump(values, sort_keys=False)
    return yaml.safe_dump(manifest, sort_keys=False)


def configure_workload(obj, image, revision, fingerprint):
    result = copy.deepcopy(obj)
    kind = result['kind']
    if kind not in {'Deployment', 'StatefulSet', 'Job', 'CronJob'}:
        return result
    template = result['spec']['jobTemplate']['spec']['template'] if kind == 'CronJob' else result['spec']['template']
    annotations = template.setdefault('metadata', {}).setdefault('annotations', {})
    annotations['noebs.dev/release'] = revision
    annotations['noebs.dev/config'] = fingerprint
    for container in template['spec'].get('containers', []) + template['spec'].get('initContainers', []):
        if container['image'].startswith(('ghcr.io/noebs/noebs', 'noebs-bootstrap:')):
            container['image'] = image
    return result


def require_clean_revision():
    from reconcile import run
    revision = run(['git', 'rev-parse', 'HEAD'], cwd=ROOT, capture_output=True).stdout.decode().strip()
    if not re.fullmatch('[0-9a-f]{40}', revision):
        raise InvalidDeployment('Deployment requires an exact source revision')
    changed = run(['git', 'status', '--porcelain', '--untracked-files=normal'], cwd=ROOT, capture_output=True).stdout
    if changed:
        raise InvalidDeployment('Commit the release before deploying; the checkout must match its image receipt')
    return revision
