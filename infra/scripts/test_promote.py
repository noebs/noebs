import hashlib
from email.message import Message
import io
import json
from pathlib import Path
from subprocess import CompletedProcess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch
from urllib.error import HTTPError, URLError

import yaml
import promote as promotion

from promote import verify_receipt, require_public_dns, verify_login_response


class DomainRegistrationTests(unittest.TestCase):
    host = 'api.noebs.sd'
    key = Path('deployment-key')

    def test_registered_domain_skips_owner_command_and_disables_redirects(self):
        response = Mock(status=200)
        context = Mock()
        context.__enter__ = Mock(return_value=response)
        context.__exit__ = Mock(return_value=False)
        opener = Mock()
        opener.open.return_value = context
        with patch.object(promotion, 'build_opener', return_value=opener) as build, patch.object(promotion, 'ssh') as ssh:
            promotion.ensure_public_domain(self.key, self.host)
        ssh.assert_not_called()
        build.assert_called_once_with(promotion.NoRedirect)
        request = opener.open.call_args.args[0]
        self.assertEqual(request.full_url, 'https://api.noebs.sd/test')
        self.assertFalse(request.has_header('Authorization'))
        self.assertFalse(request.has_header('Cookie'))
        self.assertIsNone(promotion.NoRedirect().redirect_request(None, None, 302, '', {}, 'https://other.example'))

    def test_explicit_exe_unregistered_response_registers_once(self):
        response = HTTPError('https://' + self.host + '/test', 421, 'Misdirected Request', {},
                             io.BytesIO(b'<html><title>Domain Not Configured</title></html>'))
        opener = Mock()
        opener.open.side_effect = response
        with patch.object(promotion, 'build_opener', return_value=opener), patch.object(promotion, 'ssh') as ssh:
            promotion.ensure_public_domain(self.key, self.host)
        ssh.assert_called_once_with(self.key, 'exe.dev', 'domain add noebs-workers api.noebs.sd --json')

    def test_probe_errors_do_not_trigger_registration_or_expose_response_data(self):
        errors = [URLError('private-value'), TimeoutError('private-value')]
        for code in [301, 302, 401, 403, 404, 421, 500, 502]:
            errors.append(HTTPError('https://' + self.host + '/test', code, 'private-value', {},
                                    io.BytesIO(b'private-value')))
        for error in errors:
            opener = Mock()
            opener.open.side_effect = error
            with self.subTest(error=type(error).__name__, code=getattr(error, 'code', None)), \
                 patch.object(promotion, 'build_opener', return_value=opener), patch.object(promotion, 'ssh') as ssh:
                with self.assertRaises(RuntimeError) as failure:
                    promotion.ensure_public_domain(self.key, self.host)
                self.assertNotIn('private-value', str(failure.exception))
                ssh.assert_not_called()


class PrivateLoginTests(unittest.TestCase):
    private_origin = 'https://noebs-workers.tail09832.ts.net'
    origin = 'https://api.noebs.sd'

    def headers(self):
        headers = Message()
        headers['Location'] = self.origin + '/auth/realms/noebs/protocol/openid-connect/auth?client_id=noebs-backoffice&redirect_uri=https%3A%2F%2Fnoebs-workers.tail09832.ts.net%2Fbackoffice%2Foauth%2Fcallback'
        headers['Set-Cookie'] = '__Host-noebs_backoffice_flow=example; Path=/; Secure; HttpOnly; SameSite=Lax'
        return headers

    def test_exact_public_identity_and_cookie_are_required(self):
        verify_login_response(303, self.headers(), self.origin, self.private_origin)
        for field, value in [('Location', 'https://noebs-workers.exe.xyz/auth'),
                             ('Set-Cookie', '__Host-noebs_backoffice_flow=example; Path=/; HttpOnly')]:
            headers = self.headers()
            headers.replace_header(field, value)
            with self.subTest(field=field), self.assertRaises(RuntimeError):
                verify_login_response(303, headers, self.origin, self.private_origin)


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
        self.config = {'public_host': 'api.noebs.sd', 'backoffice_origin': 'https://noebs-workers.tail09832.ts.net', 'trusted_proxy_cidrs': ['127.0.0.1/32'], 'service_config': {}}
        self.image = 'ghcr.io/noebs/noebs@sha256:' + 'a' * 64
        self.smtp_policy = {
            'apiVersion': 'networking.k8s.io/v1', 'kind': 'NetworkPolicy',
            'metadata': {'name': 'keycloak-smtp-egress', 'namespace': 'noebs'},
            'spec': {'podSelector': {'matchLabels': {'app.kubernetes.io/name': 'keycloak'}},
                     'policyTypes': ['Egress'], 'egress': [
                         {'to': [{'ipBlock': {'cidr': '100.64.1.8/32'}}],
                          'ports': [{'protocol': 'TCP', 'port': 465}]}]},
        }

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
            (release / 'platform').mkdir()
            (release / 'platform/keycloak-smtp-egress.yaml').write_text(yaml.safe_dump(self.smtp_policy))
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
        elif command == 'tailscale ip -4':
            result = b'100.85.107.107\n'
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
             patch.object(promotion, 'public_domain_registered', return_value=True), \
             patch.object(promotion, 'verify_public', side_effect=verify_public), \
             patch.object(promotion, 'check_private_backoffice_target') as private_check, \
             patch.object(promotion, 'reconcile_private_backoffice') as private_reconcile, \
             patch.object(promotion, 'verify_private_backoffice') as private_verify, patch.dict(promotion.os.environ):
            promotion.promote(self.args, Mock())
            private_check.assert_called_once()
            private_reconcile.assert_called_once()
            private_verify.assert_called_once()

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

    def test_prepared_smtp_egress_is_applied_including_permission_removal(self):
        for egress in [self.smtp_policy['spec']['egress'], []]:
            self.events = []
            self.smtp_policy['spec']['egress'] = egress
            self.invoke(Mock())
            applied = [item for event in self.events
                       if event[0] == 'ssh' and 'kubectl apply' in event[2]
                       for item in json.loads(event[3]['input'])['items']
                       if item.get('metadata', {}).get('name') == 'keycloak-smtp-egress']
            self.assertEqual(applied, [self.smtp_policy])

    def test_enabled_callback_source_is_installed_before_callback_service(self):
        self.config['service_config']['wallet-worker'] = {
            'interop_tenant': 'noebs', 'interop_backend_allowed_peers': ['100.76.217.90'],
            'interop_backend_listen_address': '0.0.0.0:4002',
            'interop_sdk_outbound_url': 'http://100.76.217.90:30401',
            'interop_sdk_inbound_url': 'http://100.76.217.90:30400',
        }
        self.invoke(Mock())
        install = next(i for i, event in enumerate(self.events)
                       if event[0] == 'ssh' and event[2] == 'sudo sh -s')
        self.assertIn(b'systemctl enable noebs-callback-source.service', self.events[install][3]['input'])
        callback = next(i for i, event in enumerate(self.events)
                        if event[0] == 'ssh' and 'kubectl apply' in event[2]
                        and any(obj.get('metadata', {}).get('name') == 'wallet-interop-callback'
                                for obj in json.loads(event[3]['input'])['items']))
        self.assertLess(install, callback)

    def test_disabled_callback_source_is_removed_after_callback_service(self):
        self.invoke(Mock())
        remove_service = next(i for i, event in enumerate(self.events)
                              if event[0] == 'ssh' and 'delete service/wallet-interop-callback' in event[2])
        remove_rule = next(i for i, event in enumerate(self.events)
                           if event[0] == 'ssh' and event[2] == 'sudo sh -s')
        self.assertLess(remove_service, remove_rule)
        self.assertIn(b'systemctl disable --now noebs-callback-source.service', self.events[remove_rule][3]['input'])

    def test_fresh_ingress_definitions_are_created_before_waiting_for_establishment(self):
        self.invoke(Mock())
        commands = [event[2] for event in self.events if event[0] == 'ssh']
        created = [command for command in commands if 'wait --for=create' in command]
        self.assertEqual(len(created), 3)
        for command in created:
            self.assertEqual(command.count('crd/'), 1)
        established = next(i for i, command in enumerate(commands) if 'wait --for=condition=Established' in command)
        self.assertTrue(all(commands.index(command) < established for command in created))
        self.assertLess(established, next(i for i, command in enumerate(commands) if 'kubectl apply' in command))



class PrivateHTTPSVerificationTests(unittest.TestCase):
    def test_private_probe_requires_verified_https_and_exact_callback_without_exposing_cookie(self):
        origin = 'https://api.noebs.sd'
        private_origin = 'https://noebs-workers.tail09832.ts.net'
        headers = PrivateLoginTests().headers()
        payload = b'HTTP/2 303\r\n' + str(headers).replace('\n', '\r\n').encode() + b'\r\n'
        netmap = json.dumps({'BackendState': 'Running', 'Self': {'DNSName': 'verifier.tail09832.ts.net.'},
                             'Peer': {'worker': {'DNSName': 'noebs-workers.tail09832.ts.net.', 'Online': True,
                                                 'TailscaleIPs': ['100.85.107.107']}}}).encode()
        with patch.object(promotion, 'ssh', side_effect=[CompletedProcess([], 0, netmap), CompletedProcess([], 0, payload)]) as ssh:
            promotion.verify_private_backoffice('key', 'tailnet-peer', origin, private_origin)
        command = ssh.call_args.args[2]
        self.assertIn('--proto =https', command)
        self.assertIn('--noproxy "*"', command)
        self.assertIn('--resolve noebs-workers.tail09832.ts.net:443:100.85.107.107', command)
        self.assertNotIn('--insecure', command)
        self.assertTrue(ssh.call_args.kwargs['capture_output'])
        bad = payload.replace(b'noebs-workers.tail09832.ts.net%2Fbackoffice', b'api.noebs.sd%2Fbackoffice')
        with patch.object(promotion, 'ssh', side_effect=[CompletedProcess([], 0, netmap), CompletedProcess([], 0, bad)]), self.assertRaises(RuntimeError):
            promotion.verify_private_backoffice('key', 'tailnet-peer', origin, private_origin)

    def test_private_probe_rejects_untrusted_or_ambiguous_peer_resolution(self):
        private_origin = 'https://noebs-workers.tail09832.ts.net'
        worker = {'DNSName': 'noebs-workers.tail09832.ts.net.', 'Online': True, 'TailscaleIPs': ['100.85.107.107']}
        valid = {'BackendState': 'Running', 'Self': {'DNSName': 'verifier.tail09832.ts.net.'}, 'Peer': {'worker': worker}}
        self.assertEqual(promotion.private_peer_address(valid, private_origin), '100.85.107.107')
        variants = [valid | {'BackendState': 'Stopped'}, valid | {'Self': worker}, valid | {'Peer': {}},
                    valid | {'Peer': {'worker': worker, 'duplicate': worker}},
                    valid | {'Peer': {'worker': worker | {'Online': False}}},
                    valid | {'Peer': {'worker': worker | {'TailscaleIPs': ['213.199.63.78']}}},
                    valid | {'Peer': {'worker': worker | {'TailscaleIPs': ['100.85.107.107', '100.85.107.108']}}}]
        for status in variants:
            with self.subTest(status=status), self.assertRaises(ValueError):
                promotion.private_peer_address(status, private_origin)

    def test_public_probe_denies_backoffice_and_verifies_public_account_login(self):
        calls = []
        class Response(io.BytesIO):
            status = 200
            headers = {'Content-Type': 'application/json'}
        def public_fetch(request, **kwargs):
            target = request.full_url
            if target.endswith('/test'):
                return Response(b'{}')
            if target.endswith('/auth/realms/noebs/.well-known/openid-configuration'):
                return Response(b'{"issuer":"https://api.noebs.sd/auth/realms/noebs"}')
            if target.endswith('/.well-known/assetlinks.json'):
                return Response(b'[{}]')
            raise HTTPError(target, 404, 'Not Found', {}, io.BytesIO())
        def auth_fetch(request, **kwargs):
            target = request if isinstance(request, str) else request.full_url
            calls.append(target)
            if target.endswith('/account/login'):
                headers = Message()
                headers['Location'] = 'https://api.noebs.sd/auth/realms/noebs/protocol/openid-connect/auth?client_id=noebs-web&redirect_uri=https%3A%2F%2Fapi.noebs.sd%2Faccount%2Foauth%2Fcallback'
                headers['Set-Cookie'] = '__Host-noebs_account_flow=example; Path=/; Secure; HttpOnly; SameSite=Lax'
                raise HTTPError(target, 303, 'See Other', headers, io.BytesIO())
            raise HTTPError(target, 404, 'Not Found', {}, io.BytesIO())
        opener = Mock(open=Mock(side_effect=auth_fetch))
        with patch.object(promotion, 'urlopen', side_effect=public_fetch), patch.object(promotion, 'build_opener', return_value=opener):
            promotion.verify_public('api.noebs.sd', 'https://api.noebs.sd')
        self.assertEqual(len(calls), 29)
        self.assertEqual(calls[-1], 'https://api.noebs.sd/account/login')


if __name__ == '__main__':
    unittest.main()
