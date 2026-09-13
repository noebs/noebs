#!/usr/bin/env python3
"""Pause the managed EXE writers for one consistent, encrypted backup set."""
import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import shlex
import signal
import subprocess

from reconcile import RemoteLease, ssh, ssh_args
from backup_checkpoint import FORMAT, validate_checkpoint
MARKER = 'noebs-backup-checkpoint'
DATABASE_PODS = {'postgres-0', 'temporal-postgres-0', 'keycloak-postgres-0'}
COLD_CLAIMS = {'kafka': 'kafka-data-kafka-0'}


def now():
    return datetime.now(timezone.utc).isoformat()


def stop_order(workloads):
    rank = {'api-gateway': 0, 'psp-webhook': 0, 'wallet-worker': 1, 'identity-worker': 1,
            'temporal': 3, 'keycloak': 3}
    return sorted(workloads, key=lambda item: (rank.get(item['name'], 2), item['name']))


class Host:
    def __init__(self, key, destination, lease):
        self.key, self.destination, self.lease = key, destination, lease

    def run(self, command, payload=None):
        self.lease.check()
        return ssh(self.key, self.destination, command, input=payload, stdout=subprocess.PIPE).stdout

    def kube(self, args, namespace='noebs'):
        return self.run('sudo k3s kubectl -n ' + namespace + ' ' + shlex.join(args))

    def get(self, resource, namespace='noebs'):
        return json.loads(self.kube(['get', resource, '-o', 'json'], namespace))

    def optional(self, name):
        result = self.kube(['get', 'configmap/' + name, '--ignore-not-found', '-o', 'json'])
        return json.loads(result)['data'] if result else None

    def save(self, path, value):
        payload = json.dumps(value, sort_keys=True, indent=2).encode() + b'\n'
        script = '''import os,sys
path=sys.argv[1]
with open(path+'.tmp','wb') as output:
 output.write(sys.stdin.buffer.read()); output.flush(); os.fsync(output.fileno())
os.replace(path+'.tmp',path)
directory=os.open(os.path.dirname(path),os.O_DIRECTORY)
os.fsync(directory); os.close(directory)
'''
        self.run('sudo python3 -c ' + shlex.quote(script) + ' ' + shlex.quote(path), payload)


def workload(item, namespace='noebs'):
    selector = item['spec']['selector']
    if selector.get('matchExpressions') or not selector.get('matchLabels'):
        raise ValueError('Backup requires the managed workload label selectors')
    return {'kind': item['kind'], 'name': item['metadata']['name'], 'namespace': namespace,
            'uid': item['metadata']['uid'], 'replicas': item['spec']['replicas'],
            'selector': ','.join(key + '=' + value for key, value in sorted(selector['matchLabels'].items())),
            'wave': int(item['metadata'].get('annotations', {}).get('argocd.argoproj.io/sync-wave', '0'))}


def inventory(host, staging_only):
    release = host.optional('noebs-release')
    migration = host.optional('noebs-migration')
    if not release or release.get('stage') not in ['empty-staging', 'production']:
        raise ValueError('A completed EXE deployment is required before backup')
    if migration and migration.get('state') != 'destination-active':
        raise ValueError('Migration or recovery is still fenced')
    if staging_only and (release['stage'] != 'empty-staging' or migration):
        raise ValueError('Backup measurement requires untouched empty staging')
    if host.optional(MARKER):
        raise ValueError('An earlier backup checkpoint requires restoration')
    resources = host.get('deployments,statefulsets,cronjobs,hpa')['items']
    if any(item['kind'] == 'HorizontalPodAutoscaler' for item in resources):
        raise ValueError('Autoscaling must be disabled for a coordinated snapshot')
    statefulsets = {item['metadata']['name']: item for item in resources if item['kind'] == 'StatefulSet'}
    if set(statefulsets) != {'postgres', 'temporal-postgres', 'keycloak-postgres'} | set(COLD_CLAIMS):
        raise ValueError('Unexpected stateful writer inventory')
    cold = {}
    for name, claim in COLD_CLAIMS.items():
        pvc = host.get('pvc/' + claim)
        pv = host.get('pv/' + pvc['spec']['volumeName'])
        sources = [pv['spec'][key] for key in ['local', 'hostPath'] if key in pv['spec']]
        nodes = pv['spec']['nodeAffinity']['required']['nodeSelectorTerms'][0]['matchExpressions'][0]['values']
        if len(sources) != 1 or not sources[0]['path'].startswith('/var/lib/rancher/k3s/storage/') or nodes != ['noebs-data']:
            raise ValueError('Cold snapshots require managed volumes on noebs-data')
        cold[name] = sources[0]['path']
    return {'format': FORMAT, 'id': datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ'),
            'cluster_uid': host.get('namespace/noebs')['metadata']['uid'], 'release': release,
            'workloads': [workload(item) for item in resources if item['kind'] == 'Deployment'],
            'cold_workloads': [workload(statefulsets[name]) for name in sorted(COLD_CLAIMS)],
            'edge': workload(host.get('deployment/traefik', 'kube-system'), 'kube-system'),
            'cronjobs': [{'name': item['metadata']['name'], 'uid': item['metadata']['uid'],
                          'suspend': item['spec'].get('suspend')} for item in resources if item['kind'] == 'CronJob'],
            'cold_volumes': cold, 'phase': 'planned'}


class Backup:
    def __init__(self, host, checkpoint, path):
        self.host, self.checkpoint, self.path = host, checkpoint, path

    def save(self):
        self.host.save(self.path, self.checkpoint)

    def scale(self, item, count, wait=True):
        resource = item['kind'].lower() + '/' + item['name']
        current = self.host.get(resource, item['namespace'])
        if current['metadata']['uid'] != item['uid']:
            raise ValueError('Workload changed during snapshot: ' + resource)
        self.host.kube(['patch', resource, '--type=merge', '--field-manager=noebs-release',
                        '-p', json.dumps({'spec': {'replicas': count}})], item['namespace'])
        if wait:
            self.wait(item, count)

    def wait(self, item, count):
        resource = item['kind'].lower() + '/' + item['name']
        if count == 0:
            self.host.kube(['wait', '--for=delete', 'pods', '-l', item['selector'], '--timeout=300s'], item['namespace'])
        else:
            self.host.kube(['rollout', 'status', resource, '--timeout=600s'], item['namespace'])

    def cron(self, item, suspended):
        if self.host.get('cronjob/' + item['name'])['metadata']['uid'] != item['uid']:
            raise ValueError('Scheduled workload changed during snapshot')
        self.host.kube(['patch', 'cronjob/' + item['name'], '--type=merge', '--field-manager=noebs-release',
                        '-p', json.dumps({'spec': {'suspend': suspended}})])

    def assert_fenced(self):
        for item in self.host.get('deployments,statefulsets,cronjobs,pods')['items']:
            kind, name = item['kind'], item['metadata']['name']
            if kind == 'Deployment' and item['spec']['replicas'] != 0:
                raise ValueError('Application writer remains active: ' + name)
            if kind == 'StatefulSet' and name in COLD_CLAIMS and item['spec']['replicas'] != 0:
                raise ValueError('Cold-store writer remains active: ' + name)
            if kind == 'CronJob' and not item['spec'].get('suspend'):
                raise ValueError('Scheduled writer remains active: ' + name)
            if kind == 'Pod' and item.get('status', {}).get('phase') not in ['Succeeded', 'Failed'] and name not in DATABASE_PODS:
                raise ValueError('Writer pod still exists: ' + name)
        if self.host.get('deployment/traefik', 'kube-system')['spec']['replicas'] != 0:
            raise ValueError('External admission is not fenced')
        query = "SELECT count(*) FROM pg_stat_activity WHERE backend_type='client backend' AND pid<>pg_backend_pid();"
        for authority, user in [('postgres', 'postgres'), ('temporal', 'temporal'), ('keycloak', 'keycloak')]:
            pod = 'postgres-0' if authority == 'postgres' else authority + '-postgres-0'
            command = ['psql', '-X', '-At', '-v', 'ON_ERROR_STOP=1', '-h', '/var/run/postgresql', '-U', user, '-d', 'postgres', '-c', query]
            if authority == 'postgres':
                command = ['gosu', 'postgres'] + command
            else:
                command = ['bash', '-ec', 'export PGPASSWORD=$(cat /opt/' + authority + '-postgres/secrets/password); exec ' + shlex.join(command)]
            if self.host.kube(['exec', pod, '--'] + command).strip() != b'0':
                raise ValueError('Database clients remain after fencing ' + authority)

    def pause(self):
        self.checkpoint['admission_paused_at'] = now()
        self.checkpoint['phase'] = 'pausing'
        self.save()
        self.host.kube(['create', 'configmap', MARKER, '--from-literal=checkpoint_id=' + self.checkpoint['id']])
        self.scale(self.checkpoint['edge'], 0)
        for item in self.checkpoint['cronjobs']:
            self.cron(item, True)
        for item in self.host.get('jobs')['items']:
            if item.get('status', {}).get('active', 0):
                self.host.kube(['wait', '--for=condition=Complete', 'job/' + item['metadata']['name'], '--timeout=300s'])
        for item in stop_order(self.checkpoint['workloads']) + self.checkpoint['cold_workloads']:
            self.scale(item, 0)
        self.assert_fenced()
        self.checkpoint['fenced_at'] = now()
        self.checkpoint['phase'] = 'fenced'
        self.save()

    def restore(self):
        if self.host.get('namespace/noebs')['metadata']['uid'] != self.checkpoint['cluster_uid']:
            raise ValueError('Refusing to restore workloads into a replaced cluster')
        marker = self.host.optional(MARKER)
        if marker != {'checkpoint_id': self.checkpoint['id']}:
            raise ValueError('Backup checkpoint fence was replaced')
        groups = [self.checkpoint['cold_workloads']]
        groups += [[item for item in self.checkpoint['workloads'] if item['wave'] == wave]
                   for wave in sorted({item['wave'] for item in self.checkpoint['workloads']})]
        for group in groups:
            for item in group:
                self.scale(item, item['replicas'], wait=False)
            for item in group:
                self.wait(item, item['replicas'])
        for item in self.checkpoint['cronjobs']:
            self.cron(item, item['suspend'])
        self.scale(self.checkpoint['edge'], self.checkpoint['edge']['replicas'])
        self.checkpoint['admission_resumed_at'] = now()
        self.checkpoint['pause_seconds'] = (datetime.fromisoformat(self.checkpoint['admission_resumed_at']) - datetime.fromisoformat(self.checkpoint['admission_paused_at'])).total_seconds()
        self.host.kube(['delete', 'configmap/' + MARKER])
        self.checkpoint['phase'] = 'resumed' if self.checkpoint.get('snapshot_completed_at') else 'snapshot-failed'
        self.save()

    def execute(self):
        try:
            self.pause()
            self.host.run('sudo /usr/local/sbin/noebs-backup snapshot ' + shlex.quote(self.path))
            self.assert_fenced()
            self.checkpoint['snapshot_completed_at'] = now()
            self.checkpoint['phase'] = 'snapshotted'
            self.save()
        finally:
            if self.host.optional(MARKER) == {'checkpoint_id': self.checkpoint['id']}:
                self.restore()
        validate_checkpoint(self.checkpoint)
        self.host.run('sudo /usr/local/sbin/noebs-backup publish ' + shlex.quote(self.path))
        self.checkpoint['phase'] = 'published'
        self.save()
        return self.checkpoint


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--key', type=Path, required=True)
    parser.add_argument('--machines', type=Path, required=True)
    parser.add_argument('--work', type=Path, required=True)
    parser.add_argument('--staging-only', action='store_true')
    parser.add_argument('--restore-checkpoint', help='restore the saved workload state after an interrupted checkpoint')
    args = parser.parse_args()
    os.umask(0o077)
    def interrupted(signum, frame):
        raise SystemExit(128 + signum)
    signal.signal(signal.SIGTERM, interrupted)
    machines = json.loads(args.machines.read_text())
    with RemoteLease(ssh_args(args.key.resolve(), machines['noebs-control']['ssh_destination'])) as lease:
        host = Host(args.key.resolve(), machines['noebs-data']['ssh_destination'], lease)
        if args.restore_checkpoint:
            if not re.fullmatch(r'/var/lib/noebs/backups/\d{8}T\d{6}Z/checkpoint\.json', args.restore_checkpoint):
                raise ValueError('Restore requires a managed checkpoint path')
            checkpoint = json.loads(host.run('sudo cat ' + shlex.quote(args.restore_checkpoint)))
            if checkpoint.get('format') != FORMAT:
                raise ValueError('Unsupported checkpoint format')
            if args.staging_only:
                release = host.optional('noebs-release')
                if not release or release.get('stage') != 'empty-staging' or host.optional('noebs-migration'):
                    raise ValueError('Backup measurement requires untouched empty staging')
            Backup(host, checkpoint, args.restore_checkpoint).restore()
            print(json.dumps({'restored_checkpoint': checkpoint['id']}))
            return
        checkpoint = inventory(host, args.staging_only)
        directory = '/var/lib/noebs/backups/' + checkpoint['id']
        host.run('sudo install -d -m 0700 /var/lib/noebs/backups; sudo mkdir -m 0700 ' + directory)
        result = Backup(host, checkpoint, directory + '/checkpoint.json').execute()
        args.work.mkdir(mode=0o700, parents=True, exist_ok=True)
        output = args.work / (checkpoint['id'] + '-checkpoint.json')
        output.write_text(json.dumps(result, indent=2) + '\n')
        print(json.dumps({'checkpoint': str(output), 'pause_seconds': result['pause_seconds']}))


if __name__ == '__main__':
    main()
