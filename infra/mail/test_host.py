import contextlib
import fcntl
import io
import json
import os
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import host
from runtime import render_compose
from test_runtime import configuration
from test_webmail import TEST_KEY
import webmail


NATIVE_FIXTURE = '''
def validate(config, secrets):
    assert secrets['admin_password'] == 'test-secret'

def render_startup(config, secrets):
    return '{"storage":"test"}\\n'

def reconcile(config, secrets, base_url):
    assert base_url == 'http://127.0.0.1:18080'
    return {'domains': len(config['domains'])}
'''


class MailHostTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.root = Path(self.directory.name)
        self.previous_umask = os.umask(0o077)

    def tearDown(self):
        os.umask(self.previous_umask)
        self.directory.cleanup()

    def payload(self, native=NATIVE_FIXTURE):
        config = configuration()
        config['state_directory'] = str(self.root)
        return {'action': 'apply', 'config': config, 'secrets': {'admin_password': 'test-secret', 'webmail_des_key': TEST_KEY},
                'native_module': native, 'normal_compose': render_compose(config),
                'recovery_compose': render_compose(config, recovery=True),
                'webmail_php': webmail.render_php(config), 'webmail_php_ini': webmail.render_php_ini()}

    @contextlib.contextmanager
    def prerequisites(self):
        config = configuration()
        addresses = [{'ifname': 'eth0', 'addr_info': [{'local': config['public_ipv4']}, {'local': config['tailscale_ipv4']}]}]
        def result(command):
            return SimpleNamespace(stdout=json.dumps(addresses).encode() if command[0] == 'ip' else b'')
        with patch.object(host.os, 'geteuid', return_value=0), \
             patch.object(host.platform, 'system', return_value='Linux'), \
             patch.object(host.platform, 'machine', return_value='x86_64'), \
             patch.object(host.shutil, 'which', return_value='/test/tool'), \
             patch.object(host, 'run', side_effect=result), patch.object(host.socket, 'socket') as sockets:
            yield config, sockets

    def test_unowned_firewall_unit_fails_before_deployment_mutation(self):
        unit = self.root / 'unrelated.service'
        unit.write_text('[Unit]\nDescription=An unrelated service\n')
        with patch.object(host, 'FIREWALL_UNIT', unit), patch.object(host, 'run') as run, \
             patch.object(host, 'write_private') as write:
            with self.assertRaisesRegex(host.MailMaintenanceError, 'another service'):
                host.install_firewall(self.payload(), self.root)
            run.assert_not_called()
            write.assert_not_called()
        self.assertEqual(unit.read_text(), '[Unit]\nDescription=An unrelated service\n')

    def test_firewall_unit_symlinks_are_not_adopted(self):
        target = self.root / 'target'
        target.write_text(host.FIREWALL_DESCRIPTION + '\n')
        unit = self.root / 'unit'
        unit.symlink_to(target)
        with self.assertRaises(host.MailMaintenanceError):
            host.check_firewall_unit(unit)

    def test_firewall_reload_stops_old_configuration_before_replacing_it(self):
        unit = self.root / 'mail.service'
        unit.write_text('[Unit]\n' + host.FIREWALL_DESCRIPTION + '\n')
        script = self.root / 'firewall.py'
        script.write_text('old implementation')
        payload = self.payload()
        payload['firewall_module'] = 'new implementation'
        events = []
        def run(command):
            events.append(command)
            if 'stop' in command:
                self.assertEqual(script.read_text(), 'old implementation')
            if 'enable' in command:
                self.assertEqual(script.read_text(), 'new implementation')
        with patch.object(host, 'FIREWALL_UNIT', unit), patch.object(host, 'run', side_effect=run):
            host.install_firewall(payload, self.root)
        self.assertEqual(events, [
            ['systemctl', 'stop', 'noebs-mail-firewall.service'],
            ['systemctl', 'daemon-reload'],
            ['systemctl', 'enable', '--now', 'noebs-mail-firewall.service']])
        text = unit.read_text()
        self.assertIn('PartOf=docker.service', text)
        self.assertIn('WantedBy=docker.service', text)
        self.assertIn('After=docker.service network-online.target noebs-public-docker-firewall.service', text)
        self.assertIn(' apply --interface eth0 --ipv4 213.199.63.78', text)
        self.assertIn(' remove --interface eth0 --ipv4 213.199.63.78', text)

    def test_unrelated_listener_fails_before_creating_state(self):
        with self.prerequisites() as (config, sockets):
            sockets.return_value.__enter__.return_value.bind.side_effect = OSError('address in use')
            with self.assertRaisesRegex(host.MailMaintenanceError, 'another service'):
                host.preflight(config, self.root)
        self.assertEqual(list(self.root.iterdir()), [])

    def test_unmanaged_directory_is_never_adopted(self):
        (self.root / 'existing-owner-data').write_text('preserve')
        with self.prerequisites() as (config, _):
            with self.assertRaisesRegex(host.MailMaintenanceError, 'unmanaged mail directory'):
                host.preflight(config, self.root)
        self.assertEqual((self.root / 'existing-owner-data').read_text(), 'preserve')

    def test_missing_applied_database_requires_restore(self):
        (self.root / '.managed-by-noebs').write_text('noebs.mail/v1\n')
        (self.root / 'deployment.json').write_text('{}')
        (self.root / 'config').mkdir()
        (self.root / 'config/config.json').write_text('{}')
        (self.root / 'data').mkdir()
        with self.prerequisites() as (config, _):
            with self.assertRaisesRegex(host.MailMaintenanceError, 'restore its encrypted backup'):
                host.preflight(config, self.root)

    def test_apply_enters_private_recovery_before_native_configuration_and_normal_start(self):
        events = []
        with patch.object(host, 'run', side_effect=lambda command: events.append(command)), \
             patch.object(host, 'wait_for_api'), patch.object(host, 'wait_for_webmail'), \
             patch.object(host.os, 'chown'), patch.object(host, 'install_firewall'), \
             patch.dict(host.sys.modules):
            summary = host.apply(self.payload(), self.root)
        self.assertEqual(summary, {'domains': 3})
        starts = [command for command in events if 'up' in command]
        self.assertEqual(len(starts), 3)
        self.assertIn('recovery.json', starts[0][5])
        self.assertEqual(starts[1][5], str(self.root / 'compose.json'))
        self.assertEqual(starts[2][-1], 'webmail')
        self.assertFalse((self.root / 'recovery.env').exists())
        self.assertEqual((self.root / 'config/config.json').stat().st_mode & 0o777, 0o600)
        self.assertEqual(json.loads((self.root / 'deployment.json').read_text())['domains'],
                         ['noebs.sd', 'adonese.sd', '2t.sd'])

    def test_native_failure_never_reopens_public_ports(self):
        fixture = NATIVE_FIXTURE.replace("return {'domains': len(config['domains'])}",
                                         "raise ValueError('test-secret')")
        with patch.object(host, 'run') as run, patch.object(host, 'wait_for_api'), \
             patch.object(host.os, 'chown'), patch.object(host, 'install_firewall'), patch.dict(host.sys.modules):
            with self.assertRaises(ValueError):
                host.apply(self.payload(fixture), self.root)
        starts = [call.args[0] for call in run.call_args_list if 'up' in call.args[0]]
        self.assertEqual(len(starts), 1)
        self.assertIn('recovery.json', starts[0][5])
        self.assertFalse((self.root / 'compose.json').exists())
        self.assertFalse((self.root / 'recovery.env').exists())

    def test_existing_storage_changes_fail_before_container_mutation(self):
        (self.root / 'config').mkdir()
        (self.root / 'config/config.json').write_text('existing storage authority')
        with patch.object(host, 'run') as run, patch.dict(host.sys.modules):
            with self.assertRaisesRegex(host.MailMaintenanceError, 'storage migration'):
                host.apply(self.payload(), self.root)
            run.assert_not_called()
        self.assertEqual((self.root / 'config/config.json').read_text(), 'existing storage authority')

    def test_existing_equivalent_storage_formatting_is_preserved(self):
        (self.root / 'config').mkdir()
        original = '{\n  "storage": "test"\n}'
        (self.root / 'config/config.json').write_text(original)
        with patch.object(host, 'run'), patch.object(host, 'wait_for_api'), patch.object(host, 'wait_for_webmail'), \
             patch.object(host.os, 'chown'), patch.object(host, 'install_firewall'), patch.dict(host.sys.modules):
            host.apply(self.payload(), self.root)
        self.assertEqual((self.root / 'config/config.json').read_text(), original)

    def test_webmail_is_stopped_before_recovery_and_stays_stopped_on_native_failure(self):
        payload = self.payload(NATIVE_FIXTURE.replace("return {'domains': len(config['domains'])}",
                                                     "raise ValueError('test-secret')"))
        (self.root / 'compose.json').write_text(json.dumps(payload['normal_compose']))
        with patch.object(host, 'run') as run, patch.object(host, 'wait_for_api'), \
             patch.object(host, 'wait_for_webmail') as ready, patch.object(host.os, 'chown'), \
             patch.object(host, 'install_firewall'), patch.dict(host.sys.modules):
            with self.assertRaises(ValueError):
                host.apply(payload, self.root)
        commands = [call.args[0] for call in run.call_args_list]
        stopped = next(index for index, command in enumerate(commands) if command[-2:] == ['stop', 'webmail'])
        started = next(index for index, command in enumerate(commands) if 'up' in command)
        self.assertLess(stopped, started)
        self.assertFalse(any('up' in command and command[-1] == 'webmail' for command in commands))
        ready.assert_not_called()

    def test_webmail_key_and_metadata_survive_reapply_and_key_change_fails_before_writes(self):
        payload = self.payload()
        with patch.object(host.os, 'chown'):
            host.prepare_webmail(payload, self.root)
            database = self.root / 'webmail/db/sqlite.db'
            database.write_bytes(b'contacts and preferences')
            original = (self.root / 'webmail/roundcube_des_key').stat().st_ino
            host.prepare_webmail(payload, self.root)
            self.assertEqual(database.read_bytes(), b'contacts and preferences')
            self.assertEqual((self.root / 'webmail/roundcube_des_key').stat().st_ino, original)
            self.assertEqual((self.root / 'webmail/roundcube_des_key').stat().st_mode & 0o777, 0o600)
            payload['secrets']['webmail_des_key'] = 'a' * 24
            with patch.object(host, 'write_private') as write:
                with self.assertRaisesRegex(host.MailMaintenanceError, 'explicit rotation'):
                    host.prepare_webmail(payload, self.root)
                write.assert_not_called()

    def test_webmail_only_adds_its_explicit_loopback_port_to_preflight(self):
        with self.prerequisites() as (config, sockets):
            host.preflight(config, self.root)
        bindings = [call.args[0] for call in sockets.return_value.__enter__.return_value.bind.call_args_list]
        self.assertEqual(bindings[-1], ('127.0.0.1', 18089))
        self.assertEqual(len(bindings), 6)

    def test_existing_webmail_metadata_loss_requires_restore(self):
        (self.root / '.managed-by-noebs').write_text('noebs.mail/v1\n')
        (self.root / 'deployment.json').write_text(json.dumps(configuration()))
        (self.root / 'config').mkdir()
        (self.root / 'config/config.json').write_text('{}')
        (self.root / 'data').mkdir()
        (self.root / 'data/authority').write_text('native data')
        with self.prerequisites() as (config, _):
            with self.assertRaisesRegex(host.MailMaintenanceError, 'webmail metadata is missing'):
                host.preflight(config, self.root)

    def test_two_service_backup_captures_webmail_and_resumes_only_previous_states(self):
        (self.root / 'compose.json').write_text(json.dumps(self.payload()['normal_compose']))
        for webmail_running, stalwart_running in [(True, True), (False, True), (True, False), (False, False)]:
            def command_result(command):
                if command[:2] == ['docker', 'inspect']:
                    running = webmail_running if command[2] == 'webmail-id' else stalwart_running
                    output = json.dumps([{'State': {'Running': running, 'Restarting': False, 'Paused': False}}]).encode()
                else:
                    output = (command[-1] + '-id\n').encode()
                return SimpleNamespace(stdout=output)
            with self.subTest(webmail=webmail_running, stalwart=stalwart_running), \
                 patch.object(host, 'run', side_effect=command_result) as run, \
                 patch.object(host.subprocess, 'run', return_value=SimpleNamespace(returncode=1)) as archive, \
                 patch.object(host.sys, 'stdout', SimpleNamespace(buffer=io.BytesIO())):
                with self.assertRaises(host.MailMaintenanceError):
                    host.backup(self.root)
                commands = [call.args[0] for call in run.call_args_list]
                self.assertEqual([command[-1] for command in commands if 'stop' in command], ['webmail', 'stalwart'])
                self.assertEqual([command[-1] for command in commands if 'start' in command],
                                 [service for service, active in [('stalwart', stalwart_running), ('webmail', webmail_running)] if active])
                self.assertIn('webmail', archive.call_args.args[0])

    def test_failed_archive_resumes_only_the_previously_running_service(self):
        (self.root / 'compose.json').write_text('{"services":{"stalwart":{}}}')
        for running in [True, False]:
            def command_result(command):
                output = json.dumps([{'State': {'Running': running, 'Restarting': False, 'Paused': False}}]).encode() if command[:2] == ['docker', 'inspect'] else b'id\n'
                return SimpleNamespace(stdout=output)
            with self.subTest(running=running), \
                 patch.object(host, 'run', side_effect=command_result) as run, \
                 patch.object(host.subprocess, 'run', return_value=SimpleNamespace(returncode=1)), \
                 patch.object(host.sys, 'stdout', SimpleNamespace(buffer=io.BytesIO())):
                with self.assertRaises(host.MailMaintenanceError):
                    host.backup(self.root)
                commands = [call.args[0] for call in run.call_args_list]
                self.assertTrue(any('stop' in command for command in commands))
                self.assertEqual(any('start' in command for command in commands), running)

    def test_held_host_lease_stops_before_apply(self):
        (self.root / '.managed-by-noebs').write_text('noebs.mail/v1\n')
        with (self.root / 'deploy.lock').open('a') as lock:
            fcntl.flock(lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
            with patch.object(host.sys, 'stdin', io.StringIO(json.dumps(self.payload()))), \
                 patch.object(host, 'preflight'), patch.object(host, 'apply') as apply:
                with self.assertRaisesRegex(host.MailMaintenanceError, 'host lease'):
                    host.main()
                apply.assert_not_called()

    def test_backup_of_missing_state_does_not_create_runtime(self):
        config = configuration()
        root = self.root / 'missing'
        config['state_directory'] = str(root)
        with patch.object(host.sys, 'stdin', io.StringIO(json.dumps({'action': 'backup', 'config': config}))), \
             patch.object(host, 'preflight') as preflight:
            with self.assertRaisesRegex(host.MailMaintenanceError, 'existing managed runtime'):
                host.main()
            preflight.assert_not_called()
        self.assertFalse(root.exists())


if __name__ == '__main__':
    unittest.main()
