import contextlib
import io
import json
import os
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import deploy
from runtime import InvalidMailConfiguration, validate_runtime
from test_runtime import configuration
from test_webmail import TEST_KEY


class MailDeploymentTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.receipt = Path(self.directory.name) / 'receipt.json'

    def tearDown(self):
        self.directory.cleanup()

    def module_contents(self):
        original = Path.read_text
        return patch.object(deploy.Path, 'read_text', lambda path: 'test module' if path.name == 'stalwart_config.py' else original(path))

    def arguments(self, **kwargs):
        return SimpleNamespace(ssh='test-ssh', identity=None, known_hosts=None, receipt=self.receipt, **kwargs)

    def test_ssh_preserves_exact_host_identity_and_passes_no_secrets_in_arguments(self):
        args = self.arguments()
        args.identity = '/private/host-key'
        args.known_hosts = '/private/known_hosts'
        command = deploy.ssh_command(args, validate_runtime(configuration()))
        self.assertIn('StrictHostKeyChecking=yes', command)
        self.assertIn('UserKnownHostsFile=/private/known_hosts', command)
        self.assertIn('IdentitiesOnly=yes', command)
        self.assertEqual(command[-2], 'adonese@vmi2993153.tail09832.ts.net')
        self.assertTrue(command[-1].startswith('sudo -n python3 -c '))

    def test_secret_decryption_requires_private_identity_and_hides_failures(self):
        with tempfile.TemporaryDirectory() as directory:
            identity = Path(directory) / 'identity'
            identity.write_text('test-only')
            identity.chmod(0o644)
            with patch.object(deploy.subprocess, 'run') as run:
                with self.assertRaises(InvalidMailConfiguration):
                    deploy.load_secrets(Path('mail.secrets.yaml'), identity)
                run.assert_not_called()
            identity.chmod(0o600)
            result = SimpleNamespace(returncode=1, stdout=b'private-value', stderr=b'private-value')
            with patch.object(deploy.subprocess, 'run', return_value=result):
                with self.assertRaises(InvalidMailConfiguration) as failure:
                    deploy.load_secrets(Path('mail.secrets.yaml'), identity)
                self.assertNotIn('private-value', str(failure.exception))

    def test_apply_keeps_credentials_in_ssh_stdin_only(self):
        secrets = {'admin_password': 'test-secret', 'webmail_des_key': TEST_KEY}
        result = SimpleNamespace(returncode=0, stdout=b'{"status":"applied","mail":{"dkim_records":[]}}')
        with self.module_contents(), \
             patch.object(deploy.subprocess, 'run', return_value=result) as run, \
             contextlib.redirect_stdout(io.StringIO()) as output:
            deploy.apply(self.arguments(), configuration(), secrets)
        command = run.call_args.args[0]
        payload = json.loads(run.call_args.kwargs['input'])
        self.assertNotIn('test-secret', repr(command))
        self.assertEqual(payload['secrets'], secrets)
        self.assertNotIn('test-secret', output.getvalue())
        self.assertNotIn('test-secret', self.receipt.read_text())
        self.assertNotIn(TEST_KEY, repr(command))
        self.assertNotIn(TEST_KEY, self.receipt.read_text())
        self.assertIn("$config['smtp_user'] = '%u'", payload['webmail_php'])
        self.assertEqual(json.loads(self.receipt.read_text())['native'], {'dkim_records': []})
        self.assertEqual(self.receipt.stat().st_mode & 0o777, 0o600)

    def test_failed_remote_native_call_does_not_echo_payload_or_exception(self):
        result = SimpleNamespace(returncode=1, stdout=b'private-value', stderr=b'private-value')
        with self.module_contents(), \
             patch.object(deploy.subprocess, 'run', return_value=result):
            with self.assertRaises(deploy.MailDeploymentError) as failure:
                deploy.apply(self.arguments(), configuration(), {'admin_password': 'private-value', 'webmail_des_key': TEST_KEY})
        self.assertNotIn('private-value', str(failure.exception))

    def test_unknown_receipt_stops_before_remote_apply(self):
        self.receipt.write_text('unrelated operator data')
        self.receipt.chmod(0o600)
        with patch.object(deploy.subprocess, 'run') as remote:
            with self.assertRaises(InvalidMailConfiguration):
                deploy.apply(self.arguments(), configuration(), {'admin_password': 'private-value'})
            remote.assert_not_called()
        self.assertEqual(self.receipt.read_text(), 'unrelated operator data')

    def test_own_receipt_can_update_but_different_host_cannot_replace_it(self):
        config = configuration()
        deploy.write_receipt(self.receipt, config, {'dkim_records': []})
        deploy.write_receipt(self.receipt, config, {'domains': 3})
        self.assertEqual(json.loads(self.receipt.read_text())['native'], {'domains': 3})
        config['hostname'] = 'different.example.com'
        with self.assertRaises(InvalidMailConfiguration):
            deploy.write_receipt(self.receipt, config, {})

    def test_backup_never_overwrites_existing_incoming_archive(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'mail.tar.age'
            incoming = output.with_name(output.name + '.incoming')
            incoming.write_bytes(b'previous operation')
            args = self.arguments(output=output, recipient='age1jmq9nl0haxetduys37jfj7ha93vee9pd7me7mtyw5ztm4n8flyrse3mwum')
            with patch.object(deploy.shutil, 'which', return_value='/test/age'), \
                 patch.object(deploy.subprocess, 'Popen') as remote:
                with self.assertRaises(FileExistsError):
                    deploy.backup(args, configuration())
                remote.assert_not_called()
            self.assertEqual(incoming.read_bytes(), b'previous operation')

    def test_backup_invalid_recipient_stops_before_remote_mutation(self):
        args = self.arguments(output=Path('/private/mail.tar.age'), recipient='')
        with patch.object(deploy.subprocess, 'Popen') as remote:
            with self.assertRaises(InvalidMailConfiguration):
                deploy.backup(args, configuration())
            remote.assert_not_called()


if __name__ == '__main__':
    unittest.main()
