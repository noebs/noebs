"""Validate the checkpoint and exact archive set of a coordinated backup."""
from datetime import datetime
import hashlib
import json
from pathlib import Path
import re
import sys

FORMAT = 'noebs.coordinated-backup.v2'
ARCHIVES = {'postgres.sql.gz.age', 'temporal.sql.gz.age', 'keycloak.sql.gz.age',
            'kafka.tar.gz.age', 'kubernetes.json.gz.age',
            'control-plane.tar.gz.age', 'checkpoint.json'}


def validate_checkpoint(checkpoint):
    if (checkpoint.get('format') != FORMAT or checkpoint.get('phase') not in ['snapshotted', 'resumed', 'published']
            or not re.fullmatch(r'\d{8}T\d{6}Z', checkpoint.get('id', ''))
            or not checkpoint.get('cluster_uid') or not checkpoint.get('fenced_at')
            or not checkpoint.get('snapshot_completed_at')
            or set(checkpoint.get('cold_volumes', {})) != {'kafka'}):
        raise ValueError('Backup lacks a completed coordinated checkpoint')
    if datetime.fromisoformat(checkpoint['fenced_at']) > datetime.fromisoformat(checkpoint['snapshot_completed_at']):
        raise ValueError('Snapshot predates its writer fence')
    return checkpoint


def manifest_entries(payload, stamp):
    expected = {stamp + '-' + suffix for suffix in ARCHIVES}
    entries = {}
    for line in payload.decode().splitlines():
        checksum, name = line.split()
        name = name.removeprefix('./')
        if name not in expected or name in entries or not re.fullmatch('[0-9a-f]{64}', checksum):
            raise ValueError('Backup manifest contains an unexpected archive')
        entries[name] = checksum
    if entries.keys() != expected:
        raise ValueError('Backup manifest is incomplete or predates coordinated snapshots')
    return entries


def verify_set(directory):
    manifests = list(directory.glob('*-SHA256SUMS'))
    if len(manifests) != 1:
        raise ValueError('Expected exactly one coordinated backup set')
    stamp = manifests[0].name.removesuffix('-SHA256SUMS')
    entries = manifest_entries(manifests[0].read_bytes(), stamp)
    for name, checksum in entries.items():
        if hashlib.sha256((directory / name).read_bytes()).hexdigest() != checksum:
            raise ValueError('Backup checksum mismatch: ' + name)
    checkpoint = validate_checkpoint(json.loads((directory / (stamp + '-checkpoint.json')).read_text()))
    if checkpoint['id'] != stamp:
        raise ValueError('Backup checkpoint belongs to a different archive set')
    return checkpoint


if __name__ == '__main__':
    mode, filename = sys.argv[1:]
    value = json.loads(Path(filename).read_text())
    if mode == 'snapshot':
        if (value.get('format') != FORMAT or value.get('phase') != 'fenced' or not value.get('fenced_at')
                or not re.fullmatch(r'\d{8}T\d{6}Z', value.get('id', ''))):
            raise ValueError('Snapshot requires an active coordinated writer fence')
        for name in ['kafka']:
            if not value['cold_volumes'][name].startswith('/var/lib/rancher/k3s/storage/'):
                raise ValueError('Cold snapshot volume is outside managed storage')
    elif mode == 'publish':
        validate_checkpoint(value)
        if value['phase'] != 'resumed':
            raise ValueError('Publish only after admission has resumed')
    else:
        raise ValueError('Unknown checkpoint operation')
