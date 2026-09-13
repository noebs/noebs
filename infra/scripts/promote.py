#!/usr/bin/env python3
"""Promote verified image receipts through the existing Kubernetes sync waves."""
import argparse
import copy
import hashlib
from http.cookies import SimpleCookie
import ipaddress
import json
import os
from pathlib import Path
import re
import shlex
import tempfile
from urllib.error import HTTPError, URLError
from urllib.parse import parse_qs, urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener, urlopen

import yaml
from reconcile import ROOT, RemoteLease, run, ssh, ssh_args
from deployment import load_config, prepare_source, configure_workload
from external_transport import resources as external_transport_resources

ORIGIN = 'https://api.noebs.sd'


def require_public_dns(host):
    cname = run(['dig', '+short', 'CNAME', host], capture_output=True).stdout.decode().strip().lower()
    if cname != 'noebs-workers.exe.xyz.':
        raise ValueError('Set the DNS-only CNAME ' + host + ' -> noebs-workers.exe.xyz before deploying')


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, url):
        return None


def public_domain_registered(host):
    request = Request('https://' + host + '/test', headers={'User-Agent': 'noebs-deployment'})
    try:
        with build_opener(NoRedirect).open(request, timeout=30) as response:
            if response.status == 200:
                return True
            raise RuntimeError('Unexpected HTTPS response while verifying EXE domain registration')
    except HTTPError as response:
        try:
            # EXE serves this explicit rejection over verified HTTPS before a
            # custom domain has been registered. Other errors are not evidence
            # that an owner must register the domain again.
            if response.code == 421 and b'<title>Domain Not Configured</title>' in response.read(16384):
                return False
            raise RuntimeError('Unable to verify EXE domain registration: HTTP ' + str(response.code)) from None
        finally:
            response.close()
    except (URLError, OSError, TimeoutError):
        raise RuntimeError('Unable to verify EXE domain registration over HTTPS') from None


def ensure_public_domain(key, host):
    if not public_domain_registered(host):
        ssh(key, 'exe.dev', 'domain add noebs-workers ' + shlex.quote(host) + ' --json')


def verify_public(host, origin):
    def fetch(path):
        request = Request('https://' + host + path, headers={'User-Agent': 'noebs-deployment'})
        return urlopen(request, timeout=30)

    with fetch('/test') as response:
        if response.status != 200:
            raise RuntimeError('Public API readiness check failed')
    with fetch('/auth/realms/noebs/.well-known/openid-configuration') as response:
        if json.load(response)['issuer'] != origin + '/auth/realms/noebs':
            raise RuntimeError('Public OIDC issuer differs from release authority')
    with fetch('/.well-known/assetlinks.json') as response:
        if 'application/json' not in response.headers['Content-Type'] or not json.load(response):
            raise RuntimeError('Public Android app association is unavailable')
    try:
        with build_opener(NoRedirect).open('https://' + host + '/backoffice/login', timeout=30):
            raise RuntimeError('Public backoffice did not start its OIDC login')
    except HTTPError as response:
        verify_login_response(response.code, response.headers, origin)
    for path in ['/auth/admin/', '/auth/realms/master/.well-known/openid-configuration']:
        try:
            with fetch(path):
                raise RuntimeError('Private identity administration is publicly reachable')
        except HTTPError as error:
            if error.code != 404:
                raise RuntimeError('Unexpected response at private identity route') from None


def verify_login_response(status, headers, origin):
    location = urlsplit(headers.get('Location', ''))
    query = parse_qs(location.query)
    if (status != 303 or location.scheme + '://' + location.netloc != origin
            or location.path != '/auth/realms/noebs/protocol/openid-connect/auth'
            or query.get('client_id') != ['noebs-backoffice']
            or query.get('redirect_uri') != [origin + '/backoffice/oauth/callback']):
        raise RuntimeError('Public backoffice Host or OIDC redirect was changed by the proxy')
    cookies = SimpleCookie()
    for value in headers.get_all('Set-Cookie', []):
        cookies.load(value)
    cookie = cookies.get('__Host-noebs_backoffice_flow')
    if cookie is None or not cookie['secure'] or not cookie['httponly'] or cookie['path'] != '/' or cookie['domain']:
        raise RuntimeError('Public backoffice browser binding cookie was changed by the proxy')


def verify_receipt(path, revision):
    receipt=json.loads(path.read_text())
    if receipt['source_sha'] != revision or not re.fullmatch(r'sha256:[0-9a-f]{64}',receipt['digest']):
        raise ValueError('Image receipt does not match this source revision')
    expected_tag='ghcr.io/noebs/noebs:'+revision
    if receipt['tag'] != expected_tag:
        raise ValueError('Image receipt belongs to a different workload role')
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
    parser.add_argument('--config', type=Path, required=True)
    args=parser.parse_args()
    os.umask(0o077)
    machines=json.loads(args.machines.read_text())
    with RemoteLease(ssh_args(args.key.resolve(), machines['noebs-control']['ssh_destination'])) as lease:
        promote(args, lease)


def promote(args, lease):
    key=args.key.resolve(); machines=json.loads(args.machines.read_text())
    server=machines['noebs-data']['ssh_destination']; controller=machines['noebs-control']['ssh_destination']
    revision=run(['git','rev-parse','HEAD'],cwd=ROOT,capture_output=True).stdout.decode().strip()
    app_image=verify_receipt(args.receipts/'noebs-receipt.json',revision)
    config = load_config(args.config)
    binary=args.work.resolve()/'noebs'
    require_public_dns(config['public_host'])

    def kubectl(command, payload=None, capture=False):
        if lease: lease.check()
        return ssh(key,server,'sudo k3s kubectl '+shlex.join(command),input=payload,capture_output=capture)

    def apply(objects):
        if objects:
            payload=json.dumps({'apiVersion':'v1','kind':'List','items':objects}).encode()
            # Kubernetes admission errors can echo rejected Secret payloads.
            kubectl(['apply','--server-side','--field-manager=noebs-release','-f','-'],payload,capture=True)

    def phase(name):
        result=kubectl(['-n','noebs','get','job',name,'--ignore-not-found','-o','json'],capture=True).stdout
        if not result: return ''
        conditions=json.loads(result).get('status',{}).get('conditions',[])
        return next((item['type'] for item in conditions if item['status']=='True' and item['type'] in ['Complete','Failed']),'Running')

    ingress_crds = ['crd/' + name + '.traefik.io' for name in
                    ['ingressroutes', 'middlewares', 'serverstransports']]
    # kubectl aggregates NotFound errors for multiple missing names instead of
    # waiting for creation. Each CRD must be observed individually first.
    for crd in ingress_crds:
        kubectl(['wait', '--for=create', crd, '--timeout=300s'])
    kubectl(['wait', '--for=condition=Established', *ingress_crds, '--timeout=300s'])

    if kubectl(['-n','noebs','get','configmap','noebs-backup-checkpoint','--ignore-not-found','-o','name'],capture=True).stdout:
        raise RuntimeError('Promotion refused: a coordinated backup has not resumed')

    migration=kubectl(['-n','noebs','get','configmap','noebs-migration','--ignore-not-found','-o','json'],capture=True).stdout
    marker=json.loads(migration).get('data',{}) if migration else {}
    if marker and marker.get('state') != 'destination-active':
        raise RuntimeError('Release refused: the existing database migration is incomplete')

    nodes=json.loads(kubectl(['get','nodes','-o','json'],capture=True).stdout)['items']
    networks={node['metadata']['name']:ipaddress.IPv4Network(node['spec']['podCIDR']) for node in nodes}
    worker=machines['noebs-workers']['ssh_destination']
    target=str(networks['noebs-data'].network_address+2)
    route=json.loads(ssh(key,worker,'ip -j route get '+target,capture_output=True).stdout)[0]
    keycloak_proxy=str(ipaddress.IPv4Address(route['prefsrc']))+'/32'
    gateway_proxy=str(networks['noebs-workers'].network_address+1)+'/32'

    with tempfile.TemporaryDirectory(prefix='noebs-release-',dir=args.work.resolve()) as directory:
        work=Path(directory); source=work/'source'; source.mkdir()
        prepare_source(source, config, [keycloak_proxy])
        for name in ['release.secrets.yaml','age-key.txt','bootstrap.secrets.yaml']:
            (work/name).write_bytes(ssh(key,controller,'cat /var/lib/noebs/runtime/'+name,capture_output=True).stdout)
        os.environ['SOPS_AGE_KEY_FILE']=str(work/'age-key.txt')
        release=work/'prepared'
        run([str(binary),'prepare-kubernetes-release',str(source),str(work/'release.secrets.yaml'),str(work/'age-key.txt'),str(release)],capture_output=True)
        secrets=[item for item in yaml.safe_load_all(run([str(binary),'render-kubernetes-secrets',str(release),'noebs'],capture_output=True).stdout) if item]
        edge_secrets=[item for item in yaml.safe_load_all(run([str(binary),'render-edge-internal-transport',str(release),'noebs'],capture_output=True).stdout) if item]
        for secret in edge_secrets:
            data = secret.get('data', secret.get('stringData'))
            data['ca.crt'] = data.pop('ca.pem')
        objects=list(yaml.safe_load_all(run(['kustomize','build',str(source/'infra/kubernetes/overlays/exe')],capture_output=True).stdout))
        objects=[item for item in objects if item]
        objects.append(yaml.safe_load((release/'platform/keycloak-smtp-egress.yaml').read_text()))
        objects.extend(external_transport_resources(config['service_config'].get('wallet-worker', {})))
        configuration = (release/'config.yaml').read_bytes()
        for path in sorted((release/'services').glob('*.yaml')):
            configuration += path.name.encode() + path.read_bytes()
        fingerprint=hashlib.sha256(json.dumps(secrets+edge_secrets,sort_keys=True).encode()+configuration).hexdigest()
        job_revision=hashlib.sha256((revision+fingerprint).encode()).hexdigest()[:10]
        objects = [configure_workload(obj, app_image, revision, fingerprint) for obj in objects]
        for obj in objects:
            if obj['kind']=='NetworkPolicy' and obj['metadata']['name'] in ['api-gateway-ingress','keycloak-https-ingress']:
                peers=obj['spec']['ingress'][0]['from']
                peers[:]=[peer for peer in peers if peer.get('ipBlock',{}).get('cidr')!='10.42.0.1/32']
                # Host-network ingress uses the CNI gateway for local pods and
                # its private node address when Kubernetes selects another node.
                sources = [gateway_proxy, keycloak_proxy] if obj['metadata']['name']=='api-gateway-ingress' else [keycloak_proxy]
                peers.extend({'ipBlock':{'cidr':cidr}} for cidr in sorted(set(sources)))
            if obj['kind']=='ConfigMap' and obj['metadata']['name']=='noebs-config':
                obj['data']['config.yaml']=(release/'config.yaml').read_text()
                for path in (release/'services').glob('*.yaml'):
                    obj['data'][path.stem+'.service.yaml']=path.read_text()
        apply([{'apiVersion':'v1','kind':'Namespace','metadata':{'name':name}} for name in ['noebs']])
        bootstrapping=phase('noebs-keycloak-delete-bootstrap-client')!='Complete'
        if bootstrapping:
            bootstrap_file=work/'bootstrap-rendered.yaml'
            run([str(binary),'render-keycloak-bootstrap-secrets',str(release),'noebs',str(work/'bootstrap.secrets.yaml'),str(bootstrap_file)],capture_output=True)
            secrets.extend(yaml.safe_load_all(bootstrap_file.read_text()))
            cleanup=yaml.safe_load((ROOT/'infra/kubernetes/bootstrap/delete-bootstrap-client-job.yaml').read_text())
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
            # The bootstrap cleanup is appended after the common workload transform.
            if name == 'noebs-keycloak-delete-bootstrap-client':
                replacement = configure_workload(obj, app_image, revision, fingerprint)
                obj.update(replacement)
                template = obj['spec']['template']
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
        edge=list(yaml.safe_load_all(run(['kustomize','build',str(ROOT/'infra/kubernetes/ingress')],capture_output=True).stdout))
        apply([obj for obj in edge if obj])
        kubectl(['-n','kube-system','rollout','status','deployment/traefik','--timeout=300s'])
        pods=json.loads(kubectl(['-n','noebs','get','pods','-o','json'],capture=True).stdout)['items']
        terminating=['pod/'+pod['metadata']['name'] for pod in pods if pod['metadata'].get('deletionTimestamp')]
        if terminating:
            kubectl(['-n','noebs','wait','--for=delete','--timeout=180s']+terminating)
        kubectl(['-n','noebs','get','deployments,statefulsets,pods','-o','wide'])
        snapshots={name:json.loads(kubectl(['-n','noebs','get',resources,'-o','json'],capture=True).stdout)
                   for name,resources in [('workloads','deployments,statefulsets,jobs'),('cronjobs','cronjobs'),('pods','pods')]}
        run(['python3',str(ROOT/'scripts/alpha-workload-images.py'),app_image],input=json.dumps(snapshots).encode())
        worker=machines['noebs-workers']['ssh_destination']
        ssh(key,worker,'curl --fail --silent --show-error --connect-timeout 10 --max-time 30 -H "Host: api.noebs.sd" http://127.0.0.1:8081/test >/dev/null')
        metadata=json.loads(ssh(key,worker,'curl --fail --silent --show-error --connect-timeout 10 --max-time 30 -H "Host: api.noebs.sd" http://127.0.0.1:8081/auth/realms/noebs/.well-known/openid-configuration',capture_output=True).stdout)
        if metadata['issuer'] != ORIGIN+'/auth/realms/noebs':
            raise ValueError('Deployed OpenID issuer differs from release origin')
        # Publish only after the application, identities and routes are ready.
        ssh(key, 'exe.dev', 'share port noebs-workers 8081 --json')
        ssh(key, 'exe.dev', 'share set-public noebs-workers --json')
        ensure_public_domain(key, config['public_host'])
        verify_public(config['public_host'], ORIGIN)
        kubectl(['-n','edge','delete','deployment/caddy','--ignore-not-found','--wait=true'])
        # Retire the old embedded adapter. Persistent data is retained by its PVC.
        kubectl(['-n','noebs','delete','statefulset/noebs-mojaloop-redis',
                 'service/noebs-mojaloop-redis','service/noebs-mojaloop-sdk',
                 'networkpolicy/noebs-mojaloop-redis','networkpolicy/noebs-mojaloop-worker',
                 'secret/noebs-mojaloop-sdk','--ignore-not-found','--wait=true'])
        if not config['service_config'].get('wallet-worker', {}).get('interop_tenant'):
            kubectl(['-n','noebs','delete','service/wallet-interop-callback',
                     'networkpolicy/wallet-interop-transport','--ignore-not-found'])
        marker={'apiVersion':'v1','kind':'ConfigMap','metadata':{'name':'noebs-release','namespace':'noebs'},
                'data':{'revision':revision,'image':app_image,'origin':ORIGIN,'fingerprint':fingerprint,'stage':'production'}}
        apply([marker])
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
