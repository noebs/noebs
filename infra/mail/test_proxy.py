import copy
import json
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

import proxy


def target():
    return {'namespace': 'edge', 'deployment': 'caddy', 'container': 'caddy',
            'configmap': 'existing-caddy-config', 'config_key': 'Caddyfile',
            'server': 'srv0', 'routes': [{'id': 'noebs-mail-https', 'hostname': 'mail.example.test', 'upstream': '127.0.0.1:18080'}]}


def current_config():
    return {'apps': {'http': {'servers': {'srv0': {
        'listen': [':443'],
        'routes': [{'match': [{'host': [name]}], 'terminal': True,
                    'handle': [{'handler': 'reverse_proxy', 'upstreams': [{'dial': '127.0.0.1:8081'}]}]}
                   for name in ['api.example.test', 'web.example.test', 'other.example.test']],
        'logs': {'logger_names': {'api.example.test': ['api-log']}},
    }}}}, 'logging': {'logs': {'api-log': {'writer': {'output': 'stdout'}}}}}


def deployment():
    return {'spec': {'replicas': 1, 'strategy': {'type': 'Recreate'}, 'template': {'spec': {
        'hostNetwork': True,
        'containers': [{'name': 'caddy', 'args': ['caddy', 'run', '--config', proxy.CONFIG_PATH],
                        'volumeMounts': [{'name': 'config', 'mountPath': proxy.CONFIG_PATH,
                                          'subPath': 'Caddyfile', 'readOnly': True}]}],
        'volumes': [{'name': 'config', 'configMap': {'name': 'existing-caddy-config'}}],
    }}}}


class FakeProxy:
    def __init__(self):
        self.events = []
        self.running = current_config()
        self.stored = {'metadata': {'resourceVersion': '1'},
                       'data': {'Caddyfile': json.dumps(self.running), 'other-key': 'preserve'}}
        self.deployed = deployment()
        self.invalid = False
        self.reload_error = False
        self.reload_timeout_after_apply = False
        self.concurrent_write = False

    def deployment(self):
        return copy.deepcopy(self.deployed)

    def configmap(self):
        return copy.deepcopy(self.stored)

    def live(self):
        return copy.deepcopy(self.running), 'test-etag'

    def validate(self, candidate):
        self.events.append('validate')
        if self.invalid:
            raise proxy.MailProxyError('candidate invalid')

    def persist(self, configmap, expected_text, desired_text):
        self.events.append('persist')
        if (self.concurrent_write or configmap['metadata']['resourceVersion'] != self.stored['metadata']['resourceVersion']
                or self.stored['data']['Caddyfile'] != expected_text):
            raise proxy.MailProxyError('concurrent ConfigMap change')
        self.stored['metadata']['resourceVersion'] = str(int(self.stored['metadata']['resourceVersion']) + 1)
        self.stored['data']['Caddyfile'] = desired_text
        return copy.deepcopy(self.stored)

    def reload(self, candidate, etag):
        self.events.append('reload')
        if etag != 'test-etag':
            raise proxy.MailProxyError('wrong etag')
        if self.reload_error:
            raise proxy.MailProxyError('invalid reload')
        self.running = copy.deepcopy(candidate)
        if self.reload_timeout_after_apply:
            raise proxy.MailProxyError('reload response timed out')

    def restart(self):
        self.events.append('restart')
        self.running = json.loads(self.stored['data']['Caddyfile'])


class MailProxyConfigTests(unittest.TestCase):
    def test_addition_preserves_all_existing_routes_and_settings(self):
        current = current_config()
        original = copy.deepcopy(current)
        desired = proxy.desired_config(current, target())
        routes = desired['apps']['http']['servers']['srv0']['routes']
        self.assertEqual(routes[-1]['@id'], target()['routes'][0]['id'])
        self.assertEqual(routes[-1]['match'], [{'host': ['mail.example.test']}])
        handler = routes[-1]['handle'][0]
        self.assertEqual(handler['upstreams'], [{'dial': '127.0.0.1:18080'}])
        headers = handler['headers']['request']
        self.assertEqual(headers['set']['X-Forwarded-For'], ['{http.request.remote.host}'])
        self.assertEqual(headers['set']['Host'], ['mail.example.test'])
        self.assertEqual(headers['set']['X-Forwarded-Proto'], ['https'])
        self.assertEqual(set(headers['delete']), {'Forwarded', 'X-Real-IP'})
        routes.pop()
        self.assertEqual(desired, original)
        self.assertEqual(current, original, 'the caller configuration was mutated')

    def test_managed_route_is_idempotent_and_its_drift_is_repaired(self):
        desired = proxy.desired_config(current_config(), target())
        self.assertEqual(proxy.desired_config(desired, target()), desired)
        drifted = copy.deepcopy(desired)
        drifted['apps']['http']['servers']['srv0']['routes'][-1]['handle'][0]['upstreams'] = [{'dial': 'wrong:80'}]
        self.assertEqual(proxy.desired_config(drifted, target()), desired)

    def test_second_configured_route_preserves_existing_mail_and_other_sites(self):
        first = proxy.desired_config(current_config(), target())
        both = target()
        both['routes'].append({'id': 'noebs-webmail-https', 'hostname': 'webmail.another.test', 'upstream': '127.0.0.1:18082'})
        desired = proxy.desired_config(first, both)
        self.assertEqual(proxy.desired_config(desired, both), desired)
        routes = desired['apps']['http']['servers']['srv0']['routes']
        webmail = routes[-1]
        self.assertEqual(webmail['match'], [{'host': ['webmail.another.test']}])
        self.assertEqual(webmail['handle'][0]['upstreams'], [{'dial': '127.0.0.1:18082'}])
        routes.pop()
        self.assertEqual(desired, first)

    def test_route_identity_and_endpoint_inputs_are_explicit_and_unique(self):
        for change in [{'id': ''}, {'hostname': ''}, {'upstream': ''}, {'upstream': '192.0.2.1:8080'},
                       {'upstream': '127.0.0.1:0'}, {'upstream': '127.0.0.1:65536'},
                       {'upstream': 'localhost:8080'}, {'upstream': 'http://127.0.0.1:8080'}]:
            invalid = target()
            invalid['routes'][0].update(change)
            with self.subTest(change=change), self.assertRaises(proxy.MailProxyError):
                proxy.desired_config(current_config(), invalid)
        for field in ['id', 'hostname']:
            invalid = target()
            other = {'id': 'other-route', 'hostname': 'other-mail.example.test', 'upstream': '127.0.0.1:18082'}
            other[field] = invalid['routes'][0][field]
            invalid['routes'].append(other)
            with self.subTest(duplicate=field), self.assertRaises(proxy.MailProxyError):
                proxy.desired_config(current_config(), invalid)

    def test_runtime_projection_uses_declared_webmail_and_rendered_stalwart_bindings(self):
        config = {'hostname': 'mail.example.test', 'webmail': {'hostname': 'personal.example.test', 'loopback_port': 28082}}
        compose = {'services': {'stalwart': {'ports': ['192.0.2.10:25:25', '127.0.0.1:28080:8080']}}}
        self.assertEqual(proxy.routes_from_config(config, compose), [
            {'id': 'noebs-mail-https', 'hostname': 'mail.example.test', 'upstream': '127.0.0.1:28080'},
            {'id': 'noebs-webmail-https', 'hostname': 'personal.example.test', 'upstream': '127.0.0.1:28082'},
        ])
        for webmail in [None, {}, {'hostname': 'personal.example.test'}, {'hostname': 'personal.example.test', 'loopback_port': True}]:
            with self.subTest(webmail=webmail), self.assertRaises(proxy.MailProxyError):
                proxy.routes_from_config(config | {'webmail': webmail}, compose)
        for ports in [[], ['0.0.0.0:28080:8080'], ['127.0.0.1:28080:8080', '127.0.0.1:28081:8080']]:
            with self.subTest(ports=ports), self.assertRaises(proxy.MailProxyError):
                proxy.routes_from_config(config, {'services': {'stalwart': {'ports': ports}}})

    def test_hostname_wildcard_unscoped_and_id_collisions_fail(self):
        for case in ['exact host', 'wildcard host', 'unscoped', 'other server', 'duplicate ID', 'nested ID', 'foreign ID']:
            with self.subTest(case=case):
                current = current_config()
                server = current['apps']['http']['servers']['srv0']
                route = copy.deepcopy(server['routes'][0])
                if case == 'exact host':
                    route['match'] = [{'host': ['MAIL.EXAMPLE.TEST']}]
                    server['routes'].append(route)
                elif case == 'wildcard host':
                    route['match'] = [{'host': ['*.example.test']}]
                    server['routes'].append(route)
                elif case == 'unscoped':
                    del route['match']
                    server['routes'].append(route)
                elif case == 'other server':
                    route['match'] = [{'host': ['mail.example.test']}]
                    current['apps']['http']['servers']['other'] = {'listen': [':8443'], 'routes': [route]}
                elif case == 'duplicate ID':
                    server['routes'].extend([proxy.proxy_route(target()['routes'][0]), proxy.proxy_route(target()['routes'][0])])
                elif case == 'nested ID':
                    route['handle'][0]['@id'] = target()['routes'][0]['id']
                    server['routes'].append(route)
                elif case == 'foreign ID':
                    route['@id'] = target()['routes'][0]['id']
                    server['routes'].append(route)
                with self.assertRaises(proxy.MailProxyError):
                    proxy.desired_config(current, target())

    def test_missing_invalid_and_non_https_configuration_is_rejected(self):
        for current in [None, [], {}, {'apps': {}}]:
            with self.assertRaises(proxy.MailProxyError):
                proxy.desired_config(current, target())
        for value in [{'listen': [':80']}, {'routes': 'invalid'}, {'automatic_https': {'disable': True}},
                      {'automatic_https': {'skip_certificates': ['mail.example.test']}}]:
            current = current_config()
            current['apps']['http']['servers']['srv0'].update(value)
            with self.assertRaises(proxy.MailProxyError):
                proxy.desired_config(current, target())
        for invalid in ['', 'mail.example.test/path', '*.example.test', 'mail.example.test:443']:
            with self.assertRaises(proxy.MailProxyError):
                proxy.desired_config(current_config(), target() | {'routes': [target()['routes'][0] | {'hostname': invalid}]})
        with self.assertRaises(proxy.MailProxyError):
            proxy.desired_config(current_config(), target() | {'namespace': ''})


class MailProxyApplyTests(unittest.TestCase):
    def test_check_validates_without_writes(self):
        remote = FakeProxy()
        original = remote.configmap()
        self.assertEqual(proxy.reconcile_proxy('check', target(), remote, False)['status'], 'validated')
        self.assertEqual(remote.events, ['validate'])
        self.assertEqual(remote.stored, original)

    def test_apply_persists_then_gracefully_reloads_and_second_apply_is_empty(self):
        remote = FakeProxy()
        desired = proxy.desired_config(remote.running, target())
        self.assertEqual(proxy.reconcile_proxy('apply', target(), remote, False)['status'], 'applied')
        self.assertEqual(remote.events, ['validate', 'persist', 'reload'])
        self.assertEqual(json.loads(remote.stored['data']['Caddyfile']), desired)
        self.assertEqual(remote.stored['data']['other-key'], 'preserve')
        self.assertEqual(remote.running, desired)
        remote.events = []
        self.assertEqual(proxy.reconcile_proxy('apply', target(), remote, False)['status'], 'unchanged')
        self.assertEqual(remote.events, [])

    def test_failed_validation_drift_or_mount_mismatch_never_writes(self):
        for case in ['validation', 'live drift', 'wrong mount', 'invalid JSON']:
            remote = FakeProxy()
            if case == 'validation':
                remote.invalid = True
            elif case == 'live drift':
                remote.running['unrelated'] = True
            elif case == 'wrong mount':
                remote.deployed['spec']['template']['spec']['volumes'][0]['configMap']['name'] = 'different'
            else:
                remote.stored['data']['Caddyfile'] = 'invalid JSON'
            original = remote.configmap()
            with self.subTest(case=case), self.assertRaises(proxy.MailProxyError):
                proxy.reconcile_proxy('apply', target(), remote, False)
            self.assertNotIn('persist', remote.events)
            self.assertNotIn('reload', remote.events)
            self.assertEqual(remote.stored, original)

    def test_concurrent_configmap_update_prevents_live_reload(self):
        remote = FakeProxy()
        remote.concurrent_write = True
        original = remote.configmap()
        with self.assertRaises(proxy.MailProxyError):
            proxy.reconcile_proxy('apply', target(), remote, False)
        self.assertNotIn('reload', remote.events)
        self.assertEqual(remote.stored, original)

    def test_reload_failure_restores_persisted_configuration(self):
        remote = FakeProxy()
        remote.reload_error = True
        original = remote.configmap()['data']
        with self.assertRaisesRegex(proxy.MailProxyError, 'rolled back'):
            proxy.reconcile_proxy('apply', target(), remote, False)
        self.assertEqual(remote.events, ['validate', 'persist', 'reload', 'persist'])
        self.assertEqual(remote.stored['data'], original)
        self.assertEqual(remote.running, current_config())

    def test_timeout_after_success_does_not_undo_working_configuration(self):
        remote = FakeProxy()
        remote.reload_timeout_after_apply = True
        self.assertEqual(proxy.reconcile_proxy('apply', target(), remote, False)['status'], 'applied')
        self.assertEqual(remote.events, ['validate', 'persist', 'reload'])
        self.assertEqual(json.loads(remote.stored['data']['Caddyfile']), remote.running)

    def test_explicit_restart_refreshes_mount_after_reload_and_checks_all_routes(self):
        remote = FakeProxy()
        desired = proxy.desired_config(remote.running, target())
        result = proxy.reconcile_proxy('apply', target(), remote, True)
        self.assertTrue(result['proxy_restarted'])
        self.assertEqual(remote.events, ['validate', 'persist', 'reload', 'restart'])
        self.assertEqual(remote.running, desired)
        remote.events = []
        proxy.reconcile_proxy('apply', target(), remote, True)
        self.assertEqual(remote.events, ['restart'])

    def test_restart_failure_or_wrong_configuration_is_not_success(self):
        for case in ['failed restart', 'stale mounted config']:
            remote = FakeProxy()
            def restart():
                if case == 'failed restart':
                    raise proxy.MailProxyError('restart failed')
                remote.running = current_config()
            remote.restart = restart
            with self.subTest(case=case), self.assertRaises(proxy.MailProxyError):
                proxy.reconcile_proxy('apply', target(), remote, True)

    def test_check_does_not_restart_and_unsupported_strategy_stops_before_writes(self):
        remote = FakeProxy()
        proxy.reconcile_proxy('check', target(), remote, True)
        self.assertEqual(remote.events, ['validate'])
        remote = FakeProxy()
        remote.deployed['spec']['strategy']['type'] = 'RollingUpdate'
        with self.assertRaises(proxy.MailProxyError):
            proxy.reconcile_proxy('apply', target(), remote, True)
        self.assertEqual(remote.events, [])

    def test_ssh_requires_verified_host_and_uses_stdin_for_target(self):
        args = SimpleNamespace(ssh='/repo/.state/bin/ssh', identity='/private/key', known_hosts='/private/hosts')
        command = proxy.ssh_command(args, 'operator@100.64.1.2')
        self.assertEqual(command[0], args.ssh)
        self.assertIn('StrictHostKeyChecking=yes', command)
        self.assertIn('BatchMode=yes', command)
        self.assertIn('IdentitiesOnly=yes', command)
        self.assertIn('UserKnownHostsFile=/private/hosts', command)
        self.assertEqual(command[-2], 'operator@100.64.1.2')
        self.assertTrue(command[-1].startswith('sudo -n python3 -c '))
        self.assertTrue(command[-1].endswith(' --remote'))


class MailProxyBackendTests(unittest.TestCase):
    def test_persistence_uses_compare_and_swap_and_keeps_config_out_of_arguments(self):
        backend = proxy.RemoteProxy(target())
        stored = {'metadata': {'resourceVersion': '17'}, 'data': {'Caddyfile': 'original-private-config'}}
        with patch.object(backend, 'kubectl', return_value=b'{"metadata":{"resourceVersion":"18"}}') as kubectl:
            backend.persist(stored, 'original-private-config', 'desired-private-config')
        command, payload = kubectl.call_args.args
        self.assertIn('--patch-file=/dev/stdin', command)
        self.assertNotIn('private-config', repr(command))
        operations = json.loads(payload)
        self.assertEqual(operations, [
            {'op': 'test', 'path': '/metadata/resourceVersion', 'value': '17'},
            {'op': 'test', 'path': '/data/Caddyfile', 'value': 'original-private-config'},
            {'op': 'replace', 'path': '/data/Caddyfile', 'value': 'desired-private-config'},
        ])

    def test_reload_is_conditional_and_never_changes_admin_endpoint(self):
        backend = proxy.RemoteProxy(target())
        response = Mock()
        response.__enter__ = Mock(return_value=response)
        response.__exit__ = Mock(return_value=False)
        with patch.object(backend.opener, 'open', return_value=response) as request:
            backend.reload(current_config(), 'version-17')
        value = request.call_args.args[0]
        self.assertEqual(value.full_url, 'http://127.0.0.1:2019/config/')
        self.assertEqual(value.method, 'POST')
        self.assertEqual(value.get_header('If-match'), 'version-17')
        self.assertEqual(json.loads(value.data), current_config())

    def test_restart_requires_recreate_and_waits_for_rollout(self):
        backend = proxy.RemoteProxy(target())
        with patch.object(backend, 'deployment', return_value=deployment()), patch.object(backend, 'kubectl') as kubectl:
            backend.restart()
        self.assertEqual(kubectl.call_args_list[0].args[0], ['rollout', 'restart', 'deployment/caddy'])
        self.assertEqual(kubectl.call_args_list[1].args[0], ['rollout', 'status', 'deployment/caddy', '--timeout=180s'])


if __name__ == '__main__':
    unittest.main()
