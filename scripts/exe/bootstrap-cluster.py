#!/usr/bin/env python3
import argparse
import concurrent.futures
import json
import os
from pathlib import Path
import shlex

from reconcile import ROOT, RemoteLease, ssh, ssh_args


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--key', type=Path, required=True)
    parser.add_argument('--machines', type=Path, required=True)
    args=parser.parse_args()
    machines=json.loads(args.machines.read_text())
    key=args.key.resolve()
    with RemoteLease(ssh_args(key,machines['noebs-control']['ssh_destination'])) as lease:
        bootstrap(key,machines,lease)


def bootstrap(key,machines,lease):
    marker=ssh(key,machines['noebs-data']['ssh_destination'],
        'if command -v k3s >/dev/null && sudo test -s /var/lib/rancher/k3s/server/db/state.db; then sudo k3s kubectl -n noebs get configmap noebs-migration --ignore-not-found -o json; fi',capture_output=True).stdout
    if marker and json.loads(marker)['data']['state']!='destination-active':
        raise RuntimeError('Cluster bootstrap refused while migration or recovery is fenced')
    checkpoint=ssh(key,machines['noebs-data']['ssh_destination'],
        'if command -v k3s >/dev/null && sudo test -s /var/lib/rancher/k3s/server/db/state.db; then sudo k3s kubectl -n noebs get configmap noebs-backup-checkpoint --ignore-not-found -o name; fi',capture_output=True).stdout
    if checkpoint:
        raise RuntimeError('Cluster bootstrap refused: a coordinated backup has not resumed')
    script=(ROOT/'scripts/exe/node-init.sh').read_bytes()
    receipt=ssh(key,machines['noebs-control']['ssh_destination'],
                'if test -f /var/lib/noebs/runtime/deployment.json; then cat /var/lib/noebs/runtime/deployment.json; fi',
                capture_output=True).stdout
    if receipt:
        authority=json.loads(receipt)
        paths=authority['postgres_volume_paths']
        if len(paths)!=3 or any(not path.startswith('/var/lib/rancher/k3s/storage/') for path in paths):
            raise ValueError('Controller deployment receipt has an invalid authority volume inventory')
        checks=['/var/lib/rancher/k3s/server/db/state.db']+[path+'/PG_VERSION' for path in paths]
        command=' && '.join('sudo test -s '+shlex.quote(path) for path in checks)
        ssh(key,machines['noebs-data']['ssh_destination'],command)


    def initialize(name, phase, server_ip='', token=''):
        lease.check()
        vm=machines[name]
        ssh(key, vm['ssh_destination'], 'sudo install -d -m 0700 /opt/noebs; sudo tee /opt/noebs/node-init.sh >/dev/null', input=script)
        command='sudo bash /opt/noebs/node-init.sh '+shlex.join([name,vm['role'],server_ip,phase])
        ssh(key, vm['ssh_destination'], command, input=json.dumps({'auth_key':os.environ.get('EXE_TAILSCALE_AUTH_KEY',''),'k3s_token':token}).encode())

    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
        results=[pool.submit(initialize, name, 'network') for name in ['noebs-data','noebs-backup','noebs-workers']]
        for result in results: result.result()
    initialize('noebs-data','cluster')
    server=machines['noebs-data']['ssh_destination']
    address=ssh(key, server, 'tailscale ip -4', capture_output=True).stdout.decode().strip()
    token=ssh(key, server, 'sudo cat /var/lib/rancher/k3s/server/node-token', capture_output=True).stdout.decode().strip()
    initialize('noebs-workers', 'cluster', address, token)
    ssh(key, server, 'sudo k3s kubectl wait --for=condition=Ready nodes --all --timeout=180s && sudo k3s kubectl get nodes -o wide')


if __name__=='__main__': main()
