import json
from subprocess import CompletedProcess
import unittest
from unittest.mock import Mock

from private_backoffice import (private_host, reconcile_private_backoffice, serve_configuration,
                                validate_serve, validate_worker)

ORIGIN = 'https://noebs-workers.tail09832.ts.net'
STATUS = {'BackendState': 'Running', 'Self': {'DNSName': 'noebs-workers.tail09832.ts.net.'},
          'CertDomains': ['noebs-workers.tail09832.ts.net'], 'CurrentTailnet': {'MagicDNSEnabled': True}}


def output(value):
    return CompletedProcess([], 0, json.dumps(value).encode())


class PrivateBackofficeTests(unittest.TestCase):
    def test_origin_has_no_public_aliases_paths_ports_or_implicit_defaults(self):
        self.assertEqual(private_host(ORIGIN), 'noebs-workers.tail09832.ts.net')
        for origin in [None, '', 'https://api.noebs.sd', ORIGIN + '/', ORIGIN + ':443',
                       ORIGIN.replace('https:', 'http:'), ORIGIN + '?q=1',
                       'https://user@noebs-workers.tail09832.ts.net', 'https://*.tail09832.ts.net']:
            with self.subTest(origin=origin), self.assertRaises(ValueError):
                private_host(origin)

    def test_serve_is_exact_https_loopback_without_funnel(self):
        wanted = serve_configuration(ORIGIN)
        self.assertEqual(wanted['TCP'], {'443': {'HTTPS': True}})
        self.assertEqual(wanted['Web']['noebs-workers.tail09832.ts.net:443']['Handlers'],
                         {'/': {'Proxy': 'http://127.0.0.1:8082'}})
        validate_serve({}, wanted)
        validate_serve(wanted, wanted, require_enabled=True)
        for candidate in [{}, wanted | {'AllowFunnel': {'noebs-workers.tail09832.ts.net:443': True}},
                          wanted | {'TCP': {'443': {'HTTPS': False}}}, wanted | {'TCP': {'8443': {'HTTPS': True}}},
                          {'Web': {'other.ts.net:443': {'Handlers': {'/': {'Text': 'unrelated'}}}}}]:
            with self.subTest(candidate=candidate), self.assertRaises(ValueError):
                validate_serve(candidate, wanted, require_enabled=True)

    def test_worker_identity_certificate_and_magicdns_must_match(self):
        validate_worker(STATUS, ORIGIN)
        for field, value in [('BackendState', 'NeedsLogin'), ('Self', {'DNSName': 'other.tail09832.ts.net.'}),
                             ('CertDomains', []), ('CurrentTailnet', {'MagicDNSEnabled': False})]:
            with self.subTest(field=field), self.assertRaises(ValueError):
                validate_worker(STATUS | {field: value}, ORIGIN)

    def test_reconcile_preserves_unrelated_state_and_checks_lease_before_write(self):
        lease = Mock()
        ssh = Mock(side_effect=[output(STATUS), output({}), output(None), output(serve_configuration(ORIGIN))])
        reconcile_private_backoffice(ORIGIN, 'key', 'worker', lease, ssh)
        self.assertEqual(lease.check.call_count, 3)
        self.assertEqual(ssh.call_args_list[2].args[2], 'sudo tailscale serve --bg --https=443 --yes http://127.0.0.1:8082')
        for existing in [serve_configuration(ORIGIN) | {'AllowFunnel': {'private:443': True}}, {'TCP': {'8443': {'HTTPS': True}}}]:
            ssh = Mock(side_effect=[output(STATUS), output(existing)])
            with self.assertRaises(ValueError):
                reconcile_private_backoffice(ORIGIN, 'key', 'worker', Mock(), ssh)
            self.assertEqual(ssh.call_count, 2)
        ssh = Mock(side_effect=[output(STATUS), output({})])
        lease = Mock(check=Mock(side_effect=[None, RuntimeError('lost')]))
        with self.assertRaises(RuntimeError):
            reconcile_private_backoffice(ORIGIN, 'key', 'worker', lease, ssh)
        self.assertEqual(ssh.call_count, 2)

    def test_existing_exact_serve_has_no_mutation(self):
        desired = serve_configuration(ORIGIN)
        ssh = Mock(side_effect=[output(STATUS), output(desired), output(desired)])
        reconcile_private_backoffice(ORIGIN, 'key', 'worker', Mock(), ssh)
        self.assertTrue(all(not call.args[2].startswith('sudo') for call in ssh.call_args_list))
