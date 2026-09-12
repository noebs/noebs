#!/usr/bin/env python3
"""Fence, copy, verify and cut over the current k3s authority to EXE.

`plan` is read-only on both hosts. `execute` consumes that saved plan once.
An interrupted mutation stays recorded; it is never retried or rolled back
automatically, including when activation has started accepting new writes.
"""
import argparse
import base64
import copy
from datetime import datetime, timezone
import fcntl
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import uuid

import yaml

from reconcile import ROOT, RemoteLease, ssh_args
from promote import verify_receipt
from native_relay import verify_native_relay, verify_native_handoff

ORIGIN = 'https://api.noebs.sd'
AUTHORITIES = {'postgres': 'postgres', 'temporal': 'temporal', 'keycloak': 'keycloak'}
CORE_DATABASES = {'admin_reporting', 'card_vault', 'ebs_adapter', 'gateway_auth',
                  'identity_auth', 'notification_chat', 'wallet_ledger', 'workload_auth'}
COLD_VOLUMES = {'kafka': 'kafka-data-kafka-0',
                'noebs-mojaloop-redis': 'data-noebs-mojaloop-redis-0'}
DATABASES_SQL = """SELECT json_build_object('name',datname,'owner',pg_get_userbyid(datdba),
'encoding',pg_encoding_to_char(encoding),'collate',datcollate,'ctype',datctype,
'provider',datlocprovider,'acl',coalesce((SELECT json_agg(json_build_object(
'grantee',CASE WHEN a.grantee=0 THEN 'PUBLIC' ELSE pg_get_userbyid(a.grantee) END,
'grantor',pg_get_userbyid(a.grantor),'privilege',a.privilege_type,'grantable',a.is_grantable)
ORDER BY a.grantee,a.privilege_type) FROM aclexplode(coalesce(datacl,acldefault('d',datdba))) a),'[]'),
'settings',coalesce((SELECT json_agg(json_build_object('role',CASE WHEN setrole=0 THEN '' ELSE pg_get_userbyid(setrole) END,
'values',setconfig) ORDER BY setrole) FROM pg_db_role_setting WHERE setdatabase=pg_database.oid),'[]'))
FROM pg_database WHERE NOT datistemplate ORDER BY datname;"""
FINGERPRINT_SQL = r"""BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL timezone = 'UTC';
SET LOCAL bytea_output = 'hex';
SELECT format($q$SELECT json_build_object('table',%L,'rows',count(*),
'hash_hi',coalesce(sum(('x'||substr(md5(to_jsonb(t)::text),1,16))::bit(64)::bigint::numeric),0)::text,
'hash_lo',coalesce(sum(('x'||substr(md5(to_jsonb(t)::text),17,16))::bit(64)::bigint::numeric),0)::text)
FROM %I.%I t;$q$,schemaname||'.'||tablename,schemaname,tablename)
FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema') ORDER BY 1
\gexec
SELECT format($q$SELECT json_build_object('sequence',%L,'last_value',last_value,'is_called',is_called) FROM %I.%I;$q$,
schemaname||'.'||sequencename,schemaname,sequencename) FROM pg_sequences ORDER BY 1
\gexec
SELECT format($q$SELECT json_build_object('tenant_table',%L,'tenants',coalesce(jsonb_object_agg(tenant_id,n),'{}'))
FROM (SELECT tenant_id::text,count(*) n FROM %I.%I GROUP BY tenant_id) t;$q$,
table_schema||'.'||table_name,table_schema,table_name)
FROM information_schema.columns WHERE column_name='tenant_id' AND table_schema='public' ORDER BY 1
\gexec
SELECT format($q$SELECT json_build_object('migration_table',%L,'versions',jsonb_agg(to_jsonb(t) ORDER BY version_id,id)) FROM %I.%I t;$q$,
schemaname||'.'||tablename,schemaname,tablename) FROM pg_tables WHERE tablename LIKE 'goose_db_version%' ORDER BY 1
\gexec
COMMIT;
"""
LEDGER_SQL = """SELECT json_build_object('wallet_balances',coalesce(jsonb_agg(to_jsonb(t) ORDER BY tenant_id,currency),'[]'))
FROM (SELECT tenant_id,currency,count(*) wallets,sum(balance)::text balance,
sum(available_balance)::text available FROM wallets GROUP BY tenant_id,currency) t;
SELECT json_build_object('ledger_totals',coalesce(jsonb_agg(to_jsonb(t) ORDER BY tenant_id,currency,entry_type),'[]'))
FROM (SELECT tenant_id,currency,entry_type,count(*) entries,sum(amount)::text amount
FROM ledger_entries GROUP BY tenant_id,currency,entry_type) t;"""


def now():
    return datetime.now(timezone.utc).isoformat()


def digest(payload):
    return hashlib.sha256(payload).hexdigest()


def atomic_json(path, value):
    temporary = path.with_suffix('.tmp')
    with temporary.open('w') as stream:
        json.dump(value, stream, indent=2, sort_keys=True)
        stream.write('\n')
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)
    directory = os.open(path.parent, os.O_DIRECTORY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)


def identifier(value):
    return '"' + value.replace('"', '""') + '"'


def literal(value):
    return "'" + value.replace("'", "''") + "'"


def lines_json(payload):
    return [json.loads(line) for line in payload.decode().splitlines() if line]


class Host:
    def __init__(self, ssh_args):
        self.ssh_args = ssh_args

    def argv(self, command):
        return self.ssh_args + [command]

    def run(self, command, payload=None):
        return subprocess.run(self.argv(command), input=payload, check=True,
                              stdout=subprocess.PIPE).stdout

    def kube(self, args, payload=None, namespace='noebs'):
        return self.run('sudo k3s kubectl -n ' + shlex.quote(namespace) + ' ' + shlex.join(args), payload)

    def get(self, resource, namespace='noebs'):
        return json.loads(self.kube(['get', resource, '-o', 'json'], namespace=namespace))

    def pg_command(self, authority, command):
        pod = 'postgres-0' if authority == 'postgres' else authority + '-postgres-0'
        if authority == 'postgres':
            command = ['gosu', 'postgres'] + command
        else:
            command = ['bash', '-ec', 'export PGPASSWORD=$(cat /opt/' + authority +
                       '-postgres/secrets/password); exec ' + shlex.join(command)]
        return 'sudo k3s kubectl -n noebs exec -i ' + pod + ' -- ' + shlex.join(command)

    def sql(self, authority, database, sql):
        command = self.pg_command(authority, ['psql', '-X', '-qAt', '-v', 'ON_ERROR_STOP=1',
                        '-h', '/var/run/postgresql', '-U', AUTHORITIES[authority], '-d', database])
        pipeline = 'printf %s ' + shlex.quote(base64.b64encode(sql.encode()).decode()) + ' | base64 -d | ' + command
        return self.run('bash -o pipefail -c ' + shlex.quote(pipeline))


def exe_host(key, destination):
    return Host(ssh_args(key, destination))


def inventory(host):
    reject_backup_checkpoint(host)
    resources = host.get('deployments,statefulsets,cronjobs,jobs,pods,pvc,hpa')['items']
    if any(item['kind'] == 'HorizontalPodAutoscaler' for item in resources):
        raise ValueError('Remove autoscaling for this planned cutover before continuing')
    crds = host.get('customresourcedefinitions', namespace='default')['items']
    applications = []
    if any(item['metadata']['name'] == 'applications.argoproj.io' for item in crds):
        applications = [item for item in json.loads(host.run('sudo k3s kubectl get applications.argoproj.io -A -o json'))['items']
                        if item['spec']['destination']['namespace'] in ['noebs', 'edge']]
        if any(item.get('operation') or item.get('status', {}).get('operationState', {}).get('phase') == 'Running'
               for item in applications):
            raise ValueError('A source release is still reconciling; plan after it finishes')
    edge = host.get('deployment/caddy', namespace='edge')
    config_volume = next(volume for volume in edge['spec']['template']['spec']['volumes'] if volume['name'] == 'config')
    config = host.get('configmap/' + config_volume['configMap']['name'], namespace='edge')['data']['Caddyfile']
    authorities = {}
    for authority in AUTHORITIES:
        databases = lines_json(host.sql(authority, 'postgres', DATABASES_SQL))
        if any(db['provider'] != 'c' for db in databases):
            raise ValueError('Migration requires an explicit locale restore plan for non-libc databases')
        authorities[authority] = {
            'databases': databases,
            'version': host.sql(authority, 'postgres', 'SHOW server_version_num;').decode().strip(),
            'roles': lines_json(host.sql(authority, 'postgres', "SELECT json_build_object('name',rolname,'superuser',rolsuper) FROM pg_roles WHERE rolname NOT LIKE 'pg_%' ORDER BY rolname;")),
        }
    if {db['name'] for db in authorities['postgres']['databases']} != CORE_DATABASES | {'postgres'}:
        raise ValueError('Core database inventory differs from the eight service databases')
    volumes = {}
    claims = {item['metadata']['name']: item for item in resources if item['kind'] == 'PersistentVolumeClaim'}
    for name, claim_name in COLD_VOLUMES.items():
        pv = host.get('pv/' + claims[claim_name]['spec']['volumeName'])
        sources = [pv['spec'][kind] for kind in ['local', 'hostPath'] if kind in pv['spec']]
        if len(sources) != 1:
            raise ValueError('Cold migration requires one local filesystem volume source')
        path = sources[0]['path']
        if not path.startswith('/var/lib/rancher/k3s/storage/'):
            raise ValueError('Cold volume is outside the managed k3s storage directory')
        terms = pv['spec']['nodeAffinity']['required']['nodeSelectorTerms']
        node = terms[0]['matchExpressions'][0]['values'][0]
        volumes[name] = {'path': path, 'node': node, 'claim': claim_name}
    # Verify persistent ciphertext keys and external callback credentials without recording values.
    continuity = {}
    for name, fields in {
        'api-gateway-secrets': ['gateway_auth_encryption_key_id', 'gateway_auth_encryption_keys',
                               'backoffice_client_secret', 'wallet_authorizer_client_secret', 'psp_webhook_routes'],
        'card-vault-secrets': ['data_key'],
        'psp-webhook-secrets': ['psp'],
        'wallet-worker-secrets': ['psp'],
    }.items():
        document = yaml.safe_load(base64.b64decode(host.get('secret/' + name)['data']['secrets.yaml']))['noebs']
        continuity[name] = {field: digest(json.dumps(document[field], sort_keys=True).encode()) for field in fields}
    ilp = host.get('secret/noebs-mojaloop-sdk')['data']['ilp-secret']
    continuity['noebs-mojaloop-sdk'] = {'ilp-secret': digest(base64.b64decode(ilp))}
    return {'cluster_uid': host.get('namespace/kube-system', namespace='default')['metadata']['uid'],
            'resources': resources, 'applications': applications, 'edge': edge,
            'edge_config': config, 'authorities': authorities, 'volumes': volumes, 'continuity': continuity}


def reject_backup_checkpoint(host):
    if host.kube(['get', 'configmap/noebs-backup-checkpoint', '--ignore-not-found', '-o', 'name']).strip():
        raise ValueError('A coordinated backup checkpoint must be resumed before migration')


def staging_marker(host, cluster_uid):
    marker = host.get('configmap/noebs-release')
    data = marker['data']
    if data.get('stage') != 'empty-staging':
        raise ValueError('Destination is not the explicitly designated fixture staging cluster')
    users = lines_json(host.sql('postgres', 'identity_auth',
        "SELECT json_build_object('id',id,'tenant_id',tenant_id,'issuer',issuer,'subject',subject) FROM users ORDER BY id;"))
    if (len(users) != 1 or users[0]['id'] != 1 or users[0]['tenant_id'] != 'tenant-cutover'
            or users[0]['issuer'] != 'https://staging-lifecycle-smoke.invalid'
            or not users[0]['subject'].startswith('staging-lifecycle-smoke:')):
        raise ValueError('Destination identity data is not the single verified lifecycle smoke fixture')
    return {'cluster_uid': cluster_uid, 'uid': marker['metadata']['uid'], 'data': data, 'users': users}


def api_route(config, upstream=None):
    config = copy.deepcopy(config)
    matches = []
    for server in config['apps']['http']['servers'].values():
        for route in server['routes']:
            if any(match.get('host') == ['api.noebs.sd'] for match in route.get('match', [])):
                matches.append(route)
    if len(matches) != 1:
        raise ValueError('Expected one dedicated api.noebs.sd Caddy route')
    handler = {'handler': 'static_response', 'status_code': 503,
               'headers': {'Retry-After': ['60']}, 'body': 'Service migration in progress\n'}
    if upstream is not None:
        address = ipaddress.ip_address(upstream)
        if address not in ipaddress.ip_network('100.64.0.0/10'):
            raise ValueError('EXE forwarding must use the private Tailscale address')
        handler = {'handler': 'reverse_proxy', 'upstreams': [{'dial': str(address) + ':8080'}],
                   'headers': {'request': {'set': {'Host': ['api.noebs.sd'],
                        'X-Forwarded-Host': ['api.noebs.sd'], 'X-Forwarded-Proto': ['https']}}}}
    matches[0]['handle'] = [handler]
    return config


def probe_worker_from_edge(source, worker_ip):
    if ipaddress.ip_address(worker_ip) not in ipaddress.ip_network('100.64.0.0/10'):
        raise ValueError('EXE edge probe requires the private Tailscale address')
    source.kube(['exec', 'deployment/caddy', '--', 'wget', '-q', '-O', '/dev/null', '-T', '20',
                 '--header=Host: api.noebs.sd', 'http://' + worker_ip + ':8080/test'], namespace='edge')


def verify_authorities(source, destination):
    if source['cluster_uid'] == destination['cluster_uid']:
        raise ValueError('Source and destination are the same cluster')
    if source['continuity'] != destination['continuity']:
        raise ValueError('Persistent encryption keys or external callback credentials differ')
    def cold_runtime(snapshot, name):
        workload = next(item for item in snapshot['resources'] if item['kind'] == 'StatefulSet' and item['metadata']['name'] == name)
        return [{key: container[key] for key in ['name', 'image', 'args', 'command', 'env'] if key in container}
                for container in workload['spec']['template']['spec']['containers']]
    for name in COLD_VOLUMES:
        if cold_runtime(source, name) != cold_runtime(destination, name):
            raise ValueError('Cold volume runtime or broker identity differs for ' + name)
    for name in AUTHORITIES:
        left, right = source['authorities'][name], destination['authorities'][name]
        if int(left['version']) // 10000 != int(right['version']) // 10000:
            raise ValueError('PostgreSQL major versions differ for ' + name)
        if left['roles'] != right['roles']:
            raise ValueError('Database ownership roles differ for ' + name)
        shape = lambda databases: [{key: value for key, value in db.items() if key not in ['acl', 'settings']} for db in databases]
        if shape(left['databases']) != shape(right['databases']):
            raise ValueError('Database names, owners or locales differ for ' + name)


def verify_plan_unchanged(expected, actual):
    if expected['cluster_uid'] != actual['cluster_uid']:
        raise ValueError('A cluster was replaced after migration planning')
    def spec(snapshot):
        kinds = ['Deployment', 'StatefulSet', 'CronJob']
        objects = snapshot['resources'] + snapshot['applications'] + [snapshot['edge']]
        return {(item['kind'], item['metadata']['name']): (item['metadata']['uid'], item['spec'])
                for item in objects if item['kind'] in kinds + ['Application']}
    if spec(expected) != spec(actual) or expected['edge_config'] != actual['edge_config']:
        raise ValueError('A release or workload configuration changed after migration planning')


def database_permissions(database, target):
    statements = ['REVOKE ALL ON DATABASE ' + identifier(target) + ' FROM PUBLIC;',
                  'REVOKE ALL ON DATABASE ' + identifier(target) + ' FROM ' + identifier(database['owner']) + ';']
    for grant in database['acl']:
        grantee = 'PUBLIC' if grant['grantee'] == 'PUBLIC' else identifier(grant['grantee'])
        statements += ['SET ROLE ' + identifier(grant['grantor']) + ';',
            'GRANT ' + grant['privilege'] + ' ON DATABASE ' + identifier(target) + ' TO ' + grantee +
            (' WITH GRANT OPTION' if grant['grantable'] else '') + ';', 'RESET ROLE;']
    for setting in database['settings']:
        for value in setting['values']:
            key, value = value.split('=', 1)
            prefix = 'ALTER DATABASE ' + identifier(target)
            if setting['role']:
                prefix = 'ALTER ROLE ' + identifier(setting['role']) + ' IN DATABASE ' + identifier(target)
            statements.append(prefix + ' SET ' + identifier(key) + ' TO ' + literal(value) + ';')
    return '\n'.join(statements)


class Migration:
    def __init__(self, directory, source, destination, volume_hosts, age, identity, lease):
        self.directory, self.source, self.destination = directory, source, destination
        self.volume_hosts, self.age, self.identity = volume_hosts, str(age), str(identity)
        self.lease = lease
        self.path = directory / 'plan.json'
        self.plan = json.loads(self.path.read_text())

    def save(self):
        atomic_json(self.path, self.plan)

    def step(self, phase, next_phase, action):
        self.lease.check()
        if self.plan.get('operation'):
            raise RuntimeError('A previous mutation is unresolved; inspect its recorded operation before proceeding')
        if self.plan['phase'] != phase:
            raise ValueError('Expected migration phase ' + phase)
        self.plan['operation'] = {'name': action.__name__, 'started_at': now()}
        self.save()
        action()
        self.plan['phase'] = next_phase
        self.plan['operation'] = None
        self.save()

    def install_edge(self, config, suffix):
        self.lease.check()
        name = 'noebs-migration-' + self.plan['id'] + '-' + suffix
        self.source.kube(['apply', '-f', '-'], json.dumps({'apiVersion': 'v1', 'kind': 'ConfigMap',
              'metadata': {'name': name, 'namespace': 'edge'},
              'data': {'Caddyfile': json.dumps(config)}}).encode(), namespace='edge')
        template = copy.deepcopy(self.plan['source']['edge']['spec']['template'])
        for volume in template['spec']['volumes']:
            if volume['name'] == 'config':
                volume['configMap']['name'] = name
        for container in template['spec']['containers']:
            if container['name'] == 'caddy':
                container['args'] = ['caddy', 'run', '--config', '/etc/caddy/migration.json']
                for mount in container['volumeMounts']:
                    if mount['name'] == 'config':
                        mount['mountPath'] = '/etc/caddy/migration.json'
        self.source.kube(['patch', 'deployment/caddy', '--type=merge', '-p',
                          json.dumps({'spec': {'template': template}})], namespace='edge')
        self.source.kube(['rollout', 'status', 'deployment/caddy', '--timeout=300s'], namespace='edge')

    def stop(self, host, snapshot):
        self.lease.check()
        manager = 'noebs-release' if host is self.destination else 'noebs-migration'
        for app in snapshot['applications']:
            host.kube(['patch', 'application/' + app['metadata']['name'], '--type=merge', '-p', json.dumps({
                'metadata': {'annotations': {'argocd.argoproj.io/skip-reconcile': 'true'}},
                'spec': {'syncPolicy': {'automated': {'enabled': False}}}}), '--field-manager=' + manager], namespace=app['metadata']['namespace'])
        for item in snapshot['resources']:
            if item['kind'] == 'CronJob':
                host.kube(['patch', 'cronjob/' + item['metadata']['name'], '--type=merge', '-p', '{"spec":{"suspend":true}}', '--field-manager=' + manager])
        # Stop workers before their service dependencies; recover persisted work after full pod termination.
        deployments = [item['metadata']['name'] for item in snapshot['resources'] if item['kind'] == 'Deployment']
        order = [name for name in ['api-gateway', 'psp-webhook'] if name in deployments]
        order += [name for name in ['wallet-worker', 'identity-worker'] if name in deployments]
        order += [name for name in deployments if name not in order + ['temporal', 'keycloak']]
        order += [name for name in ['temporal', 'keycloak'] if name in deployments]
        for name in order:
            self.lease.check()
            host.kube(['patch', 'deployment/' + name, '--type=merge', '-p', '{"spec":{"replicas":0}}', '--field-manager=' + manager])
            host.kube(['wait', '--for=delete', 'pods', '-l', 'app.kubernetes.io/name=' + name, '--timeout=300s'])
        for item in host.get('jobs')['items']:
            if item.get('status', {}).get('active', 0):
                host.kube(['wait', '--for=condition=Complete', 'job/' + item['metadata']['name'], '--timeout=300s'])
        for name in COLD_VOLUMES:
            self.lease.check()
            host.kube(['patch', 'statefulset/' + name, '--type=merge', '-p', '{"spec":{"replicas":0}}', '--field-manager=' + manager])
            host.kube(['wait', '--for=delete', 'pod/' + name + '-0', '--timeout=300s'])

    def assert_stopped(self, host):
        resources = host.get('deployments,cronjobs,pods')['items']
        for item in resources:
            if item['kind'] == 'Deployment' and item['spec']['replicas'] != 0:
                raise ValueError('Runtime admission or worker was restarted: ' + item['metadata']['name'])
            if item['kind'] == 'CronJob' and not item['spec'].get('suspend'):
                raise ValueError('A scheduled writer was restarted')
            if item['kind'] == 'Pod' and item.get('status', {}).get('phase') not in ['Succeeded', 'Failed']:
                if item['metadata']['name'] not in ['postgres-0', 'temporal-postgres-0', 'keycloak-postgres-0']:
                    raise ValueError('A writer pod still exists: ' + item['metadata']['name'])

    def freeze(self):
        revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT).decode().strip()
        if revision != self.plan['release_revision']:
            raise ValueError('Release revision changed after migration planning')
        if activation_receipts(self.plan['activate_command'], revision) != self.plan['release_receipts']:
            raise ValueError('Image receipts changed after migration planning')
        left, right = inventory(self.source), inventory(self.destination)
        verify_authorities(left, right)
        verify_plan_unchanged(self.plan['source'], left)
        verify_plan_unchanged(self.plan['destination'], right)
        if staging_marker(self.destination, right['cluster_uid']) != self.plan['destination_staging']:
            raise ValueError('Destination staging identity or release marker changed after planning')
        probe_worker_from_edge(self.source, self.plan['worker_ip'])
        verify_native_relay(self.source, self.volume_hosts['noebs-workers'])
        self.lease.check()
        marker = {'apiVersion': 'v1', 'kind': 'ConfigMap', 'metadata': {'name': 'noebs-migration', 'namespace': 'noebs'},
                  'data': {'migration_id': self.plan['id'], 'state': 'destination-staged', 'destination': self.plan['destination_host']}}
        self.destination.kube(['create', '-f', '-'], json.dumps(marker).encode())
        marker['data']['state'] = 'source-fenced'
        self.source.kube(['create', '-f', '-'], json.dumps(marker).encode())
        # Pause source reconciliation before replacing only the production site's route.
        for app in self.plan['source']['applications']:
            self.source.kube(['patch', 'application/' + app['metadata']['name'], '--type=merge', '-p', json.dumps({
                'metadata': {'annotations': {'argocd.argoproj.io/skip-reconcile': 'true'}},
                'spec': {'syncPolicy': {'automated': {'enabled': False}}}})], namespace=app['metadata']['namespace'])
        self.install_edge(api_route(self.plan['source_edge_json']), 'maintenance')
        self.stop(self.source, self.plan['source'])
        self.stop(self.destination, self.plan['destination'])
        for host in [self.source, self.destination]:
            self.assert_stopped(host)
        if staging_marker(self.destination, right['cluster_uid']) != self.plan['destination_staging']:
            raise ValueError('Destination received account data while admission was stopping')
        for authority, info in self.plan['source']['authorities'].items():
            for database in info['databases']:
                if database['name'] == 'postgres':
                    continue
                self.source.sql(authority, 'postgres', 'ALTER DATABASE ' + identifier(database['name']) +
                                ' SET default_transaction_read_only = on;')
            connections = self.source.sql(authority, 'postgres', "SELECT count(*) FROM pg_stat_activity WHERE backend_type='client backend' AND pid<>pg_backend_pid();")
            if connections.strip() != b'0':
                raise ValueError('Unexpected database clients remain after fencing ' + authority)

    def archive(self, name, host, command):
        path = self.directory / (name + '.age')
        producer = subprocess.Popen(host.argv(command), stdout=subprocess.PIPE)
        args = [self.age]
        for recipient in self.plan['recipients']:
            args += ['-r', recipient]
        with path.with_suffix('.partial').open('xb') as output:
            encrypted = subprocess.run(args, stdin=producer.stdout, stdout=output)
            producer.stdout.close()
            source_status = producer.wait()
            output.flush()
            os.fsync(output.fileno())
        if encrypted.returncode or source_status:
            raise RuntimeError('Encrypted snapshot failed: ' + name)
        os.rename(path.with_suffix('.partial'), path)
        self.plan['archives'][name] = {'file': path.name, 'sha256': file_digest(path), 'bytes': path.stat().st_size}
        self.save()

    def restore_archive(self, name, host, command):
        self.lease.check()
        archive = self.plan['archives'][name]
        path = self.directory / archive['file']
        if file_digest(path) != archive['sha256']:
            raise ValueError('Encrypted snapshot checksum mismatch: ' + name)
        remote_dir = '/var/lib/noebs/migration/' + self.plan['id']
        payload_path = remote_dir + '/' + name + '.payload'
        host.run('sudo install -d -m 0700 ' + shlex.quote(remote_dir))
        decrypted = subprocess.Popen([self.age, '-d', '-i', self.identity, str(path)], stdout=subprocess.PIPE)
        uploaded = subprocess.Popen(host.argv('sudo tee ' + shlex.quote(payload_path) +
                    ' >/dev/null && sudo chmod 0600 ' + shlex.quote(payload_path)), stdin=subprocess.PIPE)
        checksum = hashlib.sha256()
        try:
            while block := decrypted.stdout.read(1024 * 1024):
                checksum.update(block)
                uploaded.stdin.write(block)
        finally:
            uploaded.stdin.close()
            decrypted.stdout.close()
        if uploaded.wait() or decrypted.wait():
            raise RuntimeError('Restore payload upload failed: ' + name)
        received = host.run('sudo sha256sum ' + shlex.quote(payload_path)).decode().split()[0]
        if received != checksum.hexdigest():
            raise ValueError('Restore payload transfer checksum mismatch: ' + name)
        self.lease.check()
        with (self.directory / (name + '-restore.log')).open('ab') as log:
            restored = subprocess.run(host.argv('bash -o pipefail -c ' + shlex.quote(
                'sudo cat ' + shlex.quote(payload_path) + ' | ' + command)), stdout=log, stderr=log)
        if restored.returncode:
            raise RuntimeError('Restore interrupted: ' + name)
        host.run('sudo rm -- ' + shlex.quote(payload_path))

    def fingerprint(self, host, authority, database, logical_name):
        sql = FINGERPRINT_SQL + (LEDGER_SQL if logical_name == 'wallet_ledger' else '')
        return lines_json(host.sql(authority, database, sql))

    def archive_plan(self, name):
        path = self.directory / (name + '.age')
        command = [self.age]
        for recipient in self.plan['recipients']:
            command += ['-r', recipient]
        with path.open('xb') as output:
            subprocess.run(command, input=json.dumps(self.plan, sort_keys=True).encode(), stdout=output, check=True)
            output.flush()
            os.fsync(output.fileno())
        self.plan['archives'][name] = {'file': path.name, 'sha256': file_digest(path), 'bytes': path.stat().st_size}
        self.save()

    def publish_archive(self, name):
        self.lease.check()
        host = self.volume_hosts['noebs-backup']
        archive = self.plan['archives'][name]
        path = self.directory / archive['file']
        if file_digest(path) != archive['sha256']:
            raise ValueError('Encrypted snapshot checksum mismatch: ' + name)
        remote_dir = '/var/lib/noebs-backup/migrations/' + self.plan['id']
        remote_path = remote_dir + '/' + path.name
        host.run('sudo install -d -m 0700 ' + shlex.quote(remote_dir))
        with path.open('rb') as stream:
            subprocess.run(host.argv('sudo test ! -e ' + shlex.quote(remote_path) +
                ' && sudo tee ' + shlex.quote(remote_path + '.partial') + ' >/dev/null'), stdin=stream, check=True)
        received = host.run('sudo sha256sum ' + shlex.quote(remote_path + '.partial')).decode().split()[0]
        if received != archive['sha256']:
            raise ValueError('Off-host encrypted snapshot checksum mismatch: ' + name)
        host.run('sudo chmod 0600 ' + shlex.quote(remote_path + '.partial') +
                 ' && sudo mv ' + shlex.quote(remote_path + '.partial') + ' ' + shlex.quote(remote_path) +
                 ' && sudo sync -f ' + shlex.quote(remote_path))
        return remote_path

    def snapshot(self):
        self.assert_stopped(self.source)
        self.assert_stopped(self.destination)
        self.plan['archives'], self.plan['fingerprints'] = {}, {}
        for authority, info in self.plan['source']['authorities'].items():
            # Full cluster archives include password hashes and ownership for offline disaster recovery.
            self.archive(authority + '-cluster', self.source, self.source.pg_command(authority,
                ['pg_dumpall', '-h', '/var/run/postgresql', '-U', AUTHORITIES[authority]]))
            self.archive('destination-before-' + authority, self.destination, self.destination.pg_command(authority,
                ['pg_dumpall', '-h', '/var/run/postgresql', '-U', AUTHORITIES[authority]]))
            for db in info['databases']:
                name = authority + '-' + db['name']
                self.plan['fingerprints'][name] = self.fingerprint(self.source, authority, db['name'], db['name'])
                self.archive(name, self.source, self.source.pg_command(authority,
                    ['pg_dump', '-Fc', '-h', '/var/run/postgresql', '-U', AUTHORITIES[authority], '-d', db['name']]))
        for name, volume in self.plan['source']['volumes'].items():
            self.archive(name, self.source, 'sudo tar --numeric-owner -cpf - -C ' + shlex.quote(volume['path']) + ' .')
        self.archive('source-resources', self.source, 'sudo k3s kubectl -n noebs get all,pvc,configmaps,secrets -o json')
        self.archive('source-edge', self.source, 'sudo k3s kubectl -n edge get deployment,configmaps,secrets -o json')
        self.archive_plan('snapshot-plan')
        self.plan['off_host_archives'] = {}
        for name in self.plan['archives']:
            self.plan['off_host_archives'][name] = self.publish_archive(name)
        self.plan['off_host_verified_at'] = now()
        self.save()

    def restore(self):
        self.assert_stopped(self.source)
        self.assert_stopped(self.destination)
        reject_backup_checkpoint(self.source)
        reject_backup_checkpoint(self.destination)
        if staging_marker(self.destination, self.plan['destination']['cluster_uid']) != self.plan['destination_staging']:
            raise ValueError('Destination staging identity or release marker changed before replacement')
        expected = {self.plan['off_host_archives'][name]: archive['sha256']
                    for name, archive in self.plan['archives'].items()}
        output = self.volume_hosts['noebs-backup'].run('sudo sha256sum -- ' + shlex.join(expected)).decode()
        received = {path: checksum for checksum, path in (line.split(maxsplit=1) for line in output.splitlines())}
        if received != expected:
            raise ValueError('Off-host encrypted snapshot checksum mismatch before replacement')
        for name, archive in self.plan['archives'].items():
            path = self.directory / archive['file']
            if file_digest(path) != archive['sha256']:
                raise ValueError('Encrypted snapshot checksum mismatch: ' + name)
            subprocess.run([self.age, '-d', '-i', self.identity, str(path)], check=True, stdout=subprocess.DEVNULL)
        self.plan['restored_databases'] = []
        self.plan['destination_replacement_started_at'] = now()
        self.save()
        for authority, info in self.plan['source']['authorities'].items():
            for db in info['databases']:
                self.lease.check()
                name = db['name']
                maintenance = 'template1' if name == 'postgres' else 'postgres'
                self.destination.sql(authority, maintenance, 'DROP DATABASE ' + identifier(name) + ';\nCREATE DATABASE ' + identifier(name) +
                    ' WITH TEMPLATE template0 OWNER ' + identifier(db['owner']) +
                    ' ENCODING ' + literal(db['encoding']) + ' LC_COLLATE ' + literal(db['collate']) +
                    ' LC_CTYPE ' + literal(db['ctype']) + ';')
                self.restore_archive(authority + '-' + name, self.destination,
                    self.destination.pg_command(authority, ['pg_restore', '--exit-on-error', '--single-transaction',
                        '-h', '/var/run/postgresql', '-U', AUTHORITIES[authority], '-d', name]))
                self.destination.sql(authority, maintenance, database_permissions(db, name))
                self.plan['restored_databases'].append({'authority': authority, 'name': name})
                self.save()
        for name, volume in self.plan['destination']['volumes'].items():
            self.lease.check()
            host = self.volume_hosts[volume['node']]
            path = volume['path']
            retained = path + '.before-' + self.plan['id']
            host.run('sudo test ! -e ' + shlex.quote(retained) + ' && sudo mv ' + shlex.quote(path) + ' ' +
                     shlex.quote(retained) + ' && sudo mkdir ' + shlex.quote(path))
            self.restore_archive(name, host, 'sudo tar --numeric-owner -xpf - -C ' + shlex.quote(path))

    def verify(self):
        self.assert_stopped(self.source)
        self.assert_stopped(self.destination)
        for db in self.plan['restored_databases']:
            self.lease.check()
            name = db['authority'] + '-' + db['name']
            expected = self.plan['fingerprints'][name]
            if self.fingerprint(self.source, db['authority'], db['name'], db['name']) != expected:
                raise ValueError('Source changed after snapshot: ' + name)
            if self.fingerprint(self.destination, db['authority'], db['name'], db['name']) != expected:
                raise ValueError('Rows, evidence, tenants, ledger balances, sequences or migrations differ: ' + name)
        # tar compares metadata and file contents, including Kafka offsets and Redis AOF/RDB.
        for name, volume in self.plan['destination']['volumes'].items():
            self.restore_archive(name, self.volume_hosts[volume['node']],
                                 'sudo tar --compare -f - -C ' + shlex.quote(volume['path']))
        self.plan['verified_at'] = now()

    def activate(self):
        self.assert_stopped(self.source)
        self.assert_stopped(self.destination)
        # Persist before any destination worker or schema job can write. There is no automatic failback.
        self.plan['destination_write_boundary'] = now()
        self.save()
        native_handoff = self.directory / 'native-handoff.json'
        atomic_json(native_handoff, {'migration_id': self.plan['id'], 'source_ssh_args': self.source.ssh_args})
        native_handoff.chmod(0o600)
        self.lease.check()
        subprocess.run(self.plan['activate_command'] + ['--native-handoff', str(native_handoff.resolve())], cwd=ROOT, check=True,
                       env=os.environ | {'NOEBS_RELEASE_LEASE': self.plan['id']})
        self.assert_stopped(self.source)

    def route(self):
        marker = self.destination.get('configmap/noebs-migration')['data']
        if marker.get('state') != 'destination-active' or marker.get('migration_id') != self.plan['id']:
            raise ValueError('Destination promotion has not completed for this migration')
        worker = self.volume_hosts['noebs-workers']
        verify_native_handoff(self.source, worker)
        metadata = json.loads(worker.run('curl --fail --silent --show-error --max-time 20 -H "Host: api.noebs.sd" '
                         'http://127.0.0.1:8080/auth/realms/noebs/.well-known/openid-configuration'))
        if metadata['issuer'] != ORIGIN + '/auth/realms/noebs':
            raise ValueError('Destination OpenID issuer would change the production origin')
        probe_worker_from_edge(self.source, self.plan['worker_ip'])
        self.install_edge(api_route(self.plan['source_edge_json'], self.plan['worker_ip']), 'forward')
        self.source.run('curl --fail --silent --show-error --max-time 20 https://api.noebs.sd/test >/dev/null')

    def publish_final_plan(self):
        self.archive_plan('final-plan')
        self.plan['off_host_archives']['final-plan'] = self.publish_archive('final-plan')

    def execute(self):
        stages = [('planned', 'frozen', self.freeze), ('frozen', 'snapshotted', self.snapshot),
                ('snapshotted', 'restored', self.restore), ('restored', 'verified', self.verify),
                ('verified', 'activated', self.activate), ('activated', 'routed', self.route),
                ('routed', 'complete', self.publish_final_plan)]
        if self.plan.get('operation'):
            raise RuntimeError('A previous mutation is unresolved; inspect its recorded operation before proceeding')
        phases = [stage[0] for stage in stages] + ['complete']
        start = phases.index(self.plan['phase'])
        for before, after, action in stages[start:]:
            self.step(before, after, action)


def file_digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def activation_receipts(command, revision):
    if command.count('--receipts') != 1:
        raise ValueError('Activation argv must name exactly one verified receipt directory')
    receipts = Path(command[command.index('--receipts') + 1])
    result = {}
    for filename, role in [('noebs-receipt.json', 'app'), ('sdk-receipt.json', 'sdk')]:
        path = receipts / filename
        verify_receipt(path, revision, role)
        result[filename] = file_digest(path)
    return result


def require_release_tools():
    missing = [name for name in ['crane', 'kustomize', 'sops', 'go', 'ssh'] if not shutil.which(name)]
    if missing:
        raise ValueError('Migration activation tools are missing from PATH: ' + ', '.join(missing))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['plan', 'execute'])
    parser.add_argument('--work', type=Path, required=True)
    parser.add_argument('--source-ssh', type=Path, required=True)
    parser.add_argument('--source', required=True)
    parser.add_argument('--key', type=Path, required=True)
    parser.add_argument('--machines', type=Path, required=True)
    parser.add_argument('--age', type=Path, required=True)
    parser.add_argument('--identity', type=Path, required=True)
    parser.add_argument('--recipient', action='append', required=True)
    parser.add_argument('--activate-command', type=Path, required=True, help='JSON argv for verified EXE promotion')
    args = parser.parse_args()
    require_release_tools()
    os.umask(0o077)
    args.work.mkdir(parents=True, exist_ok=True, mode=0o700)
    with (args.work / 'lock').open('w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        machines = json.loads(args.machines.read_text())
        hosts = {name: exe_host(args.key.resolve(), value['ssh_destination']) for name, value in machines.items()}
        source = Host([str(args.source_ssh.resolve()), '-o', 'StrictHostKeyChecking=yes', args.source])
        destination = hosts['noebs-data']
        path = args.work / 'plan.json'
        if args.command == 'plan':
            if path.exists():
                raise ValueError('A migration plan already exists; use a new work directory')
            challenge = os.urandom(32)
            age_args = [str(args.age.resolve())]
            for recipient in args.recipient:
                age_args += ['-r', recipient]
            encrypted = subprocess.check_output(age_args, input=challenge)
            restored = subprocess.check_output([str(args.age.resolve()), '-d', '-i', str(args.identity.resolve())], input=encrypted)
            if challenge != restored:
                raise ValueError('Snapshot encryption identity failed its local roundtrip')
            left, right = inventory(source), inventory(destination)
            verify_authorities(left, right)
            staging = staging_marker(destination, right['cluster_uid'])
            worker_ip = hosts['noebs-workers'].run('tailscale ip -4').decode().strip()
            edge_json = json.loads(source.kube(['exec', 'deployment/caddy', '--', 'caddy', 'adapt', '--config',
                                               '/etc/caddy/Caddyfile', '--adapter', 'caddyfile'], namespace='edge'))
            api_route(edge_json, worker_ip)
            probe_worker_from_edge(source, worker_ip)
            verify_native_relay(source, hosts['noebs-workers'])
            command = json.loads(args.activate_command.read_text())
            if not isinstance(command, list) or not command or any(not isinstance(arg, str) or not arg for arg in command):
                raise ValueError('Activation command must be an explicit nonempty argv array')
            migration_id = uuid.uuid4().hex[:12]
            command += ['--migration-id', migration_id]
            revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT).decode().strip()
            receipts = activation_receipts(command, revision)
            atomic_json(path, {'id': migration_id, 'phase': 'planned', 'created_at': now(),
                'source_host': args.source, 'destination_host': machines['noebs-data']['ssh_destination'],
                'worker_ip': worker_ip, 'source': left, 'destination': right, 'source_edge_json': edge_json,
                'destination_staging': staging,
                'recipients': args.recipient, 'activate_command': command, 'origin': ORIGIN,
                'release_revision': revision, 'release_receipts': receipts})
            print('Read-only migration plan saved to', path)
        else:
            with RemoteLease(hosts['noebs-control'].ssh_args) as lease:
                migration = Migration(args.work, source, destination, hosts, args.age.resolve(), args.identity.resolve(), lease)
                if migration.plan['source_host'] != args.source or migration.plan['destination_host'] != machines['noebs-data']['ssh_destination']:
                    raise ValueError('Hosts differ from the reviewed migration plan')
                migration.execute()
            print('Production origin now forwards to EXE; source data remains fenced and retained.')


if __name__ == '__main__':
    main()
