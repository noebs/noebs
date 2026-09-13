"""Exercise ingress policies through the pinned Traefik binary.

Run with TRAEFIK_BINARY pointing to Traefik 3.6.13. The test maps the deployed
CRD routing/transport fields to the file provider, then sends actual HTTP over
verified TLS and mTLS upstream connections. No cluster or external service is used.
"""

import contextlib
import http.client
import http.server
import json
import os
from pathlib import Path
import shutil
import socket
import ssl
import subprocess
import tempfile
import threading
import time
import unittest

import yaml


ROOT = Path(__file__).resolve().parents[2]
INGRESS = ROOT / 'infra/kubernetes/ingress'
TRAEFIK = os.environ.get('TRAEFIK_BINARY') or shutil.which('traefik')


class IngressConfigurationTests(unittest.TestCase):
    def test_listener_and_discovery_boundary(self):
        chart = yaml.safe_load((INGRESS / 'traefik-config.yaml').read_text())
        values = yaml.safe_load(chart['spec']['valuesContent'])
        self.assertEqual(chart['metadata'], {'name': 'traefik', 'namespace': 'kube-system'})
        self.assertTrue(values['hostNetwork'])
        self.assertEqual(values['deployment']['dnsPolicy'], 'ClusterFirstWithHostNet')
        self.assertEqual(values['nodeSelector']['kubernetes.io/hostname'], 'noebs-workers')
        self.assertEqual(values['ports']['web']['port'], 8081)
        self.assertEqual(values['ports']['web']['forwardedHeaders'], {'trustedIPs': [], 'insecure': False})
        self.assertEqual(values['ports']['traefik']['hostIP'], '127.0.0.1')
        self.assertFalse(values['service']['enabled'])
        self.assertFalse(values['api']['dashboard'])
        self.assertFalse(values['providers']['kubernetesIngress']['enabled'])
        self.assertFalse(values['providers']['kubernetesGateway']['enabled'])
        self.assertTrue(values['rbac']['namespaced'])
        crd = values['providers']['kubernetesCRD']
        self.assertEqual(crd['namespaces'], ['noebs'])
        self.assertFalse(crd['allowCrossNamespace'])
        self.assertFalse(crd['allowExternalNameServices'])
        self.assertEqual(values['ports']['backoffice']['hostIP'], '127.0.0.1')
        self.assertEqual(values['ports']['backoffice']['port'], 8082)
        self.assertEqual(values['ports']['backoffice']['forwardedHeaders'], {'trustedIPs': ['127.0.0.1/32'], 'insecure': False})
        self.assertEqual(values['additionalArguments'], [
            '--entrypoints.web.http.encodedcharacters.allowencodedpercent=false',
            '--entrypoints.backoffice.http.encodedcharacters.allowencodedpercent=false',
            '--entrypoints.backoffice.http.encodedcharacters.allowencodedslash=false',
            '--entrypoints.backoffice.http.encodedcharacters.allowencodedbackslash=false',
            '--entrypoints.backoffice.http.encodedcharacters.allowencodednullcharacter=false',
            '--entrypoints.web.http.encodedcharacters.allowencodedslash=false',
            '--entrypoints.web.http.encodedcharacters.allowencodedbackslash=false',
            '--entrypoints.web.http.encodedcharacters.allowencodednullcharacter=false'])

    def test_access_logs_cannot_contain_credentials(self):
        chart = yaml.safe_load((INGRESS / 'traefik-config.yaml').read_text())
        fields = yaml.safe_load(chart['spec']['valuesContent'])['logs']['access']['fields']
        self.assertEqual(fields['general']['defaultmode'], 'drop')
        self.assertEqual(fields['headers'], {'defaultmode': 'drop'})
        self.assertEqual(set(fields['general']['names']), {
            'StartUTC', 'Duration', 'DownstreamStatus', 'RequestMethod',
            'RequestHost', 'RouterName', 'ServiceName'})

    def test_service_tls_and_host_are_explicit(self):
        routes = yaml.safe_load((INGRESS / 'routes.yaml').read_text())['spec']['routes']
        transports = {obj['metadata']['name']: obj['spec'] for obj in
                      yaml.safe_load_all((INGRESS / 'transport.yaml').read_text())}
        for route in routes:
            self.assertEqual(len(route['services']), 1)
            service = route['services'][0]
            middlewares = [{'name': 'public-headers'}]
            if service['name'] == 'keycloak':
                middlewares.append({'name': 'keycloak-source'})
            middlewares.append({'name': 'compress'})
            self.assertEqual(route['middlewares'], middlewares)
            self.assertTrue(service['passHostHeader'])
            self.assertEqual(service['scheme'], 'https')
            transport = transports[service['serversTransport']]
            self.assertFalse(transport['insecureSkipVerify'])
            self.assertEqual(transport['serverName'], service['name'] + '.noebs.svc.cluster.local')
            self.assertEqual(transport['rootCAsSecrets'], ['edge-internal-transport'])
        self.assertEqual(transports['api-gateway']['certificatesSecrets'], ['edge-internal-transport'])


class Upstream(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def respond(self):
        payload = json.dumps({'service': self.server.service, 'headers': dict(self.headers),
                              'path': self.path}).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(payload)))
        self.end_headers()
        if self.command != 'HEAD':
            self.wfile.write(payload)

    do_GET = do_HEAD = do_POST = do_PUT = do_PATCH = do_DELETE = do_OPTIONS = do_TRACE = respond


def unused_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


@unittest.skipUnless(TRAEFIK, 'set TRAEFIK_BINARY to run actual ingress transport tests')
class IngressBehaviorTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory(prefix='noebs-ingress-test-')
        cls.path = Path(cls.temp.name)
        cls.servers = {}
        version = subprocess.check_output([TRAEFIK, 'version'], text=True)
        if '3.6.13' not in version:
            raise RuntimeError('ingress tests require the k3s-bundled Traefik 3.6.13')
        cls.openssl('req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256',
                    '-nodes', '-keyout', 'ca.key', '-out', 'ca.crt', '-subj', '/CN=Noebs Test CA', '-days', '1')
        for name, usage in [('api-gateway', 'serverAuth'), ('keycloak', 'serverAuth'), ('edge', 'clientAuth')]:
            cls.openssl('req', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256',
                        '-nodes', '-keyout', name + '.key', '-out', name + '.csr', '-subj', '/CN=' + name)
            (cls.path / (name + '.ext')).write_text(
                f'subjectAltName=DNS:{name}.noebs.svc.cluster.local\nextendedKeyUsage={usage}\n')
            cls.openssl('x509', '-req', '-in', name + '.csr', '-CA', 'ca.crt', '-CAkey', 'ca.key',
                        '-CAcreateserial', '-days', '1', '-extfile', name + '.ext', '-out', name + '.crt')
        for name in ['api-gateway', 'keycloak']:
            server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Upstream)
            server.service = name
            ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            ctx.load_cert_chain(cls.path / (name + '.crt'), cls.path / (name + '.key'))
            if name == 'api-gateway':
                ctx.verify_mode = ssl.CERT_REQUIRED
                ctx.load_verify_locations(cls.path / 'ca.crt')
            server.socket = ctx.wrap_socket(server.socket, server_side=True)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            cls.servers[name] = server

    @classmethod
    def tearDownClass(cls):
        for server in cls.servers.values():
            server.shutdown()
            server.server_close()
        cls.temp.cleanup()

    @classmethod
    def openssl(cls, *args):
        subprocess.run(['openssl', *args], cwd=cls.path, check=True, stdout=subprocess.DEVNULL,
                       stderr=subprocess.DEVNULL)

    @contextlib.contextmanager
    def proxy(self, trusted_ips=(), bad_transport=None, private=False):
        transports = {}
        for obj in yaml.safe_load_all((INGRESS / 'transport.yaml').read_text()):
            transport = obj['spec']
            value = {key: item for key, item in transport.items()
                     if key not in ['rootCAsSecrets', 'certificatesSecrets']}
            value['rootCAs'] = [str(self.path / 'ca.crt')]
            if 'certificatesSecrets' in transport:
                value['certificates'] = [{'certFile': str(self.path / 'edge.crt'),
                                          'keyFile': str(self.path / 'edge.key')}]
            transports[obj['metadata']['name']] = value
        if bad_transport == 'no client certificate':
            del transports['api-gateway']['certificates']
        if bad_transport == 'wrong server name':
            transports['api-gateway']['serverName'] = 'wrong.noebs.svc.cluster.local'
        if bad_transport == 'unknown CA':
            transports['api-gateway']['rootCAs'] = [str(self.path / 'edge.crt')]
        services = {name: {'loadBalancer': {'servers': [{'url': f'https://127.0.0.1:{server.server_port}'}],
                                          'passHostHeader': True, 'serversTransport': name}}
                    for name, server in self.servers.items()}
        routes = yaml.safe_load((INGRESS / 'routes.yaml').read_text())['spec']['routes']
        private_objects = list(yaml.safe_load_all((INGRESS / 'backoffice.yaml').read_text()))
        if private:
            routes = private_objects[0]['spec']['routes']
        routers = {f'route-{index}': {'rule': route['match'], 'entryPoints': ['web'],
                                     'priority': route['priority'],
                                     'middlewares': [item['name'] for item in route['middlewares']],
                                     'service': route['services'][0]['name']}
                   for index, route in enumerate(routes)}
        middlewares = {obj['metadata']['name']: obj['spec'] for obj in
                       yaml.safe_load_all((INGRESS / 'headers.yaml').read_text())}
        middlewares[private_objects[1]['metadata']['name']] = private_objects[1]['spec']
        dynamic = self.path / 'dynamic.yaml'
        dynamic.write_text(yaml.safe_dump({'http': {'routers': routers, 'services': services,
                                                   'middlewares': middlewares,
                                                   'serversTransports': transports}}))
        port = unused_port()
        static = self.path / 'traefik.yaml'
        static.write_text(yaml.safe_dump({'entryPoints': {'web': {'address': f'127.0.0.1:{port}',
                                          'forwardedHeaders': {'trustedIPs': list(trusted_ips), 'insecure': False},
                                          'http': {'encodedCharacters': {'allowEncodedSlash': False,
                                                    'allowEncodedBackSlash': False, 'allowEncodedNullCharacter': False, 'allowEncodedPercent': False}}}},
                                         'providers': {'file': {'filename': str(dynamic)}},
                                         'log': {'level': 'ERROR'}}))
        with (self.path / 'traefik.log').open('w+') as log:
            process = subprocess.Popen([TRAEFIK, '--configFile=' + str(static)], stdout=log, stderr=log)
            try:
                for _ in range(100):
                    if process.poll() is not None:
                        log.seek(0)
                        self.fail(log.read())
                    try:
                        status, _, _ = self.request(port, 'GET', '/backoffice/login' if private else '/test', {'Host': 'noebs-workers.tail09832.ts.net'} if private else None)
                        if status != 404:
                            break
                    except OSError:
                        pass
                    time.sleep(0.02)
                else:
                    self.fail('Traefik did not load ingress routes')
                yield port
            finally:
                process.terminate()
                process.wait(timeout=10)

    def request(self, port, method, path, headers=None, extra_headers=()):
        conn = http.client.HTTPConnection('127.0.0.1', port, timeout=3)
        try:
            request_headers = {'Host': 'api.noebs.sd'}
            request_headers.update(headers or {})
            conn.putrequest(method, path, skip_host=True, skip_accept_encoding=True)
            for name, value in list(request_headers.items()) + list(extra_headers):
                conn.putheader(name, value)
            conn.endheaders()
            response = conn.getresponse()
            body = response.read()
            return response.status, dict(response.headers), json.loads(body) if body.startswith(b'{') else body
        finally:
            conn.close()

    def test_exact_keycloak_method_and_path_allowlist(self):
        metadata = ['.well-known/openid-configuration', 'protocol/openid-connect/certs']
        gets = ['protocol/openid-connect/userinfo', 'protocol/openid-connect/auth',
                'protocol/openid-connect/logout', 'login-actions/authenticate',
                'login-actions/registration', 'login-actions/reset-credentials',
                'login-actions/required-action', 'login-actions/restart', 'login-actions/action-token',
                'login-actions/first-broker-login', 'login-actions/post-broker-login',
                'broker/google/login', 'broker/google/endpoint',
                'broker/customer-idp/login', 'broker/customer-idp/endpoint',
                'broker/after-first-broker-login', 'broker/after-post-broker-login']
        posts = ['protocol/openid-connect/token', 'login-actions/authenticate',
                 'login-actions/registration', 'login-actions/reset-credentials',
                 'login-actions/required-action', 'login-actions/first-broker-login',
                 'login-actions/post-broker-login', 'broker/after-post-broker-login']
        allow = {('/auth/realms/noebs/' + path, method) for paths, methods in [
            (metadata, ['GET', 'HEAD']), (gets, ['GET']), (posts, ['POST'])]
                 for path in paths for method in methods}
        allow |= {('/auth/resources/noebs/login/theme.css', method) for method in ['GET', 'HEAD']}
        paths = {path for path, _ in allow} | {
            '/auth', '/auth/', '/auth/admin/', '/auth/realms/master/.well-known/openid-configuration',
            '/auth/realms/noebs/account', '/auth/realms/noebs/protocol/openid-connect/token/introspect',
            '/auth/realms/noebs/protocol/openid-connect/revoke', '/auth/realms/noebs/protocol/openid-connect/auth/extra',
            '/auth/realms/noebs/broker/UPPER/endpoint',
            '/auth/realms/noebs/broker/-invalid/endpoint',
            '/auth/realms/noebs/broker/' + 'a' * 64 + '/endpoint',
            '/auth/realms/noebs/broker/customer-idp/endpoint/extra',
            '/auth/realms/noebs/broker/customer-idp/token', '/auth/resources', '/auth%2Fadmin', '/auth//admin'}
        with self.proxy() as port:
            for path in sorted(paths):
                for method in ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS', 'TRACE']:
                    with self.subTest(path=path, method=method):
                        status, _, body = self.request(port, method, path)
                        expected = 200 if (path, method) in allow else 404
                        if path == '/auth%2Fadmin':
                            expected = 400
                        self.assertEqual(status, expected)
                        if status == 200 and method != 'HEAD':
                            self.assertEqual(body['service'], 'keycloak')

    def test_preserves_host_and_sets_public_https_headers(self):
        with self.proxy() as port:
            for path in ['/test', '/auth/realms/noebs/protocol/openid-connect/auth']:
                status, headers, body = self.request(port, 'GET', path, {
                    'X-Forwarded-Host': 'attacker.example', 'X-Forwarded-Proto': 'http', 'X-Forwarded-Port': '8080'})
                self.assertEqual(status, 200)
                self.assertEqual(body['headers']['Host'], 'api.noebs.sd')
                self.assertEqual(body['headers']['X-Forwarded-Host'], 'api.noebs.sd')
                self.assertEqual(body['headers']['X-Forwarded-Proto'], 'https')
                self.assertEqual(body['headers']['X-Forwarded-Port'], '443')
                self.assertEqual(headers['X-Content-Type-Options'], 'nosniff')
                self.assertEqual(headers['Strict-Transport-Security'], 'max-age=31536000; includeSubDomains')
            self.assertEqual(self.request(port, 'GET', '/test', {'Host': 'other.example'})[0], 404)

    def test_untrusted_peer_cannot_spoof_source_headers(self):
        with self.proxy() as port:
            status, _, body = self.request(port, 'GET', '/test', {
                'X-Real-IP': '203.0.113.99', 'X-Forwarded-For': '203.0.113.99, 198.51.100.1'})
            self.assertEqual(status, 200)
            self.assertNotIn('X-Real-Ip', body['headers'])
            self.assertEqual(body['headers']['X-Forwarded-For'], '127.0.0.1')

    def test_explicit_trusted_proxy_can_report_client_ip(self):
        with self.proxy(['127.0.0.1/32']) as port:
            status, _, body = self.request(port, 'GET', '/test', {
                'X-Real-IP': '198.51.100.71', 'X-Forwarded-For': '198.51.100.73',
                'X-Forwarded-Host': 'attacker.example'}, extra_headers=[
                    ('X-Forwarded-For', '203.0.113.9, 203.0.113.9'), ('X-Real-IP', '198.51.100.72')])
            self.assertEqual(status, 200)
            self.assertNotIn('X-Real-Ip', body['headers'])
            self.assertEqual(body['headers']['X-Forwarded-For'], '198.51.100.73, 203.0.113.9, 203.0.113.9, 127.0.0.1')
            self.assertEqual(body['headers']['X-Forwarded-Host'], 'api.noebs.sd')

    def test_invalid_upstream_tls_and_missing_client_identity_fail_closed(self):
        for failure in ['no client certificate', 'wrong server name', 'unknown CA']:
            with self.subTest(failure=failure), self.proxy(bad_transport=failure) as port:
                expected = 502 if failure == 'no client certificate' else 500
                self.assertEqual(self.request(port, 'GET', '/test')[0], expected)

    def test_backoffice_public_denial_including_encoded_paths_and_methods(self):
        paths = ['/backoffice', '/backoffice/', '/backoffice/login', '/backoffice/oauth/callback',
                 '/backoffice/assets/style.css', '/BACKOFFICE/login', '/back%6fffice/login',
                 '//backoffice/login', '/x/../backoffice/login', '/backoffice%2flogin',
                 '/backoffice%5clogin', '/backoffice%00/login', '/%2562ackoffice/login']
        with self.proxy() as port:
            for path in paths:
                for method in ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS', 'TRACE']:
                    with self.subTest(path=path, method=method):
                        status, headers, _ = self.request(port, method, path)
                        self.assertIn(status, [400, 404])
                        self.assertNotIn('Set-Cookie', headers)
            self.assertEqual(self.request(port, 'GET', '/backoffice/login',
                                          {'Host': 'noebs-workers.tail09832.ts.net'})[0], 404)
            self.assertEqual(self.request(port, 'GET', '/account/login')[0], 200)

    def test_private_backoffice_preserves_host_and_authenticated_tailnet_source(self):
        private_host = 'noebs-workers.tail09832.ts.net'
        with self.proxy(['127.0.0.1/32'], private=True) as port:
            status, _, body = self.request(port, 'GET', '/backoffice/login', {
                'Host': private_host, 'X-Forwarded-For': '100.85.8.9',
                'X-Forwarded-Host': 'attacker.example', 'X-Forwarded-Proto': 'http',
                'X-Real-IP': 'attacker.example'})
            self.assertEqual(status, 200)
            self.assertEqual(body['headers']['Host'], private_host)
            self.assertEqual(body['headers']['X-Forwarded-Host'], private_host)
            self.assertEqual(body['headers']['X-Forwarded-Proto'], 'https')
            self.assertEqual(body['headers']['X-Forwarded-For'], '100.85.8.9, 127.0.0.1')
            self.assertNotIn('X-Real-Ip', body['headers'])
            for path in ['/test', '/account/login', '/auth/realms/noebs/protocol/openid-connect/auth']:
                self.assertEqual(self.request(port, 'GET', path, {'Host': private_host})[0], 404)
            self.assertEqual(self.request(port, 'GET', '/backoffice/login')[0], 404)

    def test_keycloak_discards_caller_source_chain_and_preserves_pkce_query(self):
        path = '/auth/realms/noebs/protocol/openid-connect/auth?state=opaque&code_challenge=pkce&code_challenge_method=S256'
        with self.proxy(['127.0.0.1/32']) as port:
            status, _, body = self.request(port, 'GET', path, {
                'X-Real-IP': '198.51.100.71', 'X-Forwarded-For': '198.51.100.73, 203.0.113.9, 203.0.113.9'})
            self.assertEqual(status, 200)
            self.assertEqual(body['path'], path)
            self.assertNotIn('X-Forwarded-For', body['headers'])
            self.assertNotIn('X-Real-Ip', body['headers'])


if __name__ == '__main__':
    unittest.main()
