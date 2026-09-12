#!/usr/bin/env python3
"""Promote verified image receipts through the existing Kubernetes sync waves."""
import argparse
import copy
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import tempfile

import yaml
from reconcile import ROOT, RemoteLease, run, ssh, ssh_args

ORIGIN = 'https://api.noebs.sd'


def sdk_secret(inputs):
    if not isinstance(inputs, dict) or set(inputs) != {'ilp_secret'}:
        raise ValueError('Mojaloop credentials require exactly ilp_secret')
    secret = inputs['ilp_secret']
    if not isinstance(secret, str) or not secret or secret != secret.strip():
        raise ValueError('Mojaloop ilp_secret must be explicit and canonical')
    return {'apiVersion': 'v1', 'kind': 'Secret',
            'metadata': {'name': 'noebs-mojaloop-sdk', 'namespace': 'noebs'},
            'type': 'Opaque', 'stringData': {'ilp-secret': secret}}


def verify_receipt(path, revision, role):
    receipt=json.loads(path.read_text())
    if receipt['source_sha'] != revision or not re.fullmatch(r'sha256:[0-9a-f]{64}',receipt['digest']):
        raise ValueError('Image receipt does not match this source revision')
    expected_tag='ghcr.io/noebs/noebs:'+('mojaloop-sdk-' if role=='sdk' else '')+revision
    if receipt['tag'] != expected_tag:
        raise ValueError('Image receipt belongs to a different workload role')
    if role=='sdk' and (receipt['profile']!='sdg-msisdn-v1' or receipt['upstream']!='6594dc5689a95dffece23d481a407e4368d80a96'):
        raise ValueError('SDK receipt differs from the pinned integration profile')
    reference='ghcr.io/noebs/noebs@'+receipt['digest']
    if receipt['digest_ref'] != reference:
        raise ValueError('Image receipt names an unexpected registry')
    manifest=run(['crane','manifest',reference],capture_output=True).stdout
    if 'sha256:'+hashlib.sha256(manifest).hexdigest() != receipt['digest']:
        raise ValueError('Registry manifest does not match the image receipt')
    return reference


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--key',type=Path,required=True)
    parser.add_argument('--machines',type=Path,required=True)
    parser.add_argument('--receipts',type=Path,required=True)
    parser.add_argument('--work',type=Path,required=True)
    parser.add_argument('--migration-id')
    args=parser.parse_args()
    os.umask(0o077)
    inherited=args.migration_id and os.environ.get('NOEBS_RELEASE_LEASE')==args.migration_id
    if inherited:
        promote(args, None)
    else:
        machines=json.loads(args.machines.read_text())
        with RemoteLease(ssh_args(args.key.resolve(),machines['noebs-control']['ssh_destination'])) as lease:
            promote(args, lease)


def promote(args, lease):
    key=args.key.resolve(); machines=json.loads(args.machines.read_text())
    server=machines['noebs-data']['ssh_destination']; controller=machines['noebs-control']['ssh_destination']
    revision=run(['git','rev-parse','HEAD'],cwd=ROOT,capture_output=True).stdout.decode().strip()
    app_image=verify_receipt(args.receipts/'noebs-receipt.json',revision,'app')
    sdk_image=verify_receipt(args.receipts/'sdk-receipt.json',revision,'sdk')
    binary=args.work.resolve()/'noebs'
    run(['go','build','-trimpath','-o',str(binary),'./cli'],cwd=ROOT)

    def kubectl(command, payload=None, capture=False):
        if lease: lease.check()
        return ssh(key,server,'sudo k3s kubectl '+shlex.join(command),input=payload,capture_output=capture)

    def apply(objects):
        payload=json.dumps({'apiVersion':'v1','kind':'List','items':objects}).encode()
        kubectl(['apply','--server-side','--field-manager=noebs-release','-f','-'],payload)

    def phase(name):
        result=kubectl(['-n','noebs','get','job',name,'--ignore-not-found','-o','json'],capture=True).stdout
        if not result: return ''
        conditions=json.loads(result).get('status',{}).get('conditions',[])
        return next((item['type'] for item in conditions if item['status']=='True' and item['type'] in ['Complete','Failed']),'Running')

    migration=kubectl(['-n','noebs','get','configmap','noebs-migration','--ignore-not-found','-o','json'],capture=True).stdout
    marker=json.loads(migration).get('data',{}) if migration else {}
    if marker.get('state')=='source-fenced':
        raise RuntimeError('Promotion refused: this cluster is fenced as the migration source')
    if marker.get('state')=='destination-staged':
        if not args.migration_id or args.migration_id!=marker.get('migration_id'):
            raise RuntimeError('Promotion requires the matching staged --migration-id')
    elif args.migration_id:
        raise RuntimeError('Migration activation requires a staged destination marker')
    elif marker and marker.get('state')!='destination-active':
        raise RuntimeError('Promotion refused: unknown migration state')

    nodes=json.loads(kubectl(['get','nodes','-o','json'],capture=True).stdout)['items']
    networks={node['metadata']['name']:ipaddress.IPv4Network(node['spec']['podCIDR']) for node in nodes}
    worker=machines['noebs-workers']['ssh_destination']
    target=str(networks['noebs-data'].network_address+2)
    route=json.loads(ssh(key,worker,'ip -j route get '+target,capture_output=True).stdout)[0]
    keycloak_proxy=str(ipaddress.IPv4Address(route['prefsrc']))+'/32'
    gateway_proxy=str(networks['noebs-workers'].network_address+1)+'/32'

    with tempfile.TemporaryDirectory(prefix='noebs-release-',dir=args.work.resolve()) as directory:
        work=Path(directory); source=work/'source'; source.mkdir()
        shutil.copytree(ROOT/'deploy',source/'deploy')
        config_path=source/'deploy/kubernetes/base/configmap.yaml'
        source_config=yaml.safe_load(config_path.read_text())
        config=yaml.safe_load(source_config['data']['config.yaml'])
        config['noebs']['keycloak_proxy_trusted_addresses']=keycloak_proxy
        source_config['data']['config.yaml']=yaml.safe_dump(config,sort_keys=False)
        config_path.write_text(yaml.safe_dump(source_config,sort_keys=False))
        for name in ['release.secrets.yaml','age-key.txt','bootstrap.secrets.yaml','mojaloop.secrets.yaml']:
            (work/name).write_bytes(ssh(key,controller,'cat /var/lib/noebs/runtime/'+name,capture_output=True).stdout)
        os.environ['SOPS_AGE_KEY_FILE']=str(work/'age-key.txt')
        release=work/'prepared'
        run([str(binary),'prepare-kubernetes-release',str(source),str(work/'release.secrets.yaml'),str(work/'age-key.txt'),str(release)])
        secrets=[item for item in yaml.safe_load_all(run([str(binary),'render-kubernetes-secrets',str(release),'noebs'],capture_output=True).stdout) if item]
        secrets.append(sdk_secret(json.loads(run(['sops','--decrypt','--output-type','json',str(work/'mojaloop.secrets.yaml')],capture_output=True).stdout)))
        edge_secrets=[item for item in yaml.safe_load_all(run([str(binary),'render-edge-internal-transport',str(release),'edge'],capture_output=True).stdout) if item]
        objects=list(yaml.safe_load_all(run(['kustomize','build',str(source/'deploy/kubernetes/overlays/exe')],capture_output=True).stdout))
        objects=[item for item in objects if item]
        fingerprint=hashlib.sha256(json.dumps(secrets,sort_keys=True).encode()).hexdigest()
        job_revision=hashlib.sha256((revision+fingerprint+(args.migration_id or '')).encode()).hexdigest()[:10]
        for obj in objects:
            if obj['kind']=='NetworkPolicy' and obj['metadata']['name'] in ['api-gateway-ingress','keycloak-https-ingress']:
                peers=obj['spec']['ingress'][0]['from']
                peers[:]=[peer for peer in peers if peer.get('ipBlock',{}).get('cidr')!='10.42.0.1/32']
                peers.append({'ipBlock':{'cidr':gateway_proxy if obj['metadata']['name']=='api-gateway-ingress' else keycloak_proxy}})
            if obj['kind']=='ConfigMap' and obj['metadata']['name']=='noebs-config':
                obj['data']['config.yaml']=(release/'config.yaml').read_text()
                for path in (release/'services').glob('*.yaml'):
                    obj['data'][path.stem+'.service.yaml']=path.read_text()
        apply([{'apiVersion':'v1','kind':'Namespace','metadata':{'name':name}} for name in ['noebs','edge']])
        bootstrapping=phase('noebs-keycloak-delete-bootstrap-client')!='Complete'
        if bootstrapping:
            bootstrap_file=work/'bootstrap-rendered.yaml'
            run([str(binary),'render-keycloak-bootstrap-secrets',str(release),'noebs',str(work/'bootstrap.secrets.yaml'),str(bootstrap_file)])
            secrets.extend(yaml.safe_load_all(bootstrap_file.read_text()))
            cleanup=yaml.safe_load((ROOT/'deploy/kubernetes/overlays/bootstrap-current-host/delete-bootstrap-client-job.yaml').read_text())
            cleanup['metadata']['namespace']='noebs'
            # This completed Job is the durable bootstrap marker; do not expire it.
            cleanup['spec'].pop('ttlSecondsAfterFinished',None)
            objects.append(cleanup)
        apply(secrets+edge_secrets)
        if not bootstrapping:
            kubectl(['-n','noebs','delete','secret','keycloak-bootstrap-admin','keycloak-bootstrap-reconciler-credentials','--ignore-not-found'])
        steady_keycloak=None
        for obj in objects:
            kind=obj['kind']; name=obj['metadata']['name']
            if kind not in ['Deployment','StatefulSet','Job','CronJob']: continue
            if kind=='CronJob': obj['spec']['suspend']=False
            template=obj['spec']['jobTemplate']['spec']['template'] if kind=='CronJob' else obj['spec']['template']
            template['metadata'].setdefault('annotations',{})['noebs.dev/release']=revision
            template['metadata']['annotations']['noebs.dev/config']=fingerprint
            for container in template['spec'].get('containers',[])+template['spec'].get('initContainers',[]):
                if container['name']=='mojaloop-sdk': container['image']=sdk_image
                elif container['image'].startswith(('ghcr.io/noebs/noebs','noebs-bootstrap:')): container['image']=app_image
            if kind=='Deployment' and name=='keycloak' and bootstrapping:
                steady_keycloak=copy.deepcopy(obj)
                template['spec']['containers'][0]['env']=[{'name':'KC_BOOTSTRAP_ADMIN_CLIENT_ID','value':'noebs-keycloak-bootstrap'},{'name':'KC_BOOTSTRAP_ADMIN_CLIENT_SECRET','valueFrom':{'secretKeyRef':{'name':'keycloak-bootstrap-admin','key':'client-secret'}}}]
            if kind=='Job' and name=='noebs-keycloak-reconciler' and bootstrapping:
                for volume in template['spec']['volumes']:
                    if volume['name']=='credentials': volume['secret']['secretName']='keycloak-bootstrap-reconciler-credentials'
            if kind=='Job' and name!='noebs-keycloak-delete-bootstrap-client':
                obj['metadata']['name']=name+'-'+job_revision
        waves=sorted({int(obj['metadata'].get('annotations',{}).get('argocd.argoproj.io/sync-wave','0')) for obj in objects})
        for wave in waves:
            batch=[obj for obj in objects if int(obj['metadata'].get('annotations',{}).get('argocd.argoproj.io/sync-wave','0'))==wave]
            print('Applying release wave',wave,flush=True)
            for obj in batch:
                if obj['kind']=='Job' and phase(obj['metadata']['name'])=='Failed':
                    kubectl(['-n','noebs','delete','job',obj['metadata']['name'],'--wait=true'])
            apply(batch)
            for obj in batch:
                kind=obj['kind']; name=obj['metadata']['name']
                if kind in ['Deployment','StatefulSet']:
                    kubectl(['-n','noebs','rollout','status',kind.lower()+'/'+name,'--timeout=600s'])
                elif kind=='Job':
                    kubectl(['-n','noebs','wait','--for=condition=Complete','job/'+name,'--timeout=600s'])
            if wave==6 and steady_keycloak:
                apply([steady_keycloak])
                kubectl(['-n','noebs','rollout','status','deployment/keycloak','--timeout=300s'])
                kubectl(['-n','noebs','delete','secret','keycloak-bootstrap-admin','keycloak-bootstrap-reconciler-credentials'])
        edge=list(yaml.safe_load_all(run(['kustomize','build',str(ROOT/'deploy/kubernetes/overlays/exe-edge')],capture_output=True).stdout))
        apply([obj for obj in edge if obj])
        kubectl(['-n','edge','rollout','status','deployment/caddy','--timeout=300s'])
        ssh(key,'exe.dev','share port noebs-workers 8080 --json')
        kubectl(['-n','noebs','get','deployments,statefulsets,pods','-o','wide'])
        snapshots={name:json.loads(kubectl(['-n','noebs','get',resources,'-o','json'],capture=True).stdout)
                   for name,resources in [('workloads','deployments,statefulsets,jobs'),('cronjobs','cronjobs'),('pods','pods')]}
        run(['python3',str(ROOT/'scripts/alpha-workload-images.py'),app_image,sdk_image],input=json.dumps(snapshots).encode())
        worker=machines['noebs-workers']['ssh_destination']
        ssh(key,worker,'curl --fail --silent --show-error --connect-timeout 10 --max-time 30 -H "Host: api.noebs.sd" http://127.0.0.1:8080/test >/dev/null')
        metadata=json.loads(ssh(key,worker,'curl --fail --silent --show-error --connect-timeout 10 --max-time 30 -H "Host: api.noebs.sd" http://127.0.0.1:8080/auth/realms/noebs/.well-known/openid-configuration',capture_output=True).stdout)
        if metadata['issuer'] != ORIGIN+'/auth/realms/noebs':
            raise ValueError('Deployed OpenID issuer differs from release origin')
        stage='production' if args.migration_id or marker.get('state')=='destination-active' else 'empty-staging'
        marker={'apiVersion':'v1','kind':'ConfigMap','metadata':{'name':'noebs-release','namespace':'noebs'},'data':{'revision':revision,'image':app_image,'sdk_image':sdk_image,'origin':ORIGIN,'fingerprint':fingerprint,'stage':stage}}
        apply([marker])
        if args.migration_id:
            kubectl(['-n','noebs','patch','configmap','noebs-migration','--type=merge','--field-manager=noebs-release',
                     '-p',json.dumps({'data':{'state':'destination-active'}})])
        claims=['postgres-data-postgres-0','temporal-postgres-data-temporal-postgres-0','keycloak-postgres-data-keycloak-postgres-0']
        paths=[]
        for claim in claims:
            pvc=json.loads(kubectl(['-n','noebs','get','pvc/'+claim,'-o','json'],capture=True).stdout)
            pv=json.loads(kubectl(['get','pv/'+pvc['spec']['volumeName'],'-o','json'],capture=True).stdout)
            paths.append(pv['spec']['hostPath']['path'])
        receipt=marker['data']|{'postgres_volume_paths':paths}
        ssh(key,controller,'umask 077; cat > /var/lib/noebs/runtime/deployment.json',input=json.dumps(receipt).encode())
        (args.work/'release-receipt.json').write_text(json.dumps(receipt,indent=2)+'\n')


if __name__=='__main__': main()
