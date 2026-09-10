#!/usr/bin/env python3
"""Validate a configuration-only promotion against the existing secret set.

Run on the deployment host as root with a reviewed kustomize render in JSON.
Use --current-render from the exact deployed revision when generated ConfigMap
names change. Keep promotions serialized until GitOps applies the new render.
Private payloads remain in a temporary mode0700 directory on that host. Existing
secret hashes must match the current release before any new fingerprint is made.
The exact new image validates the complete proposed payload with network disabled.
"""
import argparse
import base64
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import subprocess
import tempfile
import yaml

def kubectl(*args, payload=None):
    return subprocess.run(['k3s','kubectl','-n','noebs',*args], input=payload, text=True, capture_output=True, check=True).stdout

def digest(payload): return 'sha256:' + hashlib.sha256(payload).hexdigest()


def manifest_for(payloads):
    artifacts = {name: digest(payload) for name, payload in payloads.items()}
    hashed = hashlib.sha256()
    for name in sorted(artifacts):
        hashed.update((name + '\0' + artifacts[name].removeprefix('sha256:') + '\n').encode())
    return {'api_version': 'noebs.sd/kubernetes-release/v1',
            'fingerprint': 'sha256:' + hashed.hexdigest(), 'artifacts': artifacts}


def read_render(path):
    objects = json.loads(Path(path).read_text())
    if isinstance(objects, dict) and objects.get('kind') == 'List':
        objects = objects['items']
    if not isinstance(objects, list):
        raise ValueError('Expected a JSON array or Kubernetes List from kustomize')
    jobs = [obj for obj in objects if obj.get('kind') == 'Job' and
            obj.get('metadata', {}).get('name') == 'noebs-deployment-preflight']
    if len(jobs) != 1 or jobs[0]['metadata'].get('namespace') != 'noebs':
        raise ValueError('Render must contain exactly one noebs deployment preflight job')
    spec = jobs[0]['spec']['template']['spec']
    if len(spec['containers']) != 1:
        raise ValueError('Preflight must contain exactly one validation container')
    container = spec['containers'][0]
    image = container['image']
    if not re.fullmatch(r'ghcr\.io/noebs/noebs@sha256:[0-9a-f]{64}', image):
        raise ValueError('Preflight image must be pinned to an immutable noebs digest')
    volumes = {volume['name']: volume for volume in spec['volumes']}
    if len(volumes) != len(spec['volumes']):
        raise ValueError('Preflight volume names must be unique')
    mounts = {}
    for mount in container['volumeMounts']:
        path = mount['mountPath']
        if not path.startswith('/preflight/'):
            raise ValueError('Preflight mount is outside the artifact root')
        name = path.removeprefix('/preflight/')
        if not name or PurePosixPath(name).is_absolute() or str(PurePosixPath(name)) != name or '..' in PurePosixPath(name).parts:
            raise ValueError('Preflight artifact path must be canonical and relative')
        if name in mounts or not mount.get('readOnly') or not mount.get('subPath'):
            raise ValueError('Preflight artifacts require unique read-only subPath mounts')
        volume = volumes[mount['name']]
        if ('secret' in volume) == ('configMap' in volume):
            raise ValueError('Preflight artifact must reference a Secret or ConfigMap')
        kind = 'secret' if 'secret' in volume else 'configmap'
        source = volume['secret']['secretName'] if kind == 'secret' else volume['configMap']['name']
        mounts[name] = (kind, source, mount['subPath'])
    if mounts.pop('release-manifest.yaml', None) != ('secret', 'noebs-release-manifest', 'release-manifest.yaml'):
        raise ValueError('Preflight release manifest source changed')
    configs = {}
    for obj in objects:
        if obj.get('kind') != 'ConfigMap':
            continue
        metadata = obj['metadata']
        if metadata.get('namespace') != 'noebs':
            continue
        name = metadata['name']
        if name in configs:
            raise ValueError('Render contains duplicate ConfigMap names')
        configs[name] = obj
    return image, mounts, configs


def source_payload(obj, kind, key):
    data = obj['data'][key]
    return base64.b64decode(data, validate=True) if kind == 'secret' else data.encode()


def source_version(obj):
    metadata = obj['metadata']
    version = (metadata['uid'], metadata['resourceVersion'])
    if not all(isinstance(value, str) and value for value in version):
        raise ValueError('Live artifact source has no immutable identity or version')
    return version


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('render')
    parser.add_argument('--current-render', help='JSON render from the exact currently deployed Git revision; required when generated source names change')
    parser.add_argument('--apply',action='store_true')
    args=parser.parse_args()
    image, mounts, configs = read_render(args.render)
    _, old_mounts, _ = read_render(args.current_render or args.render)
    if mounts.keys() != old_mounts.keys():
        raise ValueError('Proposed preflight artifact contract differs from the current release')
    old_secret=json.loads(kubectl('get','secret','noebs-release-manifest','-o','json'))
    source_version(old_secret)
    original=yaml.safe_load(base64.b64decode(old_secret['data']['release-manifest.yaml']))
    current={}; proposed={}; cache={('secret', 'noebs-release-manifest'): old_secret}
    for name, reference in mounts.items():
        kind, key, field = reference
        old_kind, old_key, old_field = old_mounts[name]
        if kind != old_kind or (kind == 'secret' and reference != old_mounts[name]):
            raise ValueError('Configuration-only promotion cannot change artifact source kinds or Secret references')
        if (old_kind, old_key) not in cache:
            cache[old_kind, old_key] = json.loads(kubectl('get', old_kind, old_key, '-o', 'json'))
            source_version(cache[old_kind, old_key])
        current[name] = source_payload(cache[old_kind, old_key], old_kind, old_field)
        if kind == 'configmap' and key in configs:
            proposed[name] = source_payload(configs[key], kind, field)
        elif reference == old_mounts[name]:
            proposed[name] = current[name]
        else:
            raise ValueError('A changed ConfigMap source must be present in the proposed render')
    if manifest_for(current) != original:
        raise ValueError('Existing release artifacts or fingerprint drifted; reconcile before promotion')
    manifest = manifest_for(proposed)
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
        # Detect changes during image pull/validation before publishing the new
        # manifest. Kubernetes cannot atomically compare multiple objects, so the
        # deployment controller/operator must still serialize promotions.
        for (kind, name), before in cache.items():
            after = json.loads(kubectl('get', kind, name, '-o', 'json'))
            if source_version(after) != source_version(before):
                raise RuntimeError('A current release source changed during validation; rerun from the current release')
        patch = [
            {'op': 'test', 'path': '/metadata/resourceVersion', 'value': old_secret['metadata']['resourceVersion']},
            {'op': 'test', 'path': '/data/release-manifest.yaml', 'value': old_secret['data']['release-manifest.yaml']},
            {'op': 'replace', 'path': '/data/release-manifest.yaml', 'value': base64.b64encode(raw).decode()},
        ]
        kubectl('patch','secret','noebs-release-manifest','--type=json','--patch-file=/dev/stdin',payload=json.dumps(patch))
    print(json.dumps({'validated_image':image,'changed_artifacts':changed,'previous_fingerprint':original['fingerprint'], 'fingerprint':manifest['fingerprint'],'applied':args.apply}))

if __name__=='__main__':main()
