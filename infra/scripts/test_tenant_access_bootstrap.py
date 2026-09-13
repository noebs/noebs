import contextlib
import copy
import io
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import unittest

import yaml

from tenant_access_bootstrap import ConfigurationError, UniqueLoader, main, render


SCRIPT = Path(__file__).with_name('tenant_access_bootstrap.py')
CATALOG = {'api_version': 'noebs.sd/tenants/v1', 'tenants': [{'id': 'noebs', 'name': 'Noebs'}]}
CATALOG_CONFIGMAP = 'tenant-catalog-d22tgthc77'
OPERATION = '22222222-2222-4222-8222-222222222222'
SUBJECT = '33333333-3333-4333-8333-333333333333'
IMAGE = 'ghcr.io/noebs/noebs@sha256:' + 'ab' * 32
REASON = 'Owner approved initial Noebs operator setup'


class BootstrapRendererTests(unittest.TestCase):
    def manifest(self, **changes):
        values = dict(namespace='noebs', tenant='noebs', operation_id=OPERATION,
                      reason=REASON, image=IMAGE, catalog=copy.deepcopy(CATALOG), tenant_catalog_configmap=CATALOG_CONFIGMAP)
        values.update(changes)
        return render(**values)

    def test_invalid_authority_inputs_fail_before_rendering(self):
        invalid = {
            'namespace': ['', ' noebs', 'Noebs', '-noebs', 'noebs-', 'noebs.other', 'x' * 64],
            'tenant_catalog_configmap': ['', None, 'Catalog', '-catalog', 'catalog-', 'catalog.', 'a..b',
                                         'catalog/name', 'catalog_name', 'x' * 64, 'a.' * 127 + 'a'],
            'tenant': ['', 'default', 'Noebs', 'noebs/other', 'noebs--other', 'other', 'x' * 64],
            'operation_id': ['', 'not-a-uuid', '0' * 32, OPERATION.replace('-', ''), OPERATION + ' '],
            'expected_subject': ['invalid', '00000000-0000-0000-0000-000000000000', SUBJECT.replace('-', '')],
            'image': ['', 'ghcr.io/noebs/noebs:master', 'ghcr.io/noebs/noebs:release',
                      IMAGE + ':tag', IMAGE.upper(), IMAGE[:-1], IMAGE.replace('/noebs/noebs', '/other/noebs')],
            'reason': ['', ' padded', 'padded ', 'line\nbreak', 'tab\tvalue', 'nul\0value', 'é' * 1001],
        }
        for field, values in invalid.items():
            for value in values:
                with self.subTest(field=field, value=value), self.assertRaises(ConfigurationError):
                    self.manifest(**{field: value})

    def test_catalog_rejects_ambiguous_authority(self):
        for catalog in [None, {}, {'api_version': 'other', 'tenants': CATALOG['tenants']},
                        {**CATALOG, 'unknown': True}, {**CATALOG, 'tenants': []},
                        {**CATALOG, 'tenants': CATALOG['tenants'] * 2},
                        {**CATALOG, 'tenants': [{'id': 'noebs', 'name': 'Noebs', 'enabled': True}]},
                        {**CATALOG, 'tenants': [{'id': 'noebs', 'name': ' Noebs'}]},
                        {**CATALOG, 'tenants': [{'id': 'other', 'name': 'Other'}, *CATALOG['tenants']]}]:
            with self.subTest(catalog=catalog), self.assertRaises(ConfigurationError):
                self.manifest(catalog=catalog)
        for ambiguous in [
            'api_version: noebs.sd/tenants/v1\napi_version: noebs.sd/tenants/v1\ntenants: []',
            'api_version: noebs.sd/tenants/v1\ntenants:\n- id: noebs\n  id: other\n  name: Noebs',
        ]:
            with self.assertRaises(ConfigurationError):
                yaml.load(ambiguous, Loader=UniqueLoader)

    def test_explicit_second_tenant_does_not_change_selected_tenant(self):
        catalog = {**CATALOG, 'tenants': [*CATALOG['tenants'], {'id': 'second', 'name': 'Second'}]}
        documents = self.manifest(catalog=catalog, tenant='second', expected_subject=SUBJECT)
        job = documents[-1]
        args = job['spec']['template']['spec']['containers'][0]['args']
        self.assertEqual(args[args.index('--tenant') + 1], 'second')
        self.assertEqual(args[args.index('--expected-subject') + 1], SUBJECT)
        self.assertEqual(job['metadata']['annotations']['noebs.sd/tenant'], 'second')

    def test_job_is_bounded_single_attempt_without_cluster_identity(self):
        documents = self.manifest()
        self.assertEqual([d['kind'] for d in documents],
                         ['ServiceAccount', 'Secret', 'NetworkPolicy', 'NetworkPolicy', 'NetworkPolicy', 'Job'])
        service_account, job = documents[0], documents[-1]
        self.assertFalse(service_account['automountServiceAccountToken'])
        self.assertEqual(service_account['imagePullSecrets'], [{'name': 'ghcr-credentials'}])
        self.assertEqual(job['spec']['backoffLimit'], 0)
        self.assertLessEqual(job['spec']['activeDeadlineSeconds'], 180)
        self.assertGreaterEqual(job['spec']['ttlSecondsAfterFinished'], 3600)
        pod = job['spec']['template']['spec']
        self.assertEqual(pod['restartPolicy'], 'Never')
        self.assertFalse(pod['automountServiceAccountToken'])
        self.assertFalse(pod['enableServiceLinks'])
        self.assertEqual(pod['serviceAccountName'], service_account['metadata']['name'])
        self.assertEqual(pod['securityContext'], {
            'runAsNonRoot': True, 'runAsUser': 10001, 'runAsGroup': 10001, 'fsGroup': 10001,
            'seccompProfile': {'type': 'RuntimeDefault'},
        })
        for forbidden in ['hostNetwork', 'hostPID', 'hostIPC', 'ephemeralContainers']:
            self.assertNotIn(forbidden, pod)
        self.assertEqual(len(pod['containers']), 1)
        container = pod['containers'][0]
        self.assertEqual(container['image'], IMAGE)
        self.assertEqual(container['command'], ['/usr/local/bin/noebs'])
        self.assertEqual(container['args'][0], 'bootstrap-tenant-admin')
        self.assertNotIn('--expected-subject', container['args'])
        self.assertNotIn('env', container)
        self.assertNotIn('envFrom', container)
        self.assertEqual(container['securityContext'], {
            'allowPrivilegeEscalation': False, 'readOnlyRootFilesystem': True, 'capabilities': {'drop': ['ALL']},
        })
        self.assertEqual(container['resources']['limits'], {'cpu': '500m', 'memory': '256Mi'})

    def test_network_readiness_has_no_credentials_or_bootstrap_invocation(self):
        pod = self.manifest()[-1]['spec']['template']['spec']
        self.assertEqual(len(pod['initContainers']), 1)
        gate = pod['initContainers'][0]
        self.assertEqual(gate['image'], IMAGE)
        self.assertEqual(gate['command'][:2], ['/bin/bash', '-ec'])
        self.assertEqual(gate['securityContext'], pod['containers'][0]['securityContext'])
        self.assertEqual(gate['resources']['limits'], {'cpu': '100m', 'memory': '32Mi'})
        for field in ['volumeMounts', 'env', 'envFrom']:
            self.assertNotIn(field, gate)
        script = gate['command'][2]
        self.assertNotIn('noebs', script)
        self.assertNotIn('bootstrap-tenant-admin', script)
        self.assertNotIn('password', script.lower())
        subprocess.run(['/bin/bash', '-n'], input=script, text=True, check=True)

    def test_network_readiness_retries_then_stops_without_running_application(self):
        script = self.manifest()[-1]['spec']['template']['spec']['initContainers'][0]['command'][2]
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            probe = root / 'timeout'
            probe.write_text('''#!/bin/bash
test "$#" = 4 && test "$1" = 2 && test "$2" = /bin/bash && test "$3" = -c && test "$4" = 'exec 3<>/dev/tcp/postgres/5432' || exit 90
read -r attempts < "$READINESS_COUNT"
attempts=$((attempts + 1))
printf '%s\\n' "$attempts" > "$READINESS_COUNT"
test "$attempts" -ge "$READINESS_SUCCEED_AT"
''')
            probe.chmod(0o755)
            pause = root / 'sleep'
            pause.write_text('#!/bin/bash\ntest "$#" = 1 && test "$1" = 1\n')
            pause.chmod(0o755)
            count = root / 'count'
            for succeeds_at, expected_count, expected_exit in [('3', 3, 0), ('99', 15, 1)]:
                count.write_text('0\n')
                result = subprocess.run(['/bin/bash', '-ec', script], capture_output=True, text=True, timeout=5,
                                        env={'PATH': directory, 'READINESS_COUNT': str(count), 'READINESS_SUCCEED_AT': succeeds_at})
                self.assertEqual(result.returncode, expected_exit, result.stderr)
                self.assertEqual(int(count.read_text()), expected_count)

    def test_only_exact_runtime_and_migration_authorities_are_mounted_readonly(self):
        documents = self.manifest()
        pod = documents[-1]['spec']['template']['spec']
        volumes = {v['name']: v for v in pod['volumes']}
        self.assertEqual(set(volumes), {'config', 'identity-auth-secrets', 'migration-secrets', 'tenant-catalog', 'operation'})
        expected_secrets = {'identity-auth-secrets': 'identity-auth-secrets', 'migration-secrets': 'identity-auth-migrate-secrets',
                            'operation': documents[1]['metadata']['name']}
        for name, secret in expected_secrets.items():
            self.assertEqual(set(volumes[name]), {'name', 'secret'})
            source = volumes[name]['secret']
            self.assertEqual(source['secretName'], secret)
            self.assertEqual(source['defaultMode'], 0o440)
            key = 'reason' if name == 'operation' else 'secrets.yaml'
            self.assertEqual(source['items'], [{'key': key, 'path': key}])
        self.assertEqual(volumes['config']['configMap'], {'name': 'noebs-config', 'items': [
            {'key': 'config.yaml', 'path': 'config.yaml'},
            {'key': 'identity-auth.service.yaml', 'path': 'identity-auth.service.yaml'},
        ]})
        self.assertEqual(volumes['tenant-catalog']['configMap'], {'name': CATALOG_CONFIGMAP, 'items': [
            {'key': 'tenant-catalog.yaml', 'path': 'tenant-catalog.yaml'},
        ]})
        container = pod['containers'][0]
        mounted = {m['mountPath'] for m in container['volumeMounts']}
        for flag in ['--config', '--service', '--secrets', '--database-secrets', '--tenant-catalog']:
            self.assertIn(container['args'][container['args'].index(flag) + 1], mounted)
        self.assertTrue(all(m['readOnly'] for m in container['volumeMounts']))
        self.assertEqual(documents[1]['stringData'], {'reason': REASON})
        self.assertTrue(documents[1]['immutable'])

    def test_network_reachability_is_limited_to_selected_job_pg_keycloak_and_dns(self):
        documents = self.manifest()
        job = documents[-1]
        selector = {'matchLabels': job['spec']['template']['metadata']['labels']}
        self.assertEqual(selector['matchLabels']['noebs.sd/operation-id'], OPERATION)
        outbound, pg_inbound, kc_inbound = [d['spec'] for d in documents if d['kind'] == 'NetworkPolicy']
        self.assertEqual(outbound['podSelector'], selector)
        self.assertEqual(set(outbound['policyTypes']), {'Ingress', 'Egress'})
        self.assertEqual(outbound['ingress'], [])
        self.assertEqual(len(outbound['egress']), 3)
        for rule, label, port in zip(outbound['egress'][:2], ['postgres', 'keycloak'], [5432, 8443]):
            # A lone podSelector keeps the destination inside the selected namespace.
            self.assertEqual(rule, {'to': [{'podSelector': {'matchLabels': {'app.kubernetes.io/name': label}}}],
                                    'ports': [{'protocol': 'TCP', 'port': port}]})
        self.assertEqual(outbound['egress'][2], {
            'to': [{'namespaceSelector': {'matchLabels': {'kubernetes.io/metadata.name': 'kube-system'}},
                    'podSelector': {'matchLabels': {'k8s-app': 'kube-dns'}}}],
            'ports': [{'protocol': 'UDP', 'port': 53}, {'protocol': 'TCP', 'port': 53}],
        })
        for policy, label, port in [(pg_inbound, 'postgres', 5432), (kc_inbound, 'keycloak', 8443)]:
            self.assertEqual(policy, {'podSelector': {'matchLabels': {'app.kubernetes.io/name': label}},
                                     'policyTypes': ['Ingress'], 'ingress': [
                                         {'from': [{'podSelector': selector}], 'ports': [{'protocol': 'TCP', 'port': port}]},
                                     ]})

    def test_operation_resources_are_deterministic_and_do_not_collide(self):
        first, retry = self.manifest(), self.manifest()
        self.assertEqual(first, retry)
        other = self.manifest(operation_id=SUBJECT)
        first_names = {(d['kind'], d['metadata']['name']) for d in first}
        other_names = {(d['kind'], d['metadata']['name']) for d in other}
        self.assertTrue(first_names.isdisjoint(other_names))
        for resource in first:
            self.assertLessEqual(len(resource['metadata']['name']), 63)
            self.assertEqual(resource['metadata']['namespace'], 'noebs')
        self.assertNotIn(REASON, yaml.safe_dump([d for d in first if d['kind'] != 'Secret']))


class BootstrapCommandTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.reason = self.root / 'reason'
        self.reason.write_text(REASON + '\n')
        self.catalog = self.root / 'catalog.yaml'
        self.catalog.write_text(yaml.safe_dump(CATALOG))
        self.output = self.root / 'operation.yaml'
        self.arguments = ['--namespace', 'noebs', '--tenant', 'noebs', '--operation-id', OPERATION,
                          '--reason-file', str(self.reason), '--image', IMAGE, '--tenant-catalog', str(self.catalog),
                          '--tenant-catalog-configmap', CATALOG_CONFIGMAP,
                          '--output', str(self.output)]

    def invoke(self, args=None):
        # Rendering must work with no kubectl, SSH, shell or other executable on PATH.
        return subprocess.run([sys.executable, str(SCRIPT), *(self.arguments if args is None else args)],
                              text=True, capture_output=True, env={**os.environ, 'PATH': ''}, timeout=10)

    def test_cli_writes_private_parseable_manifest_without_applying_or_leaking_reason(self):
        result = self.invoke()
        self.assertEqual(result.returncode, 0, result.stderr)
        receipt = json.loads(result.stdout)
        self.assertFalse(receipt['applied'])
        self.assertEqual(receipt['operation_id'], OPERATION)
        self.assertNotIn(REASON, result.stdout + result.stderr)
        self.assertEqual(stat.S_IMODE(self.output.stat().st_mode), 0o600)
        documents = list(yaml.safe_load_all(self.output.read_text()))
        self.assertEqual(documents[-1]['kind'], 'Job')
        self.assertEqual(documents[1]['stringData']['reason'], REASON)
        volumes = documents[-1]['spec']['template']['spec']['volumes']
        self.assertEqual(next(v['configMap']['name'] for v in volumes if v['name'] == 'tenant-catalog'), CATALOG_CONFIGMAP)
        self.assertEqual(set(self.root.iterdir()), {self.reason, self.catalog, self.output})

    def test_cli_rejects_empty_or_invalid_catalog_name_before_writing(self):
        index = self.arguments.index('--tenant-catalog-configmap') + 1
        for value in ['', 'Catalog', 'catalog/other']:
            arguments = list(self.arguments)
            arguments[index] = value
            with self.subTest(value=value):
                self.assertNotEqual(self.invoke(arguments).returncode, 0)
                self.assertFalse(self.output.exists())

    def test_cli_requires_every_boundary_input_without_creating_output(self):
        for index in range(0, len(self.arguments), 2):
            with self.subTest(flag=self.arguments[index]):
                result = self.invoke(self.arguments[:index] + self.arguments[index + 2:])
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(self.output.exists())

    def test_existing_manifest_and_symlink_targets_are_never_overwritten(self):
        self.output.write_text('reviewed existing evidence')
        result = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.output.read_text(), 'reviewed existing evidence')
        self.output.unlink()
        self.output.symlink_to(self.reason)
        result = self.invoke()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.reason.read_text(), REASON + '\n')

    def test_rejected_files_leave_no_partial_manifest_or_reason_in_errors(self):
        for reason in [b'\xff', b'\n', (REASON + '\nextra line').encode(), b'a' * 2002]:
            with self.subTest(reason_length=len(reason)):
                self.reason.write_bytes(reason)
                result = self.invoke()
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(self.output.exists())
                self.assertNotIn(REASON, result.stdout + result.stderr)
        self.reason.write_text(REASON)
        for catalog in ['tenants: [', 'api_version: noebs.sd/tenants/v1\napi_version: noebs.sd/tenants/v1\ntenants: []',
                        'x' * 65537]:
            self.catalog.write_text(catalog)
            result = self.invoke()
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(self.output.exists())

    def test_maximum_utf8_reason_is_preserved_exactly(self):
        reason = 'é' * 1000
        self.reason.write_text(reason + '\n')
        with contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(main(self.arguments), 0)
        documents = list(yaml.safe_load_all(self.output.read_text()))
        self.assertEqual(documents[1]['stringData']['reason'], reason)


if __name__ == '__main__':
    unittest.main()
