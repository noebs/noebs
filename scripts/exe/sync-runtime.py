#!/usr/bin/env python3
"""Install checked-in encrypted deployment inputs on the fleet controller."""
import argparse
import json
import os
from pathlib import Path

from reconcile import ROOT, RemoteLease, run, ssh, ssh_args


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--key', type=Path, required=True)
    parser.add_argument('--machines', type=Path, required=True)
    parser.add_argument('--age-key', type=Path, required=True)
    args = parser.parse_args()
    controller = json.loads(args.machines.read_text())['noebs-control']['ssh_destination']
    files = {'age-key.txt': args.age_key.read_bytes()}
    for name in ['release.secrets.yaml', 'bootstrap.secrets.yaml', 'mojaloop.secrets.yaml']:
        path = ROOT / 'deploy/exe' / name
        run(['sops', '--decrypt', '--output-type', 'json', str(path)],
            env=os.environ | {'SOPS_AGE_KEY_FILE': str(args.age_key.resolve())}, capture_output=True)
        files[name] = path.read_bytes()
    with RemoteLease(ssh_args(args.key.resolve(), controller)) as lease:
        ssh(args.key.resolve(), controller, 'install -d -m 0700 /var/lib/noebs/runtime')
        for name, payload in files.items():
            lease.check()
            path = '/var/lib/noebs/runtime/' + name
            ssh(args.key.resolve(), controller,
                'umask 077; cat > ' + path + '.incoming && mv ' + path + '.incoming ' + path,
                input=payload)
    print('Encrypted runtime inputs installed from this revision')


if __name__ == '__main__':
    main()
