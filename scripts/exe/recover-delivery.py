#!/usr/bin/env python3
"""Restore SDK state and replay durable outboxes after PostgreSQL backup recovery."""
import argparse
import io
import json
import os
from pathlib import Path
import re
import shlex
import tarfile

from reconcile import RemoteLease, run, ssh, ssh_args
from backup_checkpoint import verify_set


def require_fenced(marker, recovery_id, resources):
    if marker.get('state') != 'destination-staged' or marker.get('migration_id') != recovery_id:
        raise ValueError('Recovery requires the matching staged destination marker')
    for item in resources:
        kind, name = item['kind'], item['metadata']['name']
        if kind == 'Deployment' and item['spec'].get('replicas', 1) != 0:
            raise ValueError('Stop deployment before delivery recovery: ' + name)
        if kind == 'CronJob' and not item['spec'].get('suspend', False):
            raise ValueError('Suspend scheduled writers before delivery recovery: ' + name)
        if kind == 'Pod' and item.get('status', {}).get('phase') not in ['Succeeded', 'Failed']:
            if name not in ['postgres-0', 'temporal-postgres-0', 'keycloak-postgres-0', 'kafka-0', 'noebs-mojaloop-redis-0']:
                raise ValueError('A runtime or maintenance pod is still active: ' + name)


def validate_cold_archive(payload):
    with tarfile.open(fileobj=io.BytesIO(payload), mode='r:gz') as archive:
        members = archive.getmembers()
        if not members:
            raise ValueError('Cold-store backup archive is empty')
        for member in members:
            path = Path(member.name)
            if path.is_absolute() or '..' in path.parts or not (member.isfile() or member.isdir()):
                raise ValueError('Cold-store archive has an unsafe filesystem entry')
    return payload


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--key', type=Path, required=True)
    parser.add_argument('--machines', type=Path, required=True)
    parser.add_argument('--archives', type=Path, required=True)
    parser.add_argument('--age', type=Path, required=True)
    parser.add_argument('--age-key', type=Path, required=True)
    parser.add_argument('--recovery-id', required=True)
    args = parser.parse_args()
    if not re.fullmatch('[A-Za-z0-9][A-Za-z0-9_.-]{0,79}', args.recovery_id):
        raise ValueError('Recovery ID must be a filename-safe identifier')
    os.umask(0o077)
    checkpoint = verify_set(args.archives)
    key = args.key.resolve()
    machines = json.loads(args.machines.read_text())
    server = machines['noebs-data']['ssh_destination']
    controller = machines['noebs-control']['ssh_destination']
    with RemoteLease(ssh_args(key, controller)) as lease:
        def kube(command, payload=None):
            lease.check()
            return ssh(key, server, 'sudo k3s kubectl -n noebs ' + shlex.join(command),
                       input=payload, capture_output=True).stdout
        marker = json.loads(kube(['get', 'configmap/noebs-migration', '-o', 'json']))['data']
        resources = json.loads(kube(['get', 'deployments,cronjobs,pods', '-o', 'json']))['items']
        require_fenced(marker, args.recovery_id, resources)
        if marker.get('backup_checkpoint') != checkpoint['id']:
            raise ValueError('PostgreSQL restore marker must identify this exact backup checkpoint')
        archives = {}
        for name, suffix in [('kafka', 'kafka'), ('redis', 'mojaloop')]:
            path = args.archives / (checkpoint['id'] + '-' + suffix + '.tar.gz.age')
            payload = run([str(args.age.resolve()), '--decrypt', '-i', str(args.age_key.resolve()),
                           str(path.resolve())], capture_output=True).stdout
            archives[name] = validate_cold_archive(payload)
        volumes = {}
        for name, claim in [('kafka', 'kafka-data-kafka-0'), ('redis', 'data-noebs-mojaloop-redis-0')]:
            pvc = json.loads(kube(['get', 'pvc/' + claim, '-o', 'json']))
            pv = json.loads(kube(['get', 'pv/' + pvc['spec']['volumeName'], '-o', 'json']))
            path = pv['spec']['hostPath']['path']
            nodes = pv['spec']['nodeAffinity']['required']['nodeSelectorTerms'][0]['matchExpressions'][0]['values']
            if not path.startswith('/var/lib/rancher/k3s/storage/') or nodes != ['noebs-data']:
                raise ValueError('Recovery volume must be on the managed data node')
            volumes[name] = path
        # Failed recovery stays fenced. PostgreSQL snapshots must already be restored.
        kube(['patch', 'configmap/noebs-migration', '--type=merge', '-p',
              json.dumps({'data': {'state': 'destination-recovery-running'}})])
        for name in ['kafka', 'noebs-mojaloop-redis']:
            kube(['patch', 'statefulset/' + name, '--type=merge', '--field-manager=noebs-release',
                  '-p', json.dumps({'spec': {'replicas': 0}})])
        pods = [item['metadata']['name'] for item in resources if item['kind'] == 'Pod'
                and item['metadata']['name'] in ['kafka-0', 'noebs-mojaloop-redis-0']]
        if pods:
            kube(['wait', '--for=delete', '--timeout=180s'] + ['pod/' + name for name in pods])
        for name, path in volumes.items():
            lease.check()
            retained = path + '.before-recovery-' + args.recovery_id
            command = 'set -eu\npath=' + shlex.quote(path) + '\nretained=' + shlex.quote(retained) + '''
test ! -e "$retained"
owner=$(stat -c '%u:%g' "$path")
mode=$(stat -c '%a' "$path")
mv -- "$path" "$retained"
install -d -m "$mode" "$path"
chown "$owner" "$path"
'''
            command += 'tar xzf - -C "$path"\n'
            ssh(key, server, 'sudo bash -c ' + shlex.quote(command), input=archives[name])
        kube(['patch', 'configmap/noebs-migration', '--type=merge', '-p',
              json.dumps({'data': {'state': 'destination-staged', 'recovery_kind': 'coordinated-backup'}})])
        print('Checkpoint Kafka and SDK cold stores restored together; runtimes remain fenced')


if __name__ == '__main__':
    main()
