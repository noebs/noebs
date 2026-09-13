#!/usr/bin/env python3
import argparse
import concurrent.futures
import json
import os
from pathlib import Path
import re
import shlex

import yaml

from reconcile import ROOT, RemoteLease, ssh, ssh_args


def fleet_roles(machines):
    """Validate the inventory before selecting any remote mutation target."""
    roles = {role: [] for role in ('control', 'data', 'workers', 'backup', 'telegram')}
    destinations = set()
    if not isinstance(machines, dict) or not machines:
        raise ValueError('A nonempty fleet inventory is required')
    for name, machine in machines.items():
        if not isinstance(name, str) or not re.fullmatch(r'noebs-[a-z0-9-]+', name):
            raise ValueError('Fleet machine names must start with noebs-')
        if (not isinstance(machine, dict) or not isinstance(machine.get('role'), str)
                or machine['role'] not in roles):
            raise ValueError(f'{name} requires a supported fleet role')
        destination = machine.get('ssh_destination')
        if not isinstance(destination, str) or not re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9@+._-]*', destination):
            raise ValueError(f'{name} requires an explicit SSH destination')
        if destination in destinations:
            raise ValueError('Fleet SSH destinations must be distinct')
        destinations.add(destination)
        roles[machine['role']].append(name)
    if len(roles['control']) != 1 or len(roles['data']) != 1 or not roles['workers']:
        raise ValueError('The fleet requires one control host, one data server and at least one worker')
    return {role: sorted(names) for role, names in roles.items()}


def validate_ingress_config(content):
    if not isinstance(content, str) or not content.strip():
        raise ValueError('A rendered Traefik HelmChartConfig is required')
    try:
        config = yaml.safe_load(content)
    except yaml.YAMLError as error:
        raise ValueError('Ingress configuration must be valid YAML') from error
    if (not isinstance(config, dict) or config.get('apiVersion') != 'helm.cattle.io/v1'
            or config.get('kind') != 'HelmChartConfig'
            or not isinstance(config.get('metadata'), dict)
            or not isinstance(config.get('spec'), dict)
            or config.get('metadata', {}).get('name') != 'traefik'
            or config.get('metadata', {}).get('namespace') != 'kube-system'
            or not isinstance(config.get('spec', {}).get('valuesContent'), str)
            or not config['spec']['valuesContent'].strip()):
        raise ValueError('Ingress configuration must be the traefik HelmChartConfig in kube-system')
    try:
        values = yaml.safe_load(config['spec']['valuesContent'])
    except yaml.YAMLError as error:
        raise ValueError('Traefik chart values must be valid YAML') from error
    if not isinstance(values, dict):
        raise ValueError('Traefik chart values must be a mapping')


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--key', type=Path, required=True)
    parser.add_argument('--machines', type=Path, required=True)
    parser.add_argument('--ingress-config', type=Path, required=True)
    args=parser.parse_args()
    machines=json.loads(args.machines.read_text())
    roles = fleet_roles(machines)
    ingress_config = args.ingress_config.read_text()
    validate_ingress_config(ingress_config)
    key=args.key.resolve()
    with RemoteLease(ssh_args(key,machines[roles['control'][0]]['ssh_destination'])) as lease:
        bootstrap(key,machines,lease,ingress_config)


def bootstrap(key,machines,lease,ingress_config):
    roles = fleet_roles(machines)
    validate_ingress_config(ingress_config)
    server_name = roles['data'][0]
    server = machines[server_name]['ssh_destination']
    controller = machines[roles['control'][0]]['ssh_destination']
    lease.check()
    marker=ssh(key,server,
        'if command -v k3s >/dev/null && sudo test -s /var/lib/rancher/k3s/server/db/state.db; then sudo k3s kubectl -n noebs get configmap noebs-migration --ignore-not-found -o json; fi',capture_output=True).stdout
    if marker and json.loads(marker)['data']['state']!='destination-active':
        raise RuntimeError('Cluster bootstrap refused while migration or recovery is fenced')
    checkpoint=ssh(key,server,
        'if command -v k3s >/dev/null && sudo test -s /var/lib/rancher/k3s/server/db/state.db; then sudo k3s kubectl -n noebs get configmap noebs-backup-checkpoint --ignore-not-found -o name; fi',capture_output=True).stdout
    if checkpoint:
        raise RuntimeError('Cluster bootstrap refused: a coordinated backup has not resumed')
    script=(ROOT/'infra/scripts/node-init.sh').read_bytes()
    receipt=ssh(key,controller,
                'if test -f /var/lib/noebs/runtime/deployment.json; then cat /var/lib/noebs/runtime/deployment.json; fi',
                capture_output=True).stdout
    if receipt:
        authority=json.loads(receipt)
        paths=authority['postgres_volume_paths']
        if len(paths)!=3 or any(not path.startswith('/var/lib/rancher/k3s/storage/') for path in paths):
            raise ValueError('Controller deployment receipt has an invalid authority volume inventory')
        checks=['/var/lib/rancher/k3s/server/db/state.db']+[path+'/PG_VERSION' for path in paths]
        command=' && '.join('sudo test -s '+shlex.quote(path) for path in checks)
        ssh(key,server,command)


    def initialize(name, phase, server_ip='', token=''):
        lease.check()
        vm=machines[name]
        ssh(key, vm['ssh_destination'], 'sudo install -d -m 0700 /opt/noebs; sudo tee /opt/noebs/node-init.sh >/dev/null', input=script)
        command='sudo bash /opt/noebs/node-init.sh '+shlex.join([name,vm['role'],server_ip,phase])
        ssh(key, vm['ssh_destination'], command, input=json.dumps({
            'auth_key':os.environ.get('EXE_TAILSCALE_AUTH_KEY',''),
            'k3s_token':token,
            'ingress_config':ingress_config if vm['role']=='data' and phase=='cluster' else '',
        }).encode())

    network_nodes = roles['data'] + roles['workers'] + roles['backup']
    with concurrent.futures.ThreadPoolExecutor(max_workers=min(8,len(network_nodes))) as pool:
        results=[pool.submit(initialize, name, 'preflight') for name in network_nodes]
        for result in results: result.result()
    with concurrent.futures.ThreadPoolExecutor(max_workers=min(8,len(network_nodes))) as pool:
        results=[pool.submit(initialize, name, 'network') for name in network_nodes]
        for result in results: result.result()
    initialize(server_name,'cluster')
    address=ssh(key, server, 'tailscale ip -4', capture_output=True).stdout.decode().strip()
    token=ssh(key, server, 'sudo cat /var/lib/rancher/k3s/server/node-token', capture_output=True).stdout.decode().strip()
    with concurrent.futures.ThreadPoolExecutor(max_workers=min(8,len(roles['workers']))) as pool:
        results = [pool.submit(initialize, name, 'cluster', address, token) for name in roles['workers']]
        for result in results: result.result()
    lease.check()
    targets = ['node/'+name for name in roles['data']+roles['workers']]
    ssh(key, server, 'sudo k3s kubectl wait --for=condition=Ready '+shlex.join(targets)+
        ' --timeout=180s && sudo k3s kubectl get nodes -o wide')


if __name__=='__main__': main()
