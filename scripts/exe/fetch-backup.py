#!/usr/bin/env python3
"""Fetch the latest complete encrypted backup and verify every archived checksum."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shlex

from reconcile import ssh


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--key', type=Path, required=True)
    parser.add_argument('--machines', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    os.umask(0o077)
    args.output.mkdir(mode=0o700, parents=True)
    machines = json.loads(args.machines.read_text())
    backup = machines['noebs-backup']['ssh_destination']
    controller = machines['noebs-control']['ssh_destination']
    command = "sudo find /var/lib/noebs-backup/data -maxdepth 1 -type f -name '*-SHA256SUMS' -printf '%f\\n' | sort | tail -1"
    manifest_name = ssh(args.key, backup, command, capture_output=True).stdout.decode().strip()
    if not re.fullmatch(r'\d{8}T\d{6}Z-SHA256SUMS', manifest_name):
        raise ValueError('No complete backup manifest is available')
    directory = '/var/lib/noebs-backup/data/'
    manifest = ssh(args.key, backup, 'sudo cat ' + directory + manifest_name, capture_output=True).stdout
    stamp = manifest_name.removesuffix('-SHA256SUMS')
    expected = {stamp + '-' + suffix for suffix in ['postgres.sql.gz.age', 'temporal.sql.gz.age',
                'keycloak.sql.gz.age', 'mojaloop.rdb.age', 'kubernetes.json.gz.age', 'control-plane.tar.gz.age']}
    seen = set()
    for line in manifest.decode().splitlines():
        checksum, name = line.split()
        name = name.removeprefix('./')
        if name not in expected or name in seen or not re.fullmatch('[0-9a-f]{64}', checksum):
            raise ValueError('Backup manifest contains an unexpected archive')
        payload = ssh(args.key, backup, 'sudo cat ' + shlex.quote(directory + name), capture_output=True).stdout
        if hashlib.sha256(payload).hexdigest() != checksum:
            raise ValueError('Backup checksum mismatch: ' + name)
        (args.output / name).write_bytes(payload)
        seen.add(name)
    if seen != expected:
        raise ValueError('Backup manifest is incomplete')
    (args.output / manifest_name).write_bytes(manifest)
    key = ssh(args.key, controller, 'cat /var/lib/noebs/runtime/age-key.txt', capture_output=True).stdout
    (args.output / 'age-key.txt').write_bytes(key)
    print('Verified encrypted backup ' + stamp)


if __name__ == '__main__':
    main()
