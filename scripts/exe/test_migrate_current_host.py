import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, patch
import uuid

spec = importlib.util.spec_from_file_location('migration', Path(__file__).with_name('migrate-current-host.py'))
migration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(migration)


def edge_config():
    return {'apps': {'http': {'servers': {'shared': {'routes': [
        {'match': [{'host': ['other.example']}], 'handle': [{'handler': 'other-site'}]},
        {'match': [{'host': ['api.noebs.sd']}], 'handle': [{'handler': 'source-site'}], 'terminal': True},
    ]}}}, 'tls': {'certificates': {'automate': ['api.noebs.sd', 'other.example']}}}}


class MigrationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.plan = {'id': '1234abcd1234', 'phase': 'planned', 'source': {}, 'destination': {},
                     'destination_host': 'destination', 'activate_command': ['promotion'],
                     'worker_ip': '100.85.107.107'}
        migration.atomic_json(self.directory / 'plan.json', self.plan)
        self.source, self.destination, self.worker = Mock(), Mock(), Mock()
        self.source.ssh_args = ['ssh', 'source']
        self.run = migration.Migration(self.directory, self.source, self.destination,
                    {'noebs-workers': self.worker}, '/age', '/private/key', Mock())

    def test_forwarding_preserves_other_domains_and_tls(self):
        original = edge_config()
        forwarded = migration.api_route(original, '100.85.107.107')
        before = original['apps']['http']['servers']['shared']['routes']
        after = forwarded['apps']['http']['servers']['shared']['routes']
        self.assertEqual(before[0], after[0])
        self.assertEqual(original['apps']['tls'], forwarded['apps']['tls'])
        proxy = after[1]['handle'][0]
        self.assertEqual(proxy['upstreams'], [{'dial': '100.85.107.107:8080'}])
        self.assertEqual(proxy['headers']['request']['set']['Host'], ['api.noebs.sd'])
        self.assertEqual(before[1]['handle'][0]['handler'], 'source-site')
        self.assertEqual(migration.api_route(original)['apps']['http']['servers']['shared']['routes'][1]['handle'][0]['status_code'], 503)

    def test_edge_json_mount_avoids_caddyfile_filename_auto_adapter(self):
        self.run.plan['source'] = {'edge': {'spec': {'template': {'spec': {
            'volumes': [{'name': 'config', 'configMap': {'name': 'original'}}],
            'containers': [{'name': 'caddy', 'args': ['caddy', 'run', '--config', '/etc/caddy/Caddyfile', '--adapter', 'caddyfile'],
                'volumeMounts': [{'name': 'config', 'mountPath': '/etc/caddy/Caddyfile', 'subPath': 'Caddyfile'}]}]}}}}}
        self.run.install_edge(migration.api_route(edge_config()), 'maintenance')
        document = json.loads(self.source.kube.call_args_list[0].args[1])
        self.assertIn('Caddyfile', document['data'])
        patch_args = self.source.kube.call_args_list[1].args[0]
        template = json.loads(patch_args[patch_args.index('-p') + 1])['spec']['template']['spec']
        container = template['containers'][0]
        self.assertEqual(container['args'], ['caddy', 'run', '--config', '/etc/caddy/migration.json'])
        self.assertEqual(container['volumeMounts'][0], {'name': 'config', 'mountPath': '/etc/caddy/migration.json', 'subPath': 'Caddyfile'})

    def test_forwarding_rejects_public_upstream_and_ambiguous_site(self):
        with self.assertRaises(ValueError):
            migration.api_route(edge_config(), '203.0.113.1')
        config = edge_config()
        config['apps']['http']['servers']['shared']['routes'].append(copy.deepcopy(config['apps']['http']['servers']['shared']['routes'][1]))
        with self.assertRaises(ValueError):
            migration.api_route(config, '100.85.107.107')

    def test_failed_mutation_is_durable_and_never_replayed(self):
        def interrupted_restore():
            raise ConnectionError('SSH disconnected after a database rename')
        with self.assertRaises(ConnectionError):
            self.run.step('planned', 'restored', interrupted_restore)
        saved = json.loads((self.directory / 'plan.json').read_text())
        self.assertEqual(saved['phase'], 'planned')
        self.assertEqual(saved['operation']['name'], 'interrupted_restore')
        retry = Mock()
        with self.assertRaises(RuntimeError):
            self.run.step('planned', 'restored', retry)
        retry.assert_not_called()

    def test_stale_release_is_rejected_before_admission_is_stopped(self):
        self.run.plan['release_revision'] = 'reviewed-revision'
        with patch.object(migration.subprocess, 'check_output', return_value=b'new-revision\n'):
            with self.assertRaisesRegex(ValueError, 'revision changed'):
                self.run.freeze()
        self.source.kube.assert_not_called()
        self.destination.kube.assert_not_called()

    def test_activation_tools_must_exist_before_running_the_migration(self):
        with patch.object(migration.shutil, 'which', side_effect=lambda name: None if name == 'kustomize' else '/bin/' + name):
            with self.assertRaisesRegex(ValueError, 'missing from PATH: kustomize'):
                migration.require_release_tools()

    def test_replaced_cluster_and_workload_drift_require_a_new_plan(self):
        expected = {'cluster_uid': 'source-cluster', 'resources': [], 'applications': [],
                    'edge': {'kind': 'Deployment', 'metadata': {'name': 'caddy', 'uid': 'edge-uid'},
                             'spec': {'replicas': 1}}, 'edge_config': edge_config()}
        with self.assertRaisesRegex(ValueError, 'cluster was replaced'):
            migration.verify_plan_unchanged(expected, expected | {'cluster_uid': 'other-cluster'})
        changed = copy.deepcopy(expected)
        changed['edge']['spec']['replicas'] = 2
        with self.assertRaisesRegex(ValueError, 'configuration changed'):
            migration.verify_plan_unchanged(expected, changed)

    def test_execute_never_activates_after_verification_failure(self):
        self.run.freeze = Mock(__name__='freeze')
        self.run.snapshot = Mock(__name__='snapshot')
        self.run.restore = Mock(__name__='restore')
        self.run.verify = Mock(__name__='verify', side_effect=ValueError('ledger changed'))
        self.run.activate = Mock(__name__='activate')
        self.run.route = Mock(__name__='route')
        with self.assertRaises(ValueError):
            self.run.execute()
        self.run.activate.assert_not_called()
        self.run.route.assert_not_called()
        self.assertEqual(self.run.plan['phase'], 'restored')

    def test_resume_continues_after_completed_stage_without_repeating_restore(self):
        self.run.plan['phase'] = 'restored'
        for name in ['freeze', 'snapshot', 'restore', 'verify', 'activate', 'route', 'publish_final_plan']:
            setattr(self.run, name, Mock(__name__=name))
        self.run.execute()
        self.run.freeze.assert_not_called()
        self.run.snapshot.assert_not_called()
        self.run.restore.assert_not_called()
        self.run.verify.assert_called_once()
        self.run.activate.assert_called_once()
        self.run.route.assert_called_once()
        self.run.publish_final_plan.assert_called_once()
        self.assertEqual(self.run.plan['phase'], 'complete')

    def test_destination_write_boundary_is_saved_before_any_activation(self):
        self.run.assert_stopped = Mock()
        def started(*args, **kwargs):
            saved = json.loads((self.directory / 'plan.json').read_text())
            self.assertIn('destination_write_boundary', saved)
            raise RuntimeError('promotion health check failed after workers began writing')
        with patch.object(migration.subprocess, 'run', side_effect=started), self.assertRaises(RuntimeError):
            self.run.activate()
        self.destination.sql.assert_not_called()
        handoff = json.loads((self.directory / 'native-handoff.json').read_text())
        self.assertEqual(handoff, {'migration_id': self.plan['id'], 'source_ssh_args': ['ssh', 'source']})
        self.assertEqual((self.directory / 'native-handoff.json').stat().st_mode & 0o777, 0o600)
        self.source.sql.assert_not_called()

    def test_stop_closes_admission_before_workers_and_temporal(self):
        names = ['temporal', 'wallet-worker', 'identity-worker', 'identity-auth', 'keycloak', 'api-gateway', 'psp-webhook']
        resources = [{'kind': 'Deployment', 'metadata': {'name': name}} for name in names]
        resources += [{'kind': 'CronJob', 'metadata': {'name': 'cleanup'}}]
        self.destination.get.return_value = {'items': []}
        self.run.stop(self.destination, {'applications': [], 'resources': resources})
        calls = [call.args[0] for call in self.destination.kube.call_args_list]
        scales = [call for call in calls if call[0] == 'patch' and call[1].startswith(('deployment/', 'statefulset/'))]
        self.assertEqual([call[1] for call in scales[:2]], ['deployment/api-gateway', 'deployment/psp-webhook'])
        order = [call[1] for call in scales]
        self.assertLess(order.index('deployment/identity-worker'), order.index('deployment/temporal'))
        self.assertLess(order.index('deployment/identity-worker'), order.index('deployment/identity-auth'))
        self.assertLess(order.index('deployment/wallet-worker'), order.index('statefulset/kafka'))
        for call in scales:
            self.assertIn('--field-manager=noebs-release', call)
            self.assertEqual(json.loads(call[call.index('-p') + 1]), {'spec': {'replicas': 0}})

    def test_running_writer_blocks_snapshot(self):
        self.source.get.return_value = {'items': [{'kind': 'Deployment', 'metadata': {'name': 'wallet-worker'},
                                                   'spec': {'replicas': 1}}]}
        with self.assertRaisesRegex(ValueError, 'wallet-worker'):
            self.run.snapshot()
        self.source.run.assert_not_called()

    def test_source_change_after_snapshot_blocks_activation(self):
        self.run.assert_stopped = Mock()
        self.run.plan['restored_databases'] = [{'authority': 'postgres', 'name': 'wallet_ledger'}]
        self.run.plan['fingerprints'] = {'postgres-wallet_ledger': [{'rows': 2}]}
        self.run.fingerprint = Mock(return_value=[{'rows': 3}])
        with self.assertRaisesRegex(ValueError, 'Source changed'):
            self.run.verify()

    def test_route_requires_successful_matching_promotion(self):
        self.destination.get.return_value = {'data': {'state': 'destination-active', 'migration_id': 'another-migration'}}
        with self.assertRaisesRegex(ValueError, 'not completed'):
            self.run.route()
        self.worker.run.assert_not_called()

    def test_private_reachability_probe_runs_inside_caddy_pod(self):
        migration.probe_worker_from_edge(self.source, '100.85.107.107')
        args = self.source.kube.call_args.args[0]
        self.assertEqual(args[:4], ['exec', 'deployment/caddy', '--', 'wget'])
        self.assertIn('http://100.85.107.107:8080/test', args)
        self.assertEqual(self.source.kube.call_args.kwargs['namespace'], 'edge')
        self.source.run.assert_not_called()

    def test_database_acl_and_per_role_settings_are_preserved(self):
        database = {'owner': 'owner', 'acl': [
            {'grantee': 'owner', 'grantor': 'owner', 'privilege': 'CONNECT', 'grantable': True},
            {'grantee': 'wallet_runtime', 'grantor': 'owner', 'privilege': 'CONNECT', 'grantable': False}],
            'settings': [{'role': 'wallet_runtime', 'values': ['search_path=public', 'statement_timeout=30s']}]}
        sql = migration.database_permissions(database, 'staging')
        self.assertTrue(sql.startswith('REVOKE ALL ON DATABASE "staging" FROM PUBLIC;'))
        self.assertIn('GRANT CONNECT ON DATABASE "staging" TO "wallet_runtime";', sql)
        self.assertIn('ALTER ROLE "wallet_runtime" IN DATABASE "staging" SET "statement_timeout" TO \'30s\';', sql)
        self.assertNotIn('DROP', sql)

    def test_empty_database_acl_removes_default_public_and_owner_grants(self):
        sql = migration.database_permissions({'owner': 'database_owner', 'acl': [], 'settings': []}, 'restored')
        self.assertEqual(sql, 'REVOKE ALL ON DATABASE "restored" FROM PUBLIC;\n'
                             'REVOKE ALL ON DATABASE "restored" FROM "database_owner";')

    def test_existing_staging_marker_requires_only_known_fixture_identity(self):
        self.destination.get.return_value = {'metadata': {'uid': 'release-uid'},
            'data': {'stage': 'empty-staging', 'revision': 'reviewed'}}
        fixture = {'id': 1, 'tenant_id': 'tenant-cutover', 'issuer': 'https://staging-lifecycle-smoke.invalid',
                   'subject': 'staging-lifecycle-smoke:fixture'}
        self.destination.sql.return_value = json.dumps(fixture).encode()
        accepted = migration.staging_marker(self.destination, 'target-cluster')
        self.assertEqual(accepted['cluster_uid'], 'target-cluster')
        self.assertEqual(accepted['users'], [fixture])
        for users in [[], [fixture, fixture | {'id': 2}], [fixture | {'issuer': 'https://api.noebs.sd/auth/realms/noebs'}]]:
            self.destination.sql.return_value = '\n'.join(json.dumps(user) for user in users).encode()
            with self.assertRaisesRegex(ValueError, 'smoke fixture'):
                migration.staging_marker(self.destination, 'target-cluster')
        self.destination.get.return_value['data']['stage'] = 'production'
        with self.assertRaisesRegex(ValueError, 'staging cluster'):
            migration.staging_marker(self.destination, 'target-cluster')

    def test_backup_checkpoint_is_rejected_before_inventory_reads_databases(self):
        self.source.kube.return_value = b'configmap/noebs-backup-checkpoint\n'
        with self.assertRaisesRegex(ValueError, 'backup checkpoint'):
            migration.inventory(self.source)
        self.source.sql.assert_not_called()
        self.source.get.assert_not_called()

    def test_restore_replaces_canonical_target_only_after_snapshot_validation(self):
        self.run.assert_stopped = Mock()
        self.run.restore_archive = Mock()
        marker = {'cluster_uid': 'target-cluster', 'data': {'stage': 'empty-staging'}}
        db = {'name': 'wallet_ledger', 'owner': 'wallet_owner', 'encoding': 'UTF8',
              'collate': 'en_US.utf8', 'ctype': 'en_US.utf8', 'acl': [], 'settings': []}
        self.run.plan.update({'destination': {'cluster_uid': 'target-cluster', 'volumes': {}},
            'destination_staging': marker, 'source': {'authorities': {'postgres': {'databases': [db]}}},
            'archives': {}, 'off_host_archives': {}})
        for name, payload in [('postgres-wallet_ledger', b'source encrypted database archive'),
                              ('destination-before-postgres', b'encrypted original staging archive')]:
            filename = name + '.age'
            (self.directory / filename).write_bytes(payload)
            self.run.plan['archives'][name] = {'file': filename, 'sha256': migration.digest(payload)}
            self.run.plan['off_host_archives'][name] = '/backup/' + filename
        backup = Mock()
        backup.run.return_value = ''.join(self.run.plan['archives'][name]['sha256'] + '  ' + path + '\n'
            for name, path in reversed(list(self.run.plan['off_host_archives'].items()))).encode()
        self.run.volume_hosts['noebs-backup'] = backup
        decrypt = Mock()
        def replacing(*args):
            self.assertIn('destination_replacement_started_at', json.loads((self.directory / 'plan.json').read_text()))
            self.assertEqual(decrypt.call_count, 2)
        self.destination.sql.side_effect = replacing
        with patch.object(migration, 'reject_backup_checkpoint'), patch.object(migration, 'staging_marker', return_value=marker), \
             patch.object(migration.subprocess, 'run', decrypt):
            self.run.restore()
        backup.run.assert_called_once_with('sudo sha256sum -- /backup/postgres-wallet_ledger.age /backup/destination-before-postgres.age')
        sql = self.destination.sql.call_args_list[0].args[2]
        self.assertIn('DROP DATABASE "wallet_ledger";\nCREATE DATABASE "wallet_ledger"', sql)
        self.assertNotIn('noebs_restore_', sql)
        self.assertNotIn('noebs_before_', sql)
        self.source.sql.assert_not_called()
        self.assertEqual(self.run.plan['restored_databases'], [{'authority': 'postgres', 'name': 'wallet_ledger'}])

    def test_restore_refuses_changed_staging_identity_before_any_drop(self):
        self.run.assert_stopped = Mock()
        self.run.plan.update({'destination': {'cluster_uid': 'target-cluster'}, 'destination_staging': {'users': ['fixture']}})
        with patch.object(migration, 'reject_backup_checkpoint'), patch.object(migration, 'staging_marker', return_value={'users': ['customer']}):
            with self.assertRaisesRegex(ValueError, 'before replacement'):
                self.run.restore()
        self.destination.sql.assert_not_called()

    def test_restore_refuses_corrupt_off_host_copy_before_any_drop(self):
        self.run.assert_stopped = Mock()
        marker = {'users': ['fixture']}
        backup = Mock()
        self.run.volume_hosts['noebs-backup'] = backup
        payload = b'encrypted source database archive'
        (self.directory / 'source.age').write_bytes(payload)
        expected = migration.digest(payload)
        self.run.plan.update({'destination': {'cluster_uid': 'target'}, 'destination_staging': marker,
            'archives': {'source': {'file': 'source.age', 'sha256': expected}},
            'off_host_archives': {'source': '/backup/source.age'}})
        for label, received in [('changed checksum', '0' * 64 + '  /backup/source.age\n'),
                                ('missing archive', ''),
                                ('unexpected archive', expected + '  /backup/source.age\n' + expected + '  /backup/other.age\n')]:
            with self.subTest(label=label):
                backup.reset_mock()
                backup.run.return_value = received.encode()
                with patch.object(migration, 'reject_backup_checkpoint'), patch.object(migration, 'staging_marker', return_value=marker), \
                     patch.object(migration.subprocess, 'run') as decrypt:
                    with self.assertRaisesRegex(ValueError, 'Off-host encrypted snapshot checksum'):
                        self.run.restore()
                    decrypt.assert_not_called()
                backup.run.assert_called_once_with('sudo sha256sum -- /backup/source.age')
                self.assertNotIn('destination_replacement_started_at', self.run.plan)
        self.destination.sql.assert_not_called()

    def test_publish_archive_checks_received_bytes_before_finalizing(self):
        payload = b'encrypted migration snapshot'
        (self.directory / 'snapshot.age').write_bytes(payload)
        self.run.plan['archives'] = {'snapshot': {'file': 'snapshot.age', 'sha256': migration.digest(payload)}}
        backup = Mock()
        backup.argv.side_effect = lambda command: ['ssh', 'backup', command]
        backup.run.side_effect = [b'', b'wrong-checksum  snapshot.age.partial\n']
        self.run.volume_hosts['noebs-backup'] = backup
        with patch.object(migration.subprocess, 'run'), self.assertRaisesRegex(ValueError, 'Off-host encrypted snapshot checksum'):
            self.run.publish_archive('snapshot')
        self.assertEqual(backup.run.call_count, 2)
        self.assertNotIn('sudo mv', backup.run.call_args.args[0])


@unittest.skipUnless(os.environ.get('NOEBS_TEST_POSTGRES_URL'), 'set NOEBS_TEST_POSTGRES_URL for SQL integration')
class FingerprintPostgresTests(unittest.TestCase):
    def test_empty_catalog_acl_serializes_and_restores_as_an_empty_list(self):
        binary = os.environ.get('PSQL', 'psql')
        url = os.environ['NOEBS_TEST_POSTGRES_URL']
        source = 'migration_acl_source_' + uuid.uuid4().hex
        target = 'migration_acl_target_' + uuid.uuid4().hex
        def sql(query):
            return subprocess.check_output([binary, '-X', '-qAt', '-v', 'ON_ERROR_STOP=1', '--dbname', url], input=query.encode())
        owner = sql('SELECT current_user;').decode().strip()
        sql('CREATE DATABASE ' + migration.identifier(source) + ';\nCREATE DATABASE ' + migration.identifier(target) + ';')
        try:
            sql('REVOKE ALL ON DATABASE ' + migration.identifier(source) + ' FROM PUBLIC;\n'
                'REVOKE ALL ON DATABASE ' + migration.identifier(source) + ' FROM ' + migration.identifier(owner) + ';')
            catalog = {row['name']: row for row in migration.lines_json(sql(migration.DATABASES_SQL))}
            self.assertEqual(catalog[source]['acl'], [])
            self.assertTrue(catalog[target]['acl'])
            sql(migration.database_permissions(catalog[source], target))
            restored = {row['name']: row for row in migration.lines_json(sql(migration.DATABASES_SQL))}
            self.assertEqual(restored[target]['acl'], [])
        finally:
            sql('DROP DATABASE ' + migration.identifier(source) + ';\nDROP DATABASE ' + migration.identifier(target) + ';')

    def test_real_psql_fingerprints_are_parseable_and_detect_evidence_balance_and_sequence_changes(self):
        binary = os.environ.get('PSQL', 'psql')
        url = os.environ['NOEBS_TEST_POSTGRES_URL']
        name = 'migration_test_' + uuid.uuid4().hex
        def sql(query, database=None):
            argv = [binary, '-X', '-qAt', '-v', 'ON_ERROR_STOP=1', '--dbname', url]
            if database:
                from urllib.parse import urlsplit, urlunsplit
                parts = urlsplit(url)
                argv[-1] = urlunsplit(parts._replace(path='/' + database))
            return subprocess.check_output(argv, input=query.encode())
        sql('CREATE DATABASE ' + migration.identifier(name))
        try:
            sql("""CREATE TABLE identity_evidence(id bigserial PRIMARY KEY,tenant_id text,image_bytes bytea);
INSERT INTO identity_evidence(tenant_id,image_bytes) VALUES('tenant-a',decode('abcd','hex'));
CREATE TABLE goose_db_version_identity_auth(id bigint,version_id bigint,is_applied boolean);
INSERT INTO goose_db_version_identity_auth VALUES(1,3,true);
CREATE TABLE wallets(id bigint,tenant_id text,currency text,balance bigint,available_balance bigint);
INSERT INTO wallets VALUES(1,'tenant-a','SDG',120,100);
CREATE TABLE ledger_entries(id bigint,tenant_id text,currency text,entry_type text,amount bigint);
INSERT INTO ledger_entries VALUES(1,'tenant-a','SDG','credit',120);""", name)
            def fingerprint():
                return migration.lines_json(sql(migration.FINGERPRINT_SQL + migration.LEDGER_SQL, name))
            baseline = fingerprint()
            self.assertTrue(any('migration_table' in row for row in baseline))
            self.assertTrue(any('wallet_balances' in row for row in baseline))
            for change in ["UPDATE identity_evidence SET image_bytes=decode('1234','hex')",
                           'UPDATE wallets SET balance=121', "SELECT nextval('identity_evidence_id_seq')"]:
                sql(change, name)
                changed = fingerprint()
                self.assertNotEqual(baseline, changed)
                baseline = changed
            # Replacing a fenced canonical database restores evidence, ledger values,
            # migration records and sequence state without retaining alternate names.
            from urllib.parse import urlsplit, urlunsplit
            database_url = urlunsplit(urlsplit(url)._replace(path='/' + name))
            dump = subprocess.check_output([str(Path(binary).with_name('pg_dump')), '-Fc', '--dbname', database_url])
            sql('DROP DATABASE ' + migration.identifier(name) + ';\nCREATE DATABASE ' + migration.identifier(name) + ' TEMPLATE template0;')
            subprocess.run([str(Path(binary).with_name('pg_restore')), '--exit-on-error', '--single-transaction',
                            '--dbname', database_url], input=dump, check=True)
            self.assertEqual(baseline, fingerprint())
        finally:
            sql('DROP DATABASE ' + migration.identifier(name))


if __name__ == '__main__':
    unittest.main()
