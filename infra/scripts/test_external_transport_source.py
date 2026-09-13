import json
import os
from pathlib import Path
import subprocess
import sys
import unittest
from subprocess import CompletedProcess
from unittest.mock import Mock

from external_transport_source import (CHAIN, callback_tuple, firewall_script,
                                       install_script, reconcile_callback_source, systemd_unit)

SETTINGS = {'interop_tenant': 'noebs', 'interop_backend_allowed_peers': ['100.76.217.90'],
            'interop_backend_listen_address': '0.0.0.0:4002'}
WORKER = '100.85.107.107'


class CallbackSourceTests(unittest.TestCase):
    def test_exact_configuration_and_shell_syntax(self):
        connection = callback_tuple(SETTINGS, WORKER)
        self.assertEqual(connection, ('100.76.217.90', WORKER, 4002))
        for script in [firewall_script(connection), install_script(connection), install_script(None)]:
            subprocess.run(['sh', '-n'], input=script.encode(), check=True)
        script = firewall_script(connection)
        for selector in ['-s 100.76.217.90/32 --dport 4002', '--ctstate DNAT --ctdir ORIGINAL',
                         '--ctorigsrc 100.76.217.90/32 --ctorigdst 100.85.107.107/32 --ctorigdstport 30402',
                         '--mark 0x40000/0xff0000', '--set-xmark 0x0/0xff0000']:
            self.assertIn(selector, script)
        # Inspect rules, not comments: neither global SNAT nor filter policy is changed.
        changes = [line for line in script.splitlines() if 'iptables ' in line and ' -C ' not in line and ' -S ' not in line]
        self.assertTrue(all('-t mangle' in line for line in changes))
        self.assertTrue(all(CHAIN in line for line in changes))

    def test_missing_broad_duplicate_and_invalid_inputs_are_rejected(self):
        for field, values in {
            'interop_backend_allowed_peers': [[], ['100.76.217.90'] * 2, ['0.0.0.0/0'], ['100.76.217.90/32'],
                                              ['::1'], [WORKER], ['8.8.8.8'], '100.76.217.90'],
            'interop_backend_listen_address': [None, 4002, '', ':4002', '127.0.0.1:4002', '0.0.0.0:0', '0.0.0.0:65536',
                                                '0.0.0.0:04002', '0.0.0.0:4002; false'],
        }.items():
            for value in values:
                with self.subTest(field=field, value=value), self.assertRaises(ValueError):
                    callback_tuple(SETTINGS | {field: value}, WORKER)
        for worker in ['', '1.2.3.4', WORKER + '\n100.64.1.2', '100.85.107.107/32', None]:
            with self.subTest(worker=worker), self.assertRaises(ValueError):
                callback_tuple(SETTINGS, worker)

    def test_disabled_removes_only_owned_rules_and_unit_without_tailnet_discovery(self):
        lease = Mock()
        ssh = Mock(return_value=CompletedProcess([], 0, b''))
        reconcile_callback_source({}, 'key', 'worker', lease, ssh)
        self.assertEqual(ssh.call_count, 1)
        self.assertEqual(lease.check.call_count, 3)
        script = ssh.call_args.kwargs['input'].decode()
        self.assertIn('systemctl disable --now noebs-callback-source.service', script)
        self.assertIn('sh -s remove', script)
        self.assertNotIn('systemctl restart', script)
        self.assertNotIn('tailscale ip', script)
        self.assertIsNone(callback_tuple({}, None))

    def test_lease_loss_and_invalid_discovery_prevent_mutation(self):
        for lease in [None, Mock(check=Mock(side_effect=RuntimeError('lost')))]:
            ssh = Mock()
            with self.assertRaises(RuntimeError):
                reconcile_callback_source(SETTINGS, 'key', 'worker', lease, ssh)
            ssh.assert_not_called()
        ssh = Mock(return_value=CompletedProcess([], 0, b'not-an-ip'))
        with self.assertRaises(ValueError):
            reconcile_callback_source(SETTINGS, 'key', 'worker', Mock(), ssh)
        self.assertEqual(ssh.call_count, 1)
        self.assertEqual(ssh.call_args.args[2], 'tailscale ip -4')
        lease = Mock()
        lease.check.side_effect = [None, RuntimeError('lost')]
        ssh = Mock(return_value=CompletedProcess([], 0, WORKER.encode()))
        with self.assertRaises(RuntimeError):
            reconcile_callback_source(SETTINGS, 'key', 'worker', lease, ssh)
        self.assertEqual(ssh.call_count, 1)

    def test_enabled_reconciliation_is_persistent_and_rechecks_identity(self):
        ssh = Mock(return_value=CompletedProcess([], 0, (WORKER + '\n').encode()))
        lease = Mock()
        reconcile_callback_source(SETTINGS, 'key', 'worker', lease, ssh)
        self.assertEqual(lease.check.call_count, 3)
        self.assertEqual(ssh.call_count, 2)
        script = ssh.call_args.kwargs['input'].decode()
        self.assertIn('systemctl enable noebs-callback-source.service', script)
        self.assertIn('Worker tailnet identity changed', script)
        unit = systemd_unit()
        self.assertIn('After=network-online.target tailscaled.service k3s.service k3s-agent.service', unit)
        self.assertIn('ExecStop=/usr/local/sbin/noebs-callback-source remove', unit)
        self.assertIn('PartOf=tailscaled.service', unit)
        self.assertIn('Restart=on-failure', unit)
        self.assertIn('RestartSec=5', unit)
        self.assertIn('StartLimitIntervalSec=120', unit)
        self.assertIn('StartLimitBurst=12', unit)

    @unittest.skipUnless(os.environ.get('NOEBS_TEST_CALLBACK_NETNS') == '1',
                         'requires unprivileged Linux network namespaces and iptables')
    def test_real_kernel_preserves_only_exact_callback_and_removes_rule(self):
        environment = os.environ | {'NOEBS_TEST_ORIGINAL_NETNS': os.readlink('/proc/self/ns/net')}
        result = subprocess.run(['unshare', '-Urn', sys.executable,
                                 str(Path(__file__).with_name('external_transport_source_netns_test.py'))],
                                env=environment, capture_output=True, check=True, timeout=60)
        proof = json.loads(result.stdout)
        self.assertEqual(proof['exact_peer'], '100.76.217.90')
        self.assertEqual(proof['disabled_restores_existing_snat'], '10.123.0.1')
        self.assertTrue(proof['unrelated_kubernetes_mark_preserved'])


if __name__ == '__main__':
    unittest.main()
