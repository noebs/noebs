import unittest

from external_transport import resources


class ExternalTransportTests(unittest.TestCase):
    def test_unconfigured_integration_has_no_cluster_resources(self):
        self.assertEqual(resources({}), [])

    def test_callbacks_preserve_socket_peer_and_egress_targets_configured_endpoint(self):
        service, policy = resources({
            'interop_tenant': 'tenant-bank',
            'interop_sdk_outbound_url': 'http://100.64.0.10:4001',
            'interop_sdk_inbound_url': 'http://100.64.0.10:4000',
            'interop_backend_listen_address': '0.0.0.0:4002',
            'interop_backend_allowed_peers': ['100.64.0.10'],
        })
        self.assertEqual(service['spec']['externalTrafficPolicy'], 'Local')
        self.assertEqual(service['spec']['ports'][0]['nodePort'], 30402)
        self.assertEqual(policy['spec']['ingress'][0]['from'], [{'ipBlock': {'cidr': '100.64.0.10/32'}}])
        self.assertEqual([rule['ports'][0]['port'] for rule in policy['spec']['egress']], [4001, 4000])
        for rule in policy['spec']['egress']:
            self.assertEqual(rule['to'], [{'ipBlock': {'cidr': '100.64.0.10/32'}}])

    def test_dns_endpoint_only_opens_its_configured_port(self):
        _, policy = resources({
            'interop_tenant': 'tenant-bank',
            'interop_sdk_outbound_url': 'https://bank.example',
            'interop_sdk_inbound_url': 'https://bank.example',
            'interop_backend_listen_address': '0.0.0.0:4002',
            'interop_backend_allowed_peers': ['100.64.0.10'],
        })
        self.assertEqual(policy['spec']['egress'], [{'ports': [{'protocol': 'TCP', 'port': 443}]}])
