"""Unit contract tests; opt-in pinned-binary integration uses only local fixtures."""
import base64
import copy
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import socket
import smtplib
import imaplib
import ssl
import subprocess
import tempfile
import threading
import time
import unittest
from unittest.mock import patch

import stalwart_config as mail


def configuration():
    config = {'hostname': 'mail.example.com', 'public_ipv4': '213.199.63.78',
              'domains': ['example.com', 'example.org'], 'dkim_selector': 'noebs2026',
              'mailboxes': [{'email': email, 'name': email, 'quota_bytes': 1073741824,
                             'role': 'Admin' if email == 'postmaster@example.com' else 'User',
                             'password_policy': 'Initial' if email == 'person@example.org' else 'Managed'}
                            for email in ['postmaster@example.com', 'postmaster@example.org', 'accounts@example.com', 'person@example.org']]}
    secrets = {'admin_password': 'fixture-recovery-password-32-characters',
               'cloudflare_api_token': 'fixture-cloudflare-token-32-characters',
               'mailbox_passwords': {m['email']: 'fixture-mailbox-password-32-characters-' + str(i)
                                     for i, m in enumerate(config['mailboxes'])}}
    return config, secrets


class ConfigurationTests(unittest.TestCase):
    def test_missing_fields_fail_before_network_or_subprocess(self):
        config, secrets = configuration()
        for field in config:
            with self.subTest(field=field), patch.object(mail.NativeClient, 'call') as call, patch.object(mail, '_openssl') as crypto:
                missing = dict(config)
                del missing[field]
                with self.assertRaises(mail.ConfigurationError):
                    mail.reconcile(missing, secrets)
                call.assert_not_called()
                crypto.assert_not_called()

    def test_explicit_roles_and_password_map_required(self):
        config, secrets = configuration()
        for value in [None, {}, {'postmaster@example.com': 'fixture-mailbox-password-32-characters'}]:
            with self.subTest(value=value), self.assertRaises(mail.ConfigurationError):
                mail.validate(config, dict(secrets, mailbox_passwords=value))
        config['mailboxes'][0].pop('role')
        with self.assertRaises(mail.ConfigurationError):
            mail.validate(config, secrets)

    def test_passwords_are_distinct_and_environment_line_safe(self):
        config, secrets = configuration()
        for bad in ['short', 'secret\n' + 'x' * 40, 'secret\r' + 'x' * 40, 'secret\x00' + 'x' * 40]:
            with self.subTest(bad=repr(bad)), self.assertRaises(mail.ConfigurationError):
                mail.validate(config, dict(secrets, admin_password=bad))
        values = dict(secrets['mailbox_passwords'])
        values['accounts@example.com'] = values['postmaster@example.com']
        with self.assertRaises(mail.ConfigurationError):
            mail.validate(config, dict(secrets, mailbox_passwords=values))

    def test_password_policy_is_required_without_default(self):
        config, secrets = configuration()
        del config['mailboxes'][0]['password_policy']
        with self.assertRaisesRegex(mail.ConfigurationError, 'password_policy'):
            mail.validate(config, secrets)

    def test_names_domains_quotas_and_privilege_are_validated(self):
        config, secrets = configuration()
        for key, value in [('hostname', 'mail.EXAMPLE.com'), ('public_ipv4', '127.0.0.1'),
                           ('domains', ['example.com', 'example.com']), ('dkim_selector', 'bad.selector')]:
            with self.subTest(key=key), self.assertRaises(mail.ConfigurationError):
                mail.validate(dict(config, **{key: value}), secrets)
        for field, value in [('role', 'superuser'), ('password_policy', 'Guess'), ('quota_bytes', True), ('name', ''), ('email', 'bad@outside.com')]:
            bad = copy.deepcopy(config)
            bad['mailboxes'][0][field] = value
            with self.subTest(field=field), self.assertRaises(mail.ConfigurationError):
                mail.validate(bad, secrets)

    def test_startup_contains_only_explicit_persistent_datastore(self):
        config, secrets = configuration()
        before = copy.deepcopy((config, secrets))
        output = mail.render_startup(config, secrets)
        self.assertEqual(json.loads(output), {'@type': 'RocksDb', 'path': '/var/lib/stalwart/data'})
        self.assertEqual(before, (config, secrets))
        for secret in [secrets['admin_password'], secrets['cloudflare_api_token'], *secrets['mailbox_passwords'].values()]:
            self.assertNotIn(secret, output)

    def test_management_credentials_cannot_leave_loopback(self):
        for url in ['https://mail.example.com', 'http://localhost:8080', 'http://127.0.0.1:8080/redirect',
                    'http://user@127.0.0.1:8080', 'http://127.0.0.1:8080?override=true']:
            with self.subTest(url=url), self.assertRaises(mail.ConfigurationError):
                mail.NativeClient(url, 'secret')

    def test_password_hash_is_stable_and_uses_stdin(self):
        with patch.object(mail.subprocess, 'run') as run:
            run.return_value.stdout = b'$6$fixture\n'
            first = mail._password_hash('accounts@example.com', 'test-password')
            command = run.call_args.args[0]
            self.assertNotIn('test-password', command)
            self.assertEqual(run.call_args.kwargs['input'], b'test-password\n')
            self.assertEqual(first, mail._password_hash('accounts@example.com', 'test-password'))
            self.assertEqual(command, run.call_args.args[0])

    def test_primary_secret_patch_preserves_other_credentials(self):
        client = mail.NativeClient('http://127.0.0.1:18080', 'fixture')
        old = {'id': 'a', 'name': 'accounts', 'credentials': {'0': {'@type': 'Password', 'credentialId': 'b',
                'secret': '****', 'otpAuth': '****'}, '1': {'@type': 'AppPassword', 'secret': '****'}}}
        with patch.object(client, 'objects', return_value=[old]), patch.object(client, 'call') as call:
            client.upsert('Account', {'name': 'accounts'}, {'name': 'accounts',
                          'credentials': {'0': {'@type': 'Password', 'secret': '$6$new'}}})
            self.assertEqual(call.call_args.args[2], {'update': {'a': {'credentials/0/secret': '$6$new'}}})

    def test_initial_secret_is_not_recomputed_or_written_for_existing_account(self):
        from unittest.mock import Mock
        client = mail.NativeClient('http://127.0.0.1:18080', 'fixture')
        old = {'id': 'a', 'name': 'person', 'description': 'Old name', 'credentials': {
            '0': {'@type': 'Password', 'secret': '****', 'otpAuth': '****'}}}
        initial_secret = Mock()
        with patch.object(client, 'objects', return_value=[old]), patch.object(client, 'call') as call:
            client.upsert('Account', {'name': 'person'}, {'name': 'person', 'description': 'New name'},
                          create_only=initial_secret)
        initial_secret.assert_not_called()
        call.assert_called_once_with('Account', 'set', {'update': {'a': {'description': 'New name'}}})

    def test_native_errors_never_echo_secrets(self):
        client = mail.NativeClient('http://127.0.0.1:18080', 'fixture')
        response = {'methodResponses': [['x:Account/set', {'notCreated': {'a': {
            'description': 'do not leak password-secret', 'properties': ['credentials']}}}, 'apply']]}
        from io import BytesIO
        with patch.object(client.opener, 'open', return_value=BytesIO(json.dumps(response).encode())):
            with self.assertRaises(mail.ReconciliationError) as error:
                client.call('Account', 'set', {})
        self.assertNotIn('password-secret', str(error.exception))
        self.assertIn('credentials', str(error.exception))

    def test_console_logging_disables_only_the_unwritable_native_default(self):
        client = mail.NativeClient('http://127.0.0.1:18080', 'fixture')
        tracers = [{'id': 'old', '@type': 'Log', 'path': '/var/log/stalwart',
                    'prefix': 'stalwart.log', 'enable': True},
                   {'id': 'custom', '@type': 'Log', 'path': '/var/lib/stalwart/audit',
                    'prefix': 'audit.log', 'enable': True}]
        with patch.object(client, 'upsert') as upsert, patch.object(client, 'objects', return_value=tracers), \
             patch.object(client, 'call') as call:
            mail._configure_logging(client)
        self.assertEqual(upsert.call_args.args[1], {'@type': 'Stdout'})
        self.assertEqual(upsert.call_args.args[2]['level'], 'info')
        call.assert_called_once_with('Tracer', 'set', {'update': {'old': {'enable': False}}})

    def test_certificate_dns_management_cannot_publish_caa_or_service_records(self):
        config, secrets = configuration()
        with patch.object(mail.NativeClient, 'objects', return_value=[]), \
             patch.object(mail.NativeClient, 'upsert', side_effect=['dns', 'acme', 'host']) as upsert:
            mail._configure_certificate(mail.NativeClient('http://127.0.0.1:18080', 'fixture'), config, secrets)
            domain = upsert.call_args.args[2]
            self.assertEqual(domain['name'], 'mail.example.com')
            self.assertEqual(domain['dnsManagement']['publishRecords'], {'dkim': True})
            self.assertEqual(domain['dkimManagement'], {'@type': 'Manual'})

    def test_certificate_only_domain_rejects_existing_signing_keys_before_writes(self):
        config, secrets = configuration()
        client = mail.NativeClient('http://127.0.0.1:18080', 'fixture')
        with patch.object(client, 'objects', side_effect=[[{'id': 'host', 'name': config['hostname']}],
                                                        [{'id': 'key', 'domainId': 'host'}]]), \
             patch.object(client, 'upsert') as upsert:
            with self.assertRaisesRegex(mail.ReconciliationError, 'certificate-only domain'):
                mail._configure_certificate(client, config, secrets)
            upsert.assert_not_called()


@unittest.skipUnless(os.environ.get('STALWART_TEST_BINARY'), 'Set STALWART_TEST_BINARY to the pinned v0.16.21 binary')
class NativeRegistryTests(unittest.TestCase):
    def test_pinned_native_registry_reapply_credentials_keys_and_dns_schema(self):
        binary = os.environ['STALWART_TEST_BINARY']
        version = subprocess.check_output([binary, '--version'], text=True)
        self.assertEqual('0.16.21', version.strip())
        config, secrets = configuration()
        with tempfile.TemporaryDirectory(prefix='noebs-stalwart-test-') as tmp:
            path = Path(tmp)
            with socket.socket() as sock:
                sock.bind(('127.0.0.1', 0))
                port = sock.getsockname()[1]
            (path / 'config.json').write_text(json.dumps({'@type': 'RocksDb', 'path': str(path / 'data')}))
            env = dict(os.environ, STALWART_RECOVERY_MODE='1', STALWART_RECOVERY_MODE_PORT=str(port),
                       STALWART_RECOVERY_ADMIN='admin:' + secrets['admin_password'])
            with (path / 'server.log').open('w') as log:
                proc = subprocess.Popen([binary, '--config', str(path / 'config.json')], env=env, stdout=log, stderr=log)
                try:
                    client = mail.NativeClient('http://127.0.0.1:' + str(port), secrets['admin_password'])
                    deadline = time.monotonic() + 30
                    while True:
                        try:
                            client.objects('Domain')
                            break
                        except mail.ReconciliationError:
                            if time.monotonic() > deadline:
                                self.fail('Native fixture did not start: ' + (path / 'server.log').read_text())
                            time.sleep(.1)
                    first = mail._configure_mail(client, config, secrets)
                    old_accounts = client.objects('Account')
                    old_keys = client.objects('DkimSignature')
                    second = mail._configure_mail(client, config, secrets)
                    self.assertEqual(first, second)
                    self.assertEqual(client.objects('Account'), old_accounts)
                    self.assertEqual(client.objects('DkimSignature'), old_keys)
                    self.assertEqual(len(old_keys), len(config['domains']))
                    tracers = client.objects('Tracer')
                    self.assertEqual(len(tracers), 1)
                    self.assertEqual(tracers[0]['@type'], 'Stdout')
                    self.assertTrue(tracers[0]['enable'])
                    self.assertEqual({x['name'] for x in client.objects('NetworkListener')},
                                     {'smtp', 'submissions', 'submission', 'imaps', 'http'})
                    for domain in client.objects('Domain'):
                        self.assertEqual(domain['dnsManagement'], {'@type': 'Manual'})
                    auth = client.call('MtaStageAuth', 'get', {'ids': ['singleton']})['list'][0]
                    self.assertEqual(auth['mustMatchSender']['else'], 'true')
                    self.assertEqual(auth['require']['else'], 'local_port != 25')
                    self.assertEqual(auth['saslMechanisms']['else'], 'false')
                    for name in ('accounts', 'person'):
                        account = next(a for a in old_accounts if a['name'] == name)
                        client.call('Account', 'set', {'update': {account['id']: {
                            'credentials/0/secret': 'locally-changed-fixture-password-32-characters'}}})
                    mail._configure_mail(client, config, secrets)
                    self._check_acme_shape(client, config, secrets)
                    proc = self._check_mail_protocols(client, config, secrets, proc, binary, path, log)
                finally:
                    proc.terminate()
                    try:
                        proc.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        proc.kill()
                        proc.wait()

    def _check_mail_protocols(self, client, config, secrets, proc, binary, path, log):
        # ACME/DNS schema was checked in recovery. This protocol fixture uses a
        # locally trusted synthetic certificate and cannot mutate public DNS.
        host = next(d for d in client.objects('Domain') if d['name'] == config['hostname'])
        client.call('Domain', 'set', {'update': {host['id']: {
            'dnsManagement': {'@type': 'Manual'}, 'certificateManagement': {'@type': 'Manual'}}}})
        tasks = client.objects('Task')
        if tasks:
            client.call('Task', 'set', {'destroy': [t['id'] for t in tasks]})
        key_path, cert_path = path / 'tls.key', path / 'tls.crt'
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                        '-subj', '/CN=mail.example.com', '-addext', 'subjectAltName=DNS:mail.example.com',
                        '-keyout', str(key_path), '-out', str(cert_path)], capture_output=True, check=True)
        cert_id = client.call('Certificate', 'set', {'create': {'fixture': {
            'certificate': {'@type': 'Text', 'value': cert_path.read_text()},
            'privateKey': {'@type': 'Text', 'secret': key_path.read_text()}}}})['created']['fixture']['id']
        client.singleton('SystemSettings', {'defaultCertificateId': cert_id})
        ports = {}
        for listener in client.objects('NetworkListener'):
            with socket.socket() as sock:
                sock.bind(('127.0.0.1', 0))
                port = sock.getsockname()[1]
            ports[listener['name']] = port
            client.call('NetworkListener', 'set', {'update': {listener['id']: {'bind': {'127.0.0.1:' + str(port): True}}}})
        client.singleton('MtaStageAuth', {
            'require': mail._expression('local_port != ' + str(ports['smtp'])),
            'saslMechanisms': {'match': {'0': {'if': 'local_port != ' + str(ports['smtp']) + ' && is_tls',
                                               'then': '[plain, login]'}}, 'else': 'false'}})
        proc.terminate()
        proc.wait(timeout=10)
        clean_env = {k: v for k, v in os.environ.items() if not k.startswith('STALWART_RECOVERY_')}
        proc = subprocess.Popen([binary, '--config', str(path / 'config.json')], env=clean_env, stdout=log, stderr=log)
        try:
            deadline = time.monotonic() + 30
            while True:
                try:
                    with socket.create_connection(('127.0.0.1', ports['smtp']), timeout=1):
                        break
                except OSError:
                    if time.monotonic() > deadline:
                        self.fail('Normal native fixture did not start: ' + (path / 'server.log').read_text())
                    time.sleep(.1)
            context = ssl.create_default_context(cafile=str(cert_path))
            # Connect to loopback but validate the synthetic mail service name.
            context.check_hostname = False
            with smtplib.SMTP('127.0.0.1', ports['smtp'], timeout=15) as smtp:
                smtp.ehlo('client.example.net')
                self.assertNotIn('auth', smtp.esmtp_features)
                self.assertEqual(smtp.mail('sender@example.net')[0], 250)
                self.assertGreaterEqual(smtp.rcpt('recipient@example.net')[0], 500)
            with smtplib.SMTP('127.0.0.1', ports['submission'], timeout=15) as smtp:
                smtp.ehlo('client.example.net')
                self.assertNotIn('auth', smtp.esmtp_features)
                self.assertIn('starttls', smtp.esmtp_features)
                self.assertGreaterEqual(smtp.mail('accounts@example.com')[0], 500)
                smtp.starttls(context=context)
                smtp.ehlo('client.example.net')
                self.assertIn('auth', smtp.esmtp_features)
                # Single mechanism avoids triggering the server's repeated-login ban.
                auth = base64.b64encode(b'\x00accounts@example.com\x00incorrect-password').decode()
                self.assertEqual(smtp.docmd('AUTH', 'PLAIN ' + auth)[0], 535)
            with smtplib.SMTP_SSL('127.0.0.1', ports['submissions'], context=context, timeout=15) as smtp:
                smtp.ehlo('client.example.net')
                smtp.login('accounts@example.com', secrets['mailbox_passwords']['accounts@example.com'])
                self.assertGreaterEqual(smtp.mail('postmaster@example.com')[0], 500)
                smtp.rset()
                refused = smtp.sendmail('accounts@example.com', ['postmaster@example.org'],
                    'From: accounts@example.com\r\nTo: postmaster@example.org\r\n'
                    'Subject: Isolated native protocol test\r\n\r\nFixture only.\r\n')
                self.assertEqual(refused, {})
            with imaplib.IMAP4_SSL('127.0.0.1', ports['imaps'], ssl_context=context, timeout=15) as personal:
                self.assertEqual(personal.login('person@example.org',
                    'locally-changed-fixture-password-32-characters')[0], 'OK')
            with imaplib.IMAP4_SSL('127.0.0.1', ports['imaps'], ssl_context=context, timeout=15) as imap:
                self.assertEqual(imap.login('postmaster@example.org', secrets['mailbox_passwords']['postmaster@example.org'])[0], 'OK')
                deadline = time.monotonic() + 15
                while True:
                    imap.select('INBOX')
                    typ, messages = imap.search(None, 'ALL')
                    if messages[0]:
                        break
                    if time.monotonic() > deadline:
                        self.fail('Local SMTP message was not delivered to native IMAP mailbox')
                    time.sleep(.2)
                typ, message = imap.fetch(messages[0].split()[-1], '(RFC822)')
                self.assertEqual(typ, 'OK')
                self.assertIn(b'Subject: Isolated native protocol test', message[0][1])
                self.assertIn(b'DKIM-Signature:', message[0][1])
        except BaseException:
            proc.terminate()
            proc.wait(timeout=10)
            raise
        return proc

    def _check_acme_shape(self, client, config, secrets):
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass
            def do_HEAD(self):
                self.send_response(200)
                self.send_header('Replay-Nonce', 'Zml4dHVyZS1ub25jZQ')
                self.end_headers()
            def do_GET(self):
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.end_headers()
                self.wfile.write(json.dumps({name: self.server.base + '/' + name for name in
                                            ('newNonce', 'newAccount', 'newOrder')}).encode())
            def do_POST(self):
                self.rfile.read(int(self.headers['Content-Length']))
                self.send_response(201)
                self.send_header('Location', self.server.base + '/account/fixture')
                self.send_header('Content-Type', 'application/json')
                self.end_headers()
                self.wfile.write(b'{}')
        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        server.base = 'http://127.0.0.1:' + str(server.server_address[1])
        threading.Thread(target=server.serve_forever, daemon=True).start()
        real_upsert = client.upsert
        def local_acme(kind, identity, desired, **kwargs):
            if kind == 'AcmeProvider':
                identity = dict(identity, directory=server.base + '/directory')
                desired = dict(desired, directory=server.base + '/directory')
            return real_upsert(kind, identity, desired, **kwargs)
        try:
            with patch.object(client, 'upsert', side_effect=local_acme):
                mail._configure_certificate(client, config, secrets)
                before = client.objects('AcmeProvider')
                mail._configure_certificate(client, config, secrets)
                self.assertEqual(client.objects('AcmeProvider'), before)
            host = next(d for d in client.objects('Domain') if d['name'] == config['hostname'])
            self.assertEqual(host['dnsManagement']['publishRecords'], {'dkim': True})
            self.assertEqual(host['certificateManagement']['@type'], 'Automatic')
        finally:
            server.shutdown()
            server.server_close()


if __name__ == '__main__':
    unittest.main()
