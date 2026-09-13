import copy
import os
from pathlib import Path
import unittest
from unittest.mock import patch

import yaml

from runtime import InvalidMailConfiguration, RUNTIME_FIELDS, render_compose, validate_runtime


def configuration():
    return yaml.safe_load(Path(__file__).with_name('deployment.yaml.example').read_text())


class RuntimeConfigurationTest(unittest.TestCase):
    def test_missing_runtime_fields_fail_without_defaults(self):
        for field in RUNTIME_FIELDS:
            config = configuration()
            del config[field]
            before = copy.deepcopy(config)
            with self.subTest(field=field), self.assertRaises(InvalidMailConfiguration):
                validate_runtime(config)
            self.assertEqual(config, before)

    def test_invalid_endpoints_paths_images_and_limits_fail_at_boundary(self):
        for field, value in [
            ('api_version', ''), ('ssh_destination', '-oProxyCommand=bad'),
            ('public_interface', ''), ('public_interface', 'eth0;bad'), ('public_interface', None),
            ('ssh_destination', 'mail.example.com'), ('hostname', '${SECRET}'),
            ('public_ipv4', '0.0.0.0'), ('public_ipv4', '127.0.0.1'),
            ('public_ipv4', True), ('public_ipv4', '100.64.0.1'),
            ('tailscale_ipv4', '213.199.63.78'), ('tailscale_ipv4', ''),
            ('image', 'stalwartlabs/stalwart:latest'),
            ('image', 'attacker/stalwart:v0.16.21@sha256:' + 'a' * 64),
            ('state_directory', '/etc'), ('state_directory', '/var/lib/noebs-mail/../other'),
            ('resources', {}), ('resources', {'cpus': True, 'memory_mib': 2048, 'pids': 256}),
            ('resources', {'cpus': 1, 'memory_mib': 0, 'pids': 256}),
            ('resources', {'cpus': 1, 'memory_mib': 2048, 'pids': -1}),
        ]:
            config = configuration()
            config[field] = value
            with self.subTest(field=field, value=value), self.assertRaises(InvalidMailConfiguration):
                validate_runtime(config)

    def test_example_preserves_three_explicit_domains(self):
        config = configuration()
        before = copy.deepcopy(config)
        self.assertEqual(validate_runtime(config), before)
        self.assertEqual(config['domains'], ['noebs.sd', 'adonese.sd', '2t.sd'])

    def test_mail_ports_and_storage_are_isolated_from_existing_web_services(self):
        config = validate_runtime(configuration())
        service = render_compose(config)['services']['stalwart']
        self.assertEqual(service['ports'], [
            '213.199.63.78:25:25', '213.199.63.78:465:465',
            '213.199.63.78:587:587', '213.199.63.78:993:993', '127.0.0.1:18080:8080',
        ])
        self.assertNotIn('network_mode', service)
        self.assertEqual(service['volumes'], [
            '/var/lib/noebs-mail/config:/etc/stalwart', '/var/lib/noebs-mail/data:/var/lib/stalwart',
        ])
        self.assertEqual(service['user'], '2000:2000')
        self.assertEqual(service['mem_limit'], '2048m')
        self.assertEqual(service['cpus'], 1)
        self.assertEqual(service['pids_limit'], 256)
        self.assertEqual(service['cap_drop'], ['ALL'])
        self.assertEqual(service['cap_add'], ['NET_BIND_SERVICE'])

    def test_recovery_exposes_no_public_listener_and_keeps_secret_out_of_compose(self):
        config = validate_runtime(configuration())
        normal = render_compose(config)['services']['stalwart']
        recovery = render_compose(config, recovery=True)['services']['stalwart']
        self.assertEqual(recovery['ports'], ['127.0.0.1:18080:8080'])
        self.assertEqual(recovery['environment']['STALWART_RECOVERY_MODE'], '1')
        self.assertEqual(recovery['env_file'], [{'path': '/var/lib/noebs-mail/recovery.env', 'format': 'raw'}])
        self.assertNotIn('env_file', normal)
        self.assertNotIn('STALWART_RECOVERY_MODE', normal['environment'])
        self.assertNotIn('STALWART_RECOVERY_ADMIN', recovery['environment'])

    def test_ambient_environment_cannot_override_explicit_bindings(self):
        config = validate_runtime(configuration())
        expected = render_compose(config)
        with patch.dict(os.environ, {'MAIL_PUBLIC_IPV4': '0.0.0.0', 'STALWART_IMAGE': 'untrusted:latest'}):
            self.assertEqual(render_compose(config), expected)


if __name__ == '__main__':
    unittest.main()
