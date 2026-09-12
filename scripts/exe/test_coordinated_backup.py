import copy
import json
import unittest

from coordinated_backup import Backup, COLD_CLAIMS, FORMAT, MARKER, validate_checkpoint


def item(name, replicas, kind='Deployment', wave=20, namespace='noebs'):
    return {'kind': kind, 'name': name, 'namespace': namespace, 'uid': name + '-uid',
            'replicas': replicas, 'wave': wave, 'selector': 'app=' + name}


def checkpoint():
    return {'format': FORMAT, 'id': '20260912T180000Z', 'phase': 'planned', 'cluster_uid': 'cluster',
            'release': {'stage': 'empty-staging'},
            'workloads': [item('api-gateway', 2), item('wallet-api', 1), item('wallet-worker', 2),
                          item('identity-worker', 0), item('temporal', 1, wave=15), item('keycloak', 1, wave=3)],
            'cold_workloads': [item('kafka', 1, 'StatefulSet'), item('noebs-mojaloop-redis', 1, 'StatefulSet')],
            'cold_volumes': {name: '/var/lib/rancher/k3s/storage/' + name for name in COLD_CLAIMS},
            'cronjobs': [{'name': 'enabled', 'uid': 'enabled-uid', 'suspend': False},
                         {'name': 'disabled', 'uid': 'disabled-uid', 'suspend': True}],
            'edge': item('caddy', 2, namespace='edge')}


class Host:
    def __init__(self, state):
        self.events, self.marker, self.saved = [], None, None
        self.state = copy.deepcopy(state)
        self.objects = {(obj['namespace'], obj['kind'].lower() + '/' + obj['name']):
                        {'kind': obj['kind'], 'metadata': {'name': obj['name'], 'uid': obj['uid']},
                         'spec': {'replicas': obj['replicas']}}
                        for obj in state['workloads'] + state['cold_workloads'] + [state['edge']]}
        self.objects.update({('noebs', 'cronjob/' + obj['name']):
                             {'kind': 'CronJob', 'metadata': {'name': obj['name'], 'uid': obj['uid']},
                              'spec': {'suspend': obj['suspend']}} for obj in state['cronjobs']})
        self.fail_snapshot, self.extra_pods, self.database_clients = False, [], b'0'

    def save(self, path, state):
        self.saved = copy.deepcopy(state)
        self.events.append(('save', state['phase']))

    def optional(self, name):
        if name == MARKER:
            return self.marker
        raise AssertionError(name)

    def get(self, resource, namespace='noebs'):
        if resource == 'namespace/noebs':
            return {'metadata': {'uid': 'cluster'}}
        if resource == 'jobs':
            return {'items': [{'metadata': {'name': 'finishing-job'}, 'status': {'active': 1}}]}
        if resource == 'deployments,statefulsets,cronjobs,pods':
            return {'items': [obj for (ns, _), obj in self.objects.items() if ns == namespace] + self.extra_pods}
        return self.objects[(namespace, resource)]

    def run(self, command, payload=None):
        self.events.append(('run', command))
        if 'noebs-backup snapshot ' in command:
            assert self.saved['phase'] == 'fenced'
            assert self.objects[('edge', 'deployment/caddy')]['spec']['replicas'] == 0
            if self.fail_snapshot:
                raise RuntimeError('snapshot failed')
        if 'noebs-backup publish ' in command:
            assert self.saved['phase'] == 'resumed'
            assert self.objects[('edge', 'deployment/caddy')]['spec']['replicas'] == 2
            validate_checkpoint(self.saved)
        return b''

    def kube(self, args, namespace='noebs'):
        self.events.append(('kube', tuple(args), namespace))
        if args[0] == 'scale':
            self.objects[(namespace, args[1])]['spec']['replicas'] = int(args[2].split('=')[1])
        elif args[0] == 'patch':
            self.objects[(namespace, args[1])]['spec'].update(json.loads(args[-1])['spec'])
        elif args[:2] == ['create', 'configmap']:
            self.marker = {'checkpoint_id': self.state['id']}
        elif args[0] == 'delete':
            self.marker = None
        elif args[0] == 'exec':
            return self.database_clients
        return b''


class CoordinatedBackupTests(unittest.TestCase):
    def setUp(self):
        self.state = checkpoint()
        self.host = Host(self.state)
        self.backup = Backup(self.host, self.state, '/private/checkpoint.json')

    def scales(self):
        return [(entry[1][1], json.loads(entry[1][-1])['spec']['replicas']) for entry in self.host.events
                if entry[0] == 'kube' and entry[1][0] == 'patch' and 'replicas' in json.loads(entry[1][-1])['spec']]

    def assert_original_state(self):
        for obj in self.host.state['workloads'] + self.host.state['cold_workloads'] + [self.host.state['edge']]:
            self.assertEqual(self.host.get(obj['kind'].lower() + '/' + obj['name'], obj['namespace'])['spec']['replicas'], obj['replicas'])
        for obj in self.host.state['cronjobs']:
            self.assertEqual(self.host.get('cronjob/' + obj['name'])['spec']['suspend'], obj['suspend'])

    def test_snapshot_preserves_exact_workloads_and_publishes_after_resumption(self):
        result = self.backup.execute()
        self.assert_original_state()
        self.assertEqual(result['phase'], 'published')
        self.assertGreaterEqual(result['pause_seconds'], 0)
        self.assertIsNone(self.host.marker)
        operations = self.scales()
        self.assertEqual(operations[0], ('deployment/caddy', 0))
        self.assertEqual(operations[-1], ('deployment/caddy', 2))
        self.assertLess(operations.index(('deployment/wallet-worker', 0)), operations.index(('deployment/wallet-api', 0)))
        self.assertLess(operations.index(('deployment/wallet-api', 0)), operations.index(('deployment/temporal', 0)))
        self.assertLess(operations.index(('deployment/temporal', 0)), operations.index(('statefulset/kafka', 0)))
        self.assertLess(operations.index(('deployment/keycloak', 1)), operations.index(('deployment/temporal', 1)))
        self.assertIn(('deployment/identity-worker', 0), operations)

    def test_peers_in_same_restore_wave_start_before_waiting_for_readiness(self):
        self.backup.execute()
        events = self.host.events
        last_peer_start = max(index for index, event in enumerate(events) if event[0] == 'kube'
                              and event[1][:2] in [('patch', 'deployment/api-gateway'), ('patch', 'deployment/wallet-api'), ('patch', 'deployment/wallet-worker')]
                              and json.loads(event[1][-1])['spec']['replicas'] != 0)
        first_peer_wait = next(index for index, event in enumerate(events) if event[0] == 'kube'
                               and event[1][:2] == ('rollout', 'status') and event[1][2] == 'deployment/api-gateway')
        self.assertLess(last_peer_start, first_peer_wait)

    def test_failed_snapshot_restores_service_but_is_never_published(self):
        self.host.fail_snapshot = True
        with self.assertRaisesRegex(RuntimeError, 'snapshot failed'):
            self.backup.execute()
        self.assert_original_state()
        self.assertEqual(self.state['phase'], 'snapshot-failed')
        self.assertFalse(any(event[0] == 'run' and 'noebs-backup publish' in event[1] for event in self.host.events))
        with self.assertRaises(ValueError):
            validate_checkpoint(self.state)

    def test_unmanaged_writer_or_database_client_prevents_snapshot(self):
        for problem in ['pod', 'database']:
            with self.subTest(problem=problem):
                state = checkpoint()
                host = Host(state)
                if problem == 'pod':
                    host.extra_pods = [{'kind': 'Pod', 'metadata': {'name': 'unmanaged-writer'}, 'status': {'phase': 'Running'}}]
                else:
                    host.database_clients = b'1'
                with self.assertRaises(ValueError):
                    Backup(host, state, '/private/checkpoint.json').execute()
                self.assertFalse(any(event[0] == 'run' and 'noebs-backup snapshot' in event[1] for event in host.events))
                self.assertEqual(state['phase'], 'snapshot-failed')

    def test_replaced_workload_or_checkpoint_fences_restoration(self):
        self.backup.pause()
        self.host.get('statefulset/kafka')['metadata']['uid'] = 'new-statefulset'
        with self.assertRaisesRegex(ValueError, 'Workload changed'):
            self.backup.restore()
        self.assertIsNotNone(self.host.marker)
        self.assertEqual(self.host.get('deployment/caddy', 'edge')['spec']['replicas'], 0)
        self.host.marker = {'checkpoint_id': 'another-checkpoint'}
        with self.assertRaisesRegex(ValueError, 'fence was replaced'):
            self.backup.restore()

    def test_old_online_backups_and_incomplete_fences_are_rejected(self):
        complete = self.backup.execute()
        self.assertIs(validate_checkpoint(complete), complete)
        for changed in [{}, {'format': 'online-backup-v1'}, {'phase': 'fenced'}, {'snapshot_completed_at': None},
                        {'fenced_at': '9999-01-01T00:00:00+00:00'}, {'cold_volumes': {'kafka': '/some/path'}}]:
            with self.subTest(changed=changed), self.assertRaises(ValueError):
                validate_checkpoint(changed if not changed else complete | changed)


if __name__ == '__main__':
    unittest.main()
