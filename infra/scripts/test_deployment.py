import contextlib
import copy
import importlib.machinery
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

import yaml

import deployment


loader = importlib.machinery.SourceFileLoader('deploy_command', str(Path(__file__).resolve().parents[1] / 'deploy'))
spec = importlib.util.spec_from_loader(loader.name, loader)
deploy_command = importlib.util.module_from_spec(spec)
loader.exec_module(deploy_command)


def config():
    return {'api_version': 'noebs.infrastructure/v1', 'public_host': 'api.noebs.sd',
            'trusted_proxy_cidrs': ['10.42.0.1/32'], 'service_config': {}}


class DeploymentConfigTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.path = Path(self.directory.name) / 'deployment.yaml'

    def tearDown(self):
        self.directory.cleanup()

    def load(self, value):
        self.path.write_text(yaml.safe_dump(value))
        return deployment.load_config(self.path)

    def test_explicit_empty_service_config_is_preserved(self):
        value = config()
        self.assertEqual(self.load(value), value)

    def test_required_fields_have_no_defaults(self):
        for key in config():
            value = config()
            del value[key]
            with self.subTest(key=key), self.assertRaises(deployment.InvalidDeployment):
                self.load(value)

    def test_malformed_settings_fail_with_typed_error(self):
        cases = [None, {}, []]
        for key, value in [('trusted_proxy_cidrs', []), ('trusted_proxy_cidrs', [{}]),
                           ('trusted_proxy_cidrs', ['10.42.0.1/32', '10.42.0.1/32']),
                           ('trusted_proxy_cidrs', ['0.0.0.0/0']),
                           ('trusted_proxy_cidrs', ['10.42.0.1/24']),
                           ('service_config', {'wallet-api': {'db_url': 'should-not-print'}}),
                           ('service_config', {'wallet-api': {'oidc': {'issuer': 'https://wrong'}}}),
                           ('service_config', {False: {}}), ('service_config', {'unknown': {}}),
                           ('public_host', 'other.example')]:
            candidate = config()
            candidate[key] = value
            cases.append(candidate)
        for value in cases:
            with self.subTest(value=value), self.assertRaises(deployment.InvalidDeployment):
                self.load(value)

    def test_duplicate_yaml_keys_and_parse_failures_do_not_echo_values(self):
        for text in ['public_host: first\npublic_host: private-value\n', 'service_config: [private-value']:
            self.path.write_text(text)
            with self.assertRaises(deployment.InvalidDeployment) as failure:
                deployment.load_config(self.path)
            self.assertNotIn('private-value', str(failure.exception))

    def test_fleet_requires_explicit_capacity_and_supported_roles(self):
        original = json.loads((deployment.ROOT / 'infra/exedev/fleet.json').read_text())
        self.path.write_text(json.dumps(original))
        self.assertEqual(deployment.validate_fleet(self.path), original)
        candidates = []
        for field, value in [('cpus', True), ('disk_gib', 0), ('image', 'ubuntu:latest'), ('role', 'unknown'),
                             ('public_http', True), ('public_http', 'false')]:
            candidate = copy.deepcopy(original)
            candidate['machines']['noebs-data'][field] = value
            candidates.append(candidate)
        missing = copy.deepcopy(original)
        del missing['machines']['noebs-data']
        candidates.append(missing)
        for candidate in candidates:
            self.path.write_text(json.dumps(candidate))
            with self.subTest(candidate=candidate), self.assertRaises(deployment.InvalidDeployment):
                deployment.validate_fleet(self.path)

    def test_prepare_source_changes_only_the_named_service_and_excludes_local_secrets(self):
        root = Path(self.directory.name) / 'repo'
        base = root / 'infra/kubernetes/base'
        base.mkdir(parents=True)
        sql = root / 'deploy/docker/postgres/001-service-databases.sql'
        sql.parent.mkdir(parents=True)
        sql.write_text('SELECT 1;')
        (sql.parent / 'tls.key').write_text('private-value')
        manifest = {'data': {'config.yaml': 'noebs: {port: ":8080"}',
                             'wallet-api.service.yaml': 'noebs: {service_role: wallet-api}',
                             'identity-auth.service.yaml': 'noebs: {service_role: identity-auth}'}}
        (base / 'configmap.yaml').write_text(yaml.safe_dump(manifest))
        value = config()
        value['service_config'] = {'wallet-api': {'external_service_url': 'https://external.example'}}
        before = copy.deepcopy(value)
        destination = Path(self.directory.name) / 'source'
        with patch.object(deployment, 'ROOT', root):
            deployment.prepare_source(destination, value)
        result = yaml.safe_load((destination / 'infra/kubernetes/base/configmap.yaml').read_text())
        wallet = yaml.safe_load(result['data']['wallet-api.service.yaml'])
        self.assertEqual(wallet['noebs']['external_service_url'], 'https://external.example')
        self.assertEqual(result['data']['identity-auth.service.yaml'], manifest['data']['identity-auth.service.yaml'])
        self.assertEqual(value, before)
        self.assertFalse((destination / 'deploy/docker/postgres/tls.key').exists())
        self.assertEqual((destination / 'deploy/docker/postgres/001-service-databases.sql').read_text(), 'SELECT 1;')


class DeployBoundaryTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.root = Path(self.directory.name)
        self.key = self.root / 'ssh-key'
        self.age = self.root / 'age-key'
        for path in [self.key, self.age]:
            path.write_text('test-key')
            path.chmod(0o600)
        self.argv = ['infra/deploy', '--config', str(self.root / 'config.yaml'),
                     '--key', str(self.key), '--age-key', str(self.age),
                     '--work', str(self.root / 'work')]
        self.calls = []
        self.old_umask = os.umask(0o077)

    def tearDown(self):
        os.umask(self.old_umask)
        self.directory.cleanup()

    def run_command(self, args, **kwargs):
        self.calls.append((args, kwargs))
        return SimpleNamespace(stdout=b'', stderr=b'')

    @contextlib.contextmanager
    def boundaries(self, extra=('--check',), run=None):
        with contextlib.ExitStack() as stack:
            stack.enter_context(patch.object(sys, 'argv', self.argv + list(extra)))
            stack.enter_context(patch.object(deploy_command, 'load_config', return_value=config()))
            stack.enter_context(patch.object(deploy_command, 'validate_fleet'))
            stack.enter_context(patch.object(deploy_command, 'prepare_source'))
            stack.enter_context(patch.object(deploy_command, 'render_ingress_config', return_value='rendered ingress'))
            stack.enter_context(patch.object(deploy_command.shutil, 'which', return_value='/bin/test-tool'))
            stack.enter_context(patch.object(deploy_command, 'run', side_effect=run or self.run_command))
            stack.enter_context(patch.dict(os.environ))
            yield

    def assert_no_fleet_mutations(self):
        forbidden = {'reconcile.py', 'sync-runtime.py', 'bootstrap-cluster.py', 'promote.py', 'setup-backups.py'}
        self.assertFalse(any(Path(arg).name in forbidden for args, _ in self.calls for arg in args))

    def test_check_validates_runtime_authority_without_fleet_mutations(self):
        output = io.StringIO()
        with self.boundaries(), contextlib.redirect_stdout(output):
            deploy_command.main()
            self.assertEqual(os.environ['SOPS_AGE_KEY_FILE'], str(self.age))
        self.assert_no_fleet_mutations()
        for command in ['prepare-kubernetes-release', 'render-keycloak-bootstrap-secrets']:
            matches = [kwargs for args, kwargs in self.calls if command in args]
            self.assertEqual(len(matches), 1)
            self.assertTrue(matches[0]['capture_output'])
        builds = [args[-1] for args, _ in self.calls if args[0] == 'kustomize']
        self.assertTrue(all('/source/infra/kubernetes/' in path for path in builds))
        self.assertIn('fleet unchanged', output.getvalue())

    def test_invalid_authority_never_reaches_provisioning(self):
        for failed_command in ['prepare-kubernetes-release', 'render-keycloak-bootstrap-secrets', 'kustomize']:
            self.calls = []
            def fail(args, **kwargs):
                self.run_command(args, **kwargs)
                if failed_command in args:
                    raise subprocess.CalledProcessError(1, args, output=b'private-value')
                return SimpleNamespace(stdout=b'', stderr=b'')
            with self.subTest(command=failed_command), self.boundaries(extra=(), run=fail):
                with self.assertRaises(subprocess.CalledProcessError):
                    deploy_command.main()
                self.assert_no_fleet_mutations()

    def test_missing_dns_stops_before_publishing_or_fleet_mutation(self):
        with self.boundaries(extra=()), patch.object(deploy_command, 'require_public_dns',
                side_effect=ValueError('DNS not configured')), patch.object(deploy_command, 'require_clean_revision') as revision:
            with self.assertRaisesRegex(ValueError, 'DNS not configured'):
                deploy_command.main()
            revision.assert_not_called()
        self.assert_no_fleet_mutations()
        self.assertFalse(any('publish-alpha-image.sh' in arg for args, _ in self.calls for arg in args))

    def test_repeated_revision_verifies_existing_receipt_without_republishing(self):
        revision = 'a' * 40
        receipt = self.root / 'work/receipts' / revision / 'noebs-receipt.json'
        receipt.parent.mkdir(parents=True)
        receipt.write_text('{}')
        (self.root / 'work').chmod(0o700)
        with self.boundaries(extra=()), patch.object(deploy_command, 'require_public_dns'), \
             patch.object(deploy_command, 'require_clean_revision', return_value=revision), \
             patch('promote.verify_receipt', side_effect=ValueError('invalid existing receipt')) as verify:
            with self.assertRaisesRegex(ValueError, 'invalid existing receipt'):
                deploy_command.main()
            verify.assert_called_once_with(receipt, revision)
        self.assert_no_fleet_mutations()
        self.assertFalse(any('publish-alpha-image.sh' in arg for args, _ in self.calls for arg in args))

    def test_one_controller_lease_covers_all_release_stages(self):
        events = []
        lease = Mock()
        active = False
        machines = {'noebs-control': {'ssh_destination': 'noebs-control.exe.xyz'}}

        @contextlib.contextmanager
        def hold_lease(command):
            nonlocal active
            active = True
            events.append('lock')
            yield lease
            events.append('unlock')
            active = False

        def stage(name, passed_lease):
            self.assertTrue(active)
            self.assertIs(passed_lease, lease)
            events.append(name)

        scripts = {
            'sync-runtime.py': SimpleNamespace(synchronize=lambda args, machines, lease: stage('credentials', lease)),
            'bootstrap-cluster.py': SimpleNamespace(bootstrap=lambda key, machines, lease, config: stage('cluster', lease)),
            'setup-backups.py': SimpleNamespace(setup=lambda args, machines, lease: stage('backups', lease)),
        }

        def run(args, **kwargs):
            result = self.run_command(args, **kwargs)
            if any(Path(arg).name == 'reconcile.py' for arg in args):
                events.append('reconcile')
                (self.root / 'work/machines.json').write_text(json.dumps(machines))
            return result

        with self.boundaries(extra=(), run=run), patch.object(deploy_command, 'require_public_dns'), \
             patch.object(deploy_command, 'require_clean_revision', return_value='a' * 40), \
             patch('promote.verify_receipt'), patch.object(deploy_command, 'RemoteLease', side_effect=hold_lease), \
             patch.object(deploy_command, 'load_script', side_effect=lambda filename: scripts[filename]), \
             patch.object(deploy_command, 'promote', side_effect=lambda args, lease: stage('application', lease)), \
             contextlib.redirect_stdout(io.StringIO()):
            deploy_command.main()
        self.assertEqual(events, ['reconcile', 'lock', 'credentials', 'cluster', 'application', 'backups', 'unlock'])


if __name__ == '__main__':
    unittest.main()
