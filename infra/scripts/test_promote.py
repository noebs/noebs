import hashlib
from email.message import Message
import json
from pathlib import Path
from subprocess import CompletedProcess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

import yaml
import promote as promotion

from promote import verify_receipt, require_public_dns, verify_login_response


class PublicLoginTests(unittest.TestCase):
    origin = 'https://api.noebs.sd'

    def headers(self):
        headers = Message()
        headers['Location'] = self.origin + '/auth/realms/noebs/protocol/openid-connect/auth?client_id=noebs-backoffice&redirect_uri=https%3A%2F%2Fapi.noebs.sd%2Fbackoffice%2Foauth%2Fcallback'
        headers['Set-Cookie'] = '__Host-noebs_backoffice_flow=example; Path=/; Secure; HttpOnly; SameSite=Lax'
        return headers

    def test_exact_public_identity_and_cookie_are_required(self):
        verify_login_response(303, self.headers(), self.origin)
        for field, value in [('Location', 'https://noebs-workers.exe.xyz/auth'),
                             ('Set-Cookie', '__Host-noebs_backoffice_flow=example; Path=/; HttpOnly')]:
            headers = self.headers()
            headers.replace_header(field, value)
            with self.subTest(field=field), self.assertRaises(RuntimeError):
                verify_login_response(303, headers, self.origin)


class ReceiptTests(unittest.TestCase):
    revision = 'a' * 40
    manifest = b'{"schemaVersion":2}'

    def receipt(self):
        digest = 'sha256:' + hashlib.sha256(self.manifest).hexdigest()
        return {'source_sha': self.revision, 'digest': digest,
                'digest_ref': 'ghcr.io/noebs/noebs@' + digest,
                'tag': 'ghcr.io/noebs/noebs:' + self.revision}

    def verify(self, receipt, manifest=None):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'receipt.json'
            path.write_text(json.dumps(receipt))
            result = CompletedProcess([], 0, self.manifest if manifest is None else manifest)
            with patch('promote.run', return_value=result):
                return verify_receipt(path, self.revision)

    def test_verified_manifest(self):
        receipt = self.receipt()
        self.assertEqual(self.verify(receipt), receipt['digest_ref'])

    def test_receipt_cannot_change_source_registry_or_workload(self):
        for field, value in [('source_sha', 'b' * 40),
                             ('digest_ref', 'example.org/other@' + self.receipt()['digest']),
                             ('tag', 'ghcr.io/noebs/noebs:mojaloop-sdk-' + self.revision)]:
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.verify(self.receipt() | {field: value})

    def test_manifest_bytes_must_match_receipt(self):
        with self.assertRaises(ValueError):
            self.verify(self.receipt(), manifest=b'changed')


class PromotionSequenceTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.work = Path(self.directory.name)
        self.machines = {name: {'ssh_destination': name + '.exe.xyz'} for name in
                         ['noebs-control', 'noebs-data', 'noebs-workers']}
        machines_file = self.work / 'machines.json'
        machines_file.write_text(json.dumps(self.machines))
        self.args = SimpleNamespace(key=self.work / 'key', machines=machines_file, receipts=self.work,
                                    work=self.work, config=self.work / 'deployment.yaml')
        self.events = []
        self.config = {'public_host': 'api.noebs.sd', 'trusted_proxy_cidrs': ['127.0.0.1/32'], 'service_config': {}}
        self.image = 'ghcr.io/noebs/noebs@sha256:' + 'a' * 64

    def tearDown(self):
        self.directory.cleanup()

    def run_command(self, command, **kwargs):
        self.events.append(('run', command, kwargs))
        result = b''
        if command[:2] == ['git', 'rev-parse']:
            result = ('a' * 40).encode()
        elif 'prepare-kubernetes-release' in command:
            release = Path(command[-1])
            release.mkdir()
            (release / 'services').mkdir()
            (release / 'services/wallet-worker.yaml').write_text('noebs: {}')
            (release / 'config.yaml').write_text('noebs: {}')
        elif 'render-kubernetes-secrets' in command:
            result = yaml.safe_dump({'apiVersion': 'v1', 'kind': 'Secret', 'metadata': {'name': 'test-secret'},
                                     'stringData': {'private': 'secret-value'}}).encode()
        elif 'render-edge-internal-transport' in command:
            result = yaml.safe_dump({'apiVersion': 'v1', 'kind': 'Secret', 'metadata': {'name': 'edge-internal-transport'},
                                     'data': {'ca.pem': 'test-ca', 'tls.key': 'test-key', 'tls.crt': 'test-cert'}}).encode()
        elif command[:2] == ['kustomize', 'build']:
            result = yaml.safe_dump({'apiVersion': 'v1', 'kind': 'ConfigMap',
                                     'metadata': {'name': 'test-manifest'}}).encode()
        return CompletedProcess(command, 0, result)

    def ssh(self, key, host, command, **kwargs):
        import shlex
        self.events.append(('ssh', host, command, kwargs))
        result = b''
        if command.startswith('sudo k3s kubectl '):
            args = shlex.split(command)[3:]
            if args == ['get', 'nodes', '-o', 'json']:
                result = json.dumps({'items': [{'metadata': {'name': 'noebs-data'}, 'spec': {'podCIDR': '10.242.0.0/24'}},
                                              {'metadata': {'name': 'noebs-workers'}, 'spec': {'podCIDR': '10.242.1.0/24'}}]}).encode()
            elif 'noebs-keycloak-delete-bootstrap-client' in args and 'get' in args:
                result = b'{"status":{"conditions":[{"type":"Complete","status":"True"}]}}'
            elif any(arg.startswith('pvc/') for arg in args):
                result = b'{"spec":{"volumeName":"pv-test"}}'
            elif 'pv/pv-test' in args:
                result = b'{"spec":{"hostPath":{"path":"/var/lib/rancher/k3s/storage/test"}}}'
            elif 'get' in args and any(arg in args for arg in ['pods', 'deployments,statefulsets,jobs', 'cronjobs']):
                result = b'{"items":[]}'
        elif command.startswith('ip -j route get '):
            result = b'[{"prefsrc":"100.85.107.107"}]'
        elif command.startswith('cat /var/lib/noebs/runtime/'):
            result = b'encrypted-test-input'
        elif '.well-known/openid-configuration' in command:
            result = b'{"issuer":"https://api.noebs.sd/auth/realms/noebs"}'
        return CompletedProcess([], 0, result)

    def invoke(self, verify_public):
        with patch.object(promotion, 'run', side_effect=self.run_command), patch.object(promotion, 'ssh', side_effect=self.ssh), \
             patch.object(promotion, 'load_config', return_value=self.config), \
             patch.object(promotion, 'verify_receipt', return_value=self.image), \
             patch.object(promotion, 'require_public_dns'), patch.object(promotion, 'prepare_source'), \
             patch.object(promotion, 'verify_public', side_effect=verify_public), patch.dict(promotion.os.environ):
            promotion.promote(self.args, Mock())

    def test_public_verification_precedes_retirement_and_secret_errors_are_captured(self):
        def verify(*args):
            self.events.append(('public-verified',))
        self.invoke(verify)
        verified = self.events.index(('public-verified',))
        retirement = [i for i, event in enumerate(self.events) if event[0] == 'ssh' and 'delete deployment/caddy' in event[2]]
        self.assertEqual(len(retirement), 1)
        self.assertGreater(retirement[0], verified)
        secret_batches = []
        for event in self.events:
            if event[0] != 'ssh' or 'kubectl apply' not in event[2]:
                continue
            objects = json.loads(event[3]['input'])['items']
            if any(item['kind'] == 'Secret' for item in objects):
                secret_batches.append(objects)
                self.assertTrue(event[3]['capture_output'])
        self.assertEqual(len(secret_batches), 1)
        edge = next(item for item in secret_batches[0] if item['metadata']['name'] == 'edge-internal-transport')
        self.assertEqual(edge['data']['ca.crt'], 'test-ca')
        self.assertNotIn('ca.pem', edge['data'])
        self.assertTrue((self.work / 'release-receipt.json').is_file())

    def test_failed_public_verification_does_not_retire_existing_resources_or_write_receipt(self):
        with self.assertRaisesRegex(RuntimeError, 'public verification failed'):
            self.invoke(Mock(side_effect=RuntimeError('public verification failed')))
        deletes = [event[2] for event in self.events if event[0] == 'ssh' and 'delete' in event[2]]
        self.assertFalse(any('deployment/caddy' in command or 'statefulset/noebs-mojaloop-redis' in command for command in deletes))
        self.assertFalse((self.work / 'release-receipt.json').exists())



if __name__ == '__main__':
    unittest.main()
