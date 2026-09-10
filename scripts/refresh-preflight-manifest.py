#!/usr/bin/env python3
"""Validate a configuration-only promotion against the existing secret set.

Run on the deployment host as root with a reviewed kustomize render in JSON.
Private payloads remain in a temporary mode0700 directory on that host. Existing
secret hashes must match the current release before any new fingerprint is made.
The exact new image validates the complete proposed payload with network disabled.
"""
import argparse
import base64
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import yaml

def kubectl(*args, payload=None):
    return subprocess.run(['k3s','kubectl','-n','noebs',*args], input=payload, text=True, capture_output=True, check=True).stdout

def digest(payload): return 'sha256:' + hashlib.sha256(payload).hexdigest()

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('render')
    parser.add_argument('--apply',action='store_true')
    args=parser.parse_args()
    objects=json.loads(Path(args.render).read_text())
    job=next(o for o in objects if o['kind']=='Job' and o['metadata']['name']=='noebs-deployment-preflight')
    spec=job['spec']['template']['spec']; container=spec['containers'][0]
    image=container['image']
    assert image.startswith('ghcr.io/noebs/noebs@sha256:') and len(image.rsplit(':',1)[1])==64
    volumes={v['name']:v for v in spec['volumes']}
    configs={o['metadata']['name']:o for o in objects if o['kind']=='ConfigMap'}
    old_secret=json.loads(kubectl('get','secret','noebs-release-manifest','-o','json'))
    original=yaml.safe_load(base64.b64decode(old_secret['data']['release-manifest.yaml']))
    current={}; proposed={}; cache={}
    for mount in container['volumeMounts']:
        name=mount['mountPath'].removeprefix('/preflight/')
        if name=='release-manifest.yaml':continue
        assert not name.startswith('/') and '..' not in Path(name).parts
        volume=volumes[mount['name']]
        kind='secret' if 'secret' in volume else 'configmap'
        key=volume['secret']['secretName'] if kind=='secret' else volume['configMap']['name']
        if (kind,key) not in cache:cache[kind,key]=json.loads(kubectl('get',kind,key,'-o','json'))
        obj=cache[kind,key]; field=mount['subPath']
        data=obj['data'][field]
        current[name]=base64.b64decode(data) if kind=='secret' else data.encode()
        proposed[name]=configs[key]['data'][field].encode() if kind=='configmap' and key in configs else current[name]
    assert {k:digest(v) for k,v in current.items()}==original['artifacts'], 'Existing release artifacts drifted; reconcile before promotion'
    artifacts={k:digest(v) for k,v in proposed.items()}
    hashed=hashlib.sha256()
    for name in sorted(artifacts):hashed.update((name+'\0'+artifacts[name].removeprefix('sha256:')+'\n').encode())
    manifest={'api_version':'noebs.sd/kubernetes-release/v1','fingerprint':'sha256:'+hashed.hexdigest(),'artifacts':artifacts}
    raw=yaml.safe_dump(manifest,sort_keys=True).encode()
    with tempfile.TemporaryDirectory(prefix='noebs-interop-preflight-') as directory:
        root=Path(directory)
        for name,data in proposed.items():
            path=root/name;path.parent.mkdir(parents=True,exist_ok=True);path.write_bytes(data);path.chmod(0o600)
        (root/'release-manifest.yaml').write_bytes(raw)
        subprocess.run(['docker','pull',image],stdout=subprocess.DEVNULL,check=True)
        result=subprocess.run(['docker','run','--rm','--network=none','--user','0','--read-only','--entrypoint','/usr/local/bin/noebs','-v',str(root)+':/preflight:ro',image,'validate-kubernetes-deployment','/preflight'],capture_output=True,text=True)
        if result.returncode:
            # Validation errors are bounded; application utilities do not print
            # secret values. Never emit the materialized payload itself.
            raise RuntimeError('Proposed preflight failed: '+(result.stderr+result.stdout)[-2000:])
    changed=sorted(k for k in proposed if proposed[k]!=current[k])
    if args.apply:
        patch={'data':{'release-manifest.yaml':base64.b64encode(raw).decode()}}
        kubectl('patch','secret','noebs-release-manifest','--type=merge','--patch-file=/dev/stdin',payload=json.dumps(patch))
    print(json.dumps({'validated_image':image,'changed_artifacts':changed,'fingerprint':manifest['fingerprint'],'applied':args.apply}))

if __name__=='__main__':main()
