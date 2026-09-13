"""Kubernetes connectivity for the application's configured external client."""
import ipaddress
from urllib.parse import urlsplit


def resources(settings):
    if not settings.get('interop_tenant'):
        return []
    # Application configuration validation runs before this renderer.
    port = int(settings['interop_backend_listen_address'].rsplit(':', 1)[1])
    peers = [str(ipaddress.ip_network(peer + ('/128' if ':' in peer else '/32')))
             for peer in settings['interop_backend_allowed_peers']]
    selector = {'app.kubernetes.io/name': 'wallet-worker'}
    egress = []
    for key in ['interop_sdk_outbound_url', 'interop_sdk_inbound_url']:
        endpoint = urlsplit(settings[key])
        remote_port = endpoint.port or (443 if endpoint.scheme == 'https' else 80)
        rule = {'ports': [{'protocol': 'TCP', 'port': remote_port}]}
        try:
            address = ipaddress.ip_address(endpoint.hostname)
        except ValueError:
            # Kubernetes NetworkPolicy has no DNS selectors. A hostname uses
            # port-restricted egress; the client verifies its configured URL.
            pass
        else:
            rule['to'] = [{'ipBlock': {'cidr': str(address) + ('/128' if address.version == 6 else '/32')}}]
        if rule not in egress:
            egress.append(rule)
    return [
        {'apiVersion': 'v1', 'kind': 'Service',
         'metadata': {'name': 'wallet-interop-callback', 'namespace': 'noebs'},
         'spec': {'type': 'NodePort', 'externalTrafficPolicy': 'Local', 'selector': selector,
                  'ports': [{'name': 'callback', 'port': port, 'targetPort': port, 'nodePort': 30402}]}},
        {'apiVersion': 'networking.k8s.io/v1', 'kind': 'NetworkPolicy',
         'metadata': {'name': 'wallet-interop-transport', 'namespace': 'noebs'},
         'spec': {'podSelector': {'matchLabels': selector}, 'policyTypes': ['Ingress', 'Egress'],
                  'ingress': [{'from': [{'ipBlock': {'cidr': peer}} for peer in peers],
                               'ports': [{'protocol': 'TCP', 'port': port}]}], 'egress': egress}},
    ]
