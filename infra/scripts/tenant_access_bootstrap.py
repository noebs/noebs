#!/usr/bin/env python3
"""Render a separately reviewed, one-time tenant administrator bootstrap Job.

This command only writes a private manifest file. It never contacts Kubernetes,
reads credentials, runs a workload, or adds anything to normal release manifests.
Use the same operation ID and reason when resuming an interrupted bootstrap.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import sys
import unicodedata
import uuid

import yaml


DATABASE_READINESS_SCRIPT = '''for attempt in {1..15}; do
  if timeout 2 /bin/bash -c 'exec 3<>/dev/tcp/postgres/5432' >/dev/null 2>&1; then
    exit 0
  fi
  sleep 1
done
echo 'Database network is not ready; bootstrap was not started.' >&2
exit 1
'''


class ConfigurationError(ValueError):
    pass


def require(value, message):
    if not value:
        raise ConfigurationError(message)


class UniqueLoader(yaml.SafeLoader):
    def construct_mapping(self, node, deep=False):
        result = {}
        for key_node, value_node in node.value:
            key = self.construct_object(key_node, deep=deep)
            require(isinstance(key, str) and key not in result, 'Catalog keys must be unique strings')
            result[key] = self.construct_object(value_node, deep=deep)
        return result


def canonical_uuid(value, name):
    try:
        parsed = uuid.UUID(value)
    except (ValueError, TypeError, AttributeError):
        raise ConfigurationError(name + ' must be a canonical nonzero UUID') from None
    require(parsed.int != 0 and str(parsed) == value, name + ' must be a canonical nonzero UUID')


def tenant_id(value):
    require(isinstance(value, str) and len(value) <= 63 and value != 'default'
            and re.fullmatch(r'[a-z][a-z0-9]*(?:-[a-z0-9]+)*', value), 'Tenant must be explicit and canonical')


def validate_catalog(catalog, tenant):
    require(isinstance(catalog, dict) and set(catalog) == {'api_version', 'tenants'}
            and catalog['api_version'] == 'noebs.sd/tenants/v1', 'Invalid authoritative tenant catalog')
    entries = catalog['tenants']
    require(isinstance(entries, list) and entries, 'Tenant catalog must not be empty')
    previous, found = '', False
    for entry in entries:
        require(isinstance(entry, dict) and set(entry) == {'id', 'name'}, 'Invalid tenant catalog entry')
        tenant_id(entry['id'])
        require(entry['id'] > previous, 'Catalog tenants must be ordered and unique')
        require(isinstance(entry['name'], str) and entry['name'] and entry['name'].strip() == entry['name'], 'Tenant names must be nonempty and normalized')
        previous = entry['id']
        found = found or entry['id'] == tenant
    require(found, 'Selected tenant is absent from the authoritative catalog')


def validate_reason(reason):
    require(isinstance(reason, str) and 1 <= len(reason.encode('utf-8')) <= 2000
            and reason.strip() == reason and not any(unicodedata.category(char) == 'Cc' for char in reason),
            'Reason must contain 1–2000 normalized UTF-8 bytes without control characters')


def render(*, namespace, tenant, operation_id, reason, image, catalog, tenant_catalog_configmap, expected_subject=''):
    require(isinstance(namespace, str) and len(namespace) <= 63
            and re.fullmatch(r'[a-z0-9](?:[a-z0-9-]*[a-z0-9])?', namespace), 'Namespace must be an explicit DNS label')
    require(isinstance(tenant_catalog_configmap, str) and 1 <= len(tenant_catalog_configmap) <= 253
            and all(len(label) <= 63 and re.fullmatch(r'[a-z0-9](?:[a-z0-9-]*[a-z0-9])?', label)
                    for label in tenant_catalog_configmap.split('.')),
            'Tenant catalog ConfigMap must be an explicit Kubernetes DNS name')
    tenant_id(tenant)
    canonical_uuid(operation_id, 'Operation ID')
    if expected_subject:
        canonical_uuid(expected_subject, 'Expected subject')
    validate_reason(reason)
    require(isinstance(image, str) and re.fullmatch(r'ghcr\.io/noebs/noebs@sha256:[0-9a-f]{64}', image),
            'Image must be the Noebs repository pinned to a lowercase SHA-256 digest')
    validate_catalog(catalog, tenant)
    name = 'tenant-admin-bootstrap-' + operation_id
    labels = {'app.kubernetes.io/name': 'tenant-admin-bootstrap', 'noebs.sd/operation-id': operation_id}
    annotations = {'noebs.sd/tenant': tenant, 'noebs.sd/reason-sha256': hashlib.sha256(reason.encode()).hexdigest()}

    def metadata(suffix=''):
        return {'name': name + suffix, 'namespace': namespace, 'labels': dict(labels)}

    service_account = {'apiVersion': 'v1', 'kind': 'ServiceAccount', 'metadata': metadata(),
                       'automountServiceAccountToken': False, 'imagePullSecrets': [{'name': 'ghcr-credentials'}]}
    reason_secret = {'apiVersion': 'v1', 'kind': 'Secret', 'metadata': metadata('-why'), 'type': 'Opaque', 'immutable': True, 'stringData': {'reason': reason}}
    arguments = ['bootstrap-tenant-admin', '--config', '/app/config.yaml', '--service', '/app/service.yaml',
                 '--secrets', '/app/secrets.yaml', '--database-secrets', '/app/migration-secrets.yaml',
                 '--tenant-catalog', '/app/tenant-catalog.yaml', '--tenant', tenant,
                 '--operation-id', operation_id, '--reason-file', '/operation/reason']
    if expected_subject:
        arguments.extend(['--expected-subject', expected_subject])
    container = {
        'name': 'bootstrap', 'image': image, 'imagePullPolicy': 'IfNotPresent',
        'command': ['/usr/local/bin/noebs'], 'args': arguments,
        'securityContext': {'allowPrivilegeEscalation': False, 'readOnlyRootFilesystem': True, 'capabilities': {'drop': ['ALL']}},
        'resources': {'requests': {'cpu': '100m', 'memory': '64Mi'}, 'limits': {'cpu': '500m', 'memory': '256Mi'}},
        'volumeMounts': [
            {'name': 'config', 'mountPath': '/app/config.yaml', 'subPath': 'config.yaml', 'readOnly': True},
            {'name': 'config', 'mountPath': '/app/service.yaml', 'subPath': 'identity-auth.service.yaml', 'readOnly': True},
            {'name': 'identity-auth-secrets', 'mountPath': '/app/secrets.yaml', 'subPath': 'secrets.yaml', 'readOnly': True},
            {'name': 'migration-secrets', 'mountPath': '/app/migration-secrets.yaml', 'subPath': 'secrets.yaml', 'readOnly': True},
            {'name': 'tenant-catalog', 'mountPath': '/app/tenant-catalog.yaml', 'subPath': 'tenant-catalog.yaml', 'readOnly': True},
            {'name': 'operation', 'mountPath': '/operation', 'readOnly': True},
        ],
    }
    # Trusted Keycloak CA is in identity-auth-secrets; the independent database
    # CA and migration DSN are in identity-auth-migrate-secrets. The CLI verifies
    # issuer, exact database identity, TLS and the private callback at runtime.
    pod = {
        'serviceAccountName': name, 'automountServiceAccountToken': False, 'restartPolicy': 'Never',
        'enableServiceLinks': False, 'terminationGracePeriodSeconds': 10,
        'securityContext': {'runAsNonRoot': True, 'runAsUser': 10001, 'runAsGroup': 10001, 'fsGroup': 10001, 'seccompProfile': {'type': 'RuntimeDefault'}},
        # Network policies may be programmed after a pod begins running. Probe
        # only TCP in an init container that mounts no credentials.
        # The command still verifies TLS and database identity itself.
        'initContainers': [{
            'name': 'wait-database', 'image': image, 'imagePullPolicy': 'IfNotPresent',
            'command': ['/bin/bash', '-ec', DATABASE_READINESS_SCRIPT],
            'securityContext': {'allowPrivilegeEscalation': False, 'readOnlyRootFilesystem': True, 'capabilities': {'drop': ['ALL']}},
            'resources': {'requests': {'cpu': '10m', 'memory': '16Mi'}, 'limits': {'cpu': '100m', 'memory': '32Mi'}},
        }],
        'containers': [container],
        'volumes': [
            {'name': 'config', 'configMap': {'name': 'noebs-config', 'items': [{'key': 'config.yaml', 'path': 'config.yaml'}, {'key': 'identity-auth.service.yaml', 'path': 'identity-auth.service.yaml'}]}},
            {'name': 'identity-auth-secrets', 'secret': {'secretName': 'identity-auth-secrets', 'defaultMode': 0o440, 'items': [{'key': 'secrets.yaml', 'path': 'secrets.yaml'}]}},
            {'name': 'migration-secrets', 'secret': {'secretName': 'identity-auth-migrate-secrets', 'defaultMode': 0o440, 'items': [{'key': 'secrets.yaml', 'path': 'secrets.yaml'}]}},
            {'name': 'tenant-catalog', 'configMap': {'name': tenant_catalog_configmap, 'items': [{'key': 'tenant-catalog.yaml', 'path': 'tenant-catalog.yaml'}]}},
            {'name': 'operation', 'secret': {'secretName': name + '-why', 'defaultMode': 0o440, 'items': [{'key': 'reason', 'path': 'reason'}]}},
        ],
    }
    job = {'apiVersion': 'batch/v1', 'kind': 'Job', 'metadata': metadata(), 'spec': {
        'backoffLimit': 0, 'activeDeadlineSeconds': 180, 'ttlSecondsAfterFinished': 86400,
        'template': {'metadata': {'labels': dict(labels), 'annotations': dict(annotations)}, 'spec': pod},
    }}
    job['metadata']['annotations'] = dict(annotations)
    workload_selector = {'matchLabels': dict(labels)}
    pg = {'podSelector': {'matchLabels': {'app.kubernetes.io/name': 'postgres'}}}
    keycloak = {'podSelector': {'matchLabels': {'app.kubernetes.io/name': 'keycloak'}}}
    dns = {'namespaceSelector': {'matchLabels': {'kubernetes.io/metadata.name': 'kube-system'}}, 'podSelector': {'matchLabels': {'k8s-app': 'kube-dns'}}}
    egress = {'apiVersion': 'networking.k8s.io/v1', 'kind': 'NetworkPolicy', 'metadata': metadata('-net'), 'spec': {
        'podSelector': workload_selector, 'policyTypes': ['Ingress', 'Egress'], 'ingress': [], 'egress': [
            {'to': [pg], 'ports': [{'protocol': 'TCP', 'port': 5432}]},
            {'to': [keycloak], 'ports': [{'protocol': 'TCP', 'port': 8443}]},
            {'to': [dns], 'ports': [{'protocol': 'UDP', 'port': 53}, {'protocol': 'TCP', 'port': 53}]},
        ],
    }}
    ingress = []
    for suffix, peer, port in [('pg', pg, 5432), ('kc', keycloak, 8443)]:
        ingress.append({'apiVersion': 'networking.k8s.io/v1', 'kind': 'NetworkPolicy', 'metadata': metadata('-' + suffix), 'spec': {
            'podSelector': peer['podSelector'], 'policyTypes': ['Ingress'], 'ingress': [
                {'from': [{'podSelector': workload_selector}], 'ports': [{'protocol': 'TCP', 'port': port}]},
            ],
        }})
    # The Job is last so admission policies and its secret precede it when an
    # operator deliberately applies this separately reviewed manifest later.
    return [service_account, reason_secret, egress, *ingress, job]


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    for argument in ['namespace', 'tenant', 'operation-id', 'reason-file', 'image', 'tenant-catalog', 'tenant-catalog-configmap', 'output']:
        parser.add_argument('--' + argument, required=True)
    parser.add_argument('--expected-subject', default='')
    args = parser.parse_args(argv)
    reason_bytes = Path(args.reason_file).read_bytes()
    require(len(reason_bytes) <= 2001, 'Reason file exceeds 2001 bytes')
    reason = reason_bytes.decode('utf-8').removesuffix('\n')
    catalog_bytes = Path(args.tenant_catalog).read_bytes()
    require(len(catalog_bytes) <= 65536, 'Tenant catalog exceeds 64 KiB')
    catalog = yaml.load(catalog_bytes, Loader=UniqueLoader)
    documents = render(namespace=args.namespace, tenant=args.tenant, operation_id=args.operation_id, reason=reason,
                       image=args.image, catalog=catalog, tenant_catalog_configmap=args.tenant_catalog_configmap,
                       expected_subject=args.expected_subject)
    rendered = yaml.safe_dump_all(documents, sort_keys=False, explicit_start=True)
    output = Path(args.output)
    # No overwrite or stdout fallback: the accountable reason is private and
    # an existing operation manifest must remain available for exact retries.
    fd = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as stream:
        stream.write(rendered)
    print(json.dumps({'manifest': str(output), 'job': documents[-1]['metadata']['name'], 'namespace': args.namespace,
                      'tenant': args.tenant, 'operation_id': args.operation_id, 'applied': False}))
    return 0


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except (ConfigurationError, OSError, UnicodeError, yaml.YAMLError) as error:
        print(str(error) if isinstance(error, ConfigurationError) else 'Cannot read inputs or write the private bootstrap manifest', file=sys.stderr)
        raise SystemExit(1)
