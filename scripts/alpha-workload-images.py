#!/usr/bin/env python3
"""Check the current-host NoEBS workload contract independently of image names.

Input is Kubernetes List objects named workloads, pods and cronjobs. Required
roles mirror base/kustomization.yaml plus the current-host Mojaloop sidecar.
Only completed Job pods are historical; third-party workloads stay out of this
application image contract. No Kubernetes or filesystem mutation is performed.
"""
import json
import re
import sys

DEPLOYMENTS = {
    name: [name] for name in (
        'admin-reporting', 'admin-reporting-projector', 'api-gateway', 'card-vault',
        'ebs-adapter', 'ebs-adapter-events', 'identity-auth', 'identity-worker', 'notification-chat',
        'psp-webhook', 'wallet-api', 'wallet-ledger', 'wallet-worker',
    )
}
DEPLOYMENTS['wallet-worker'].append('mojaloop-sdk')
CRONJOBS = ('noebs-gateway-auth-cleanup', 'noebs-workload-auth-cleanup')
# Hook jobs may already have been deleted. If still active, their roles must
# retain the release image even if someone changes its repository completely.
OPTIONAL_JOB_ROLES = {
    **{f'noebs-{name}-migrate': (['migrate'], ['wait-for-postgres']) for name in (
        'workload-auth', 'gateway-auth', 'identity-auth', 'card-vault',
        'ebs-adapter', 'admin-reporting', 'notification-chat', 'wallet-ledger',
    )},
    'noebs-deployment-preflight': (['preflight'], []),
    'keycloak-reconciler': (['reconcile'], ['wait-for-keycloak']),
    'temporal-namespace-bootstrap': (['temporal-namespace-bootstrap'], []),
}
LABEL = 'app.kubernetes.io/name'


def historical_job(pod):
    return pod.get('status', {}).get('phase') in ('Succeeded', 'Failed') and any(
        owner.get('kind') == 'Job' for owner in pod.get('metadata', {}).get('ownerReferences', [])
    )


def verify(value, expected_app, expected_sdk):
    errors = []
    workloads = value['workloads']['items']
    cronjobs = value['cronjobs']['items']
    pods = [pod for pod in value['pods']['items'] if not historical_job(pod)]

    def check(condition, message):
        if not condition:
            errors.append(message)

    def expected(name):
        return expected_sdk if name == 'mojaloop-sdk' else expected_app

    def containers(spec, names, init_names, label, status=None, deployment=False):
        for key, required, status_key in (
            ('containers', names, 'containerStatuses'),
            ('initContainers', init_names, 'initContainerStatuses'),
        ):
            actual = spec.get(key, [])
            check(sorted(c.get('name', '') for c in actual) == sorted(required),
                  f'{label}: unexpected or missing {key} roles')
            for item in actual:
                name = item.get('name', '')
                check(item.get('image') == expected(name), f'{label}:{name}: wrong declared image')
                if status is None:
                    continue
                matches = [s for s in status.get(status_key, []) if s.get('name') == name]
                check(len(matches) == 1, f'{label}:{name}: missing or duplicate runtime status')
                if len(matches) != 1:
                    continue
                running = matches[0]
                check(running.get('imageID') == expected(name), f'{label}:{name}: wrong running imageID')
                if key == 'containers' and deployment:
                    check(running.get('ready') is True, f'{label}:{name}: runtime is not ready')
                elif key == 'initContainers':
                    check(running.get('state', {}).get('terminated', {}).get('exitCode') == 0,
                          f'{label}:{name}: init container has not completed successfully')

    runtime_count = 0
    for role, names in DEPLOYMENTS.items():
        found = [w for w in workloads if w.get('kind') == 'Deployment' and w.get('metadata', {}).get('name') == role]
        check(len(found) == 1, f'Deployment/{role}: required workload missing or duplicated')
        if len(found) != 1:
            continue
        workload = found[0]
        template = workload['spec']['template']
        check(template.get('metadata', {}).get('labels', {}).get(LABEL) == role,
              f'Deployment/{role}: role label differs from the deployment contract')
        check(workload['spec'].get('selector', {}).get('matchLabels', {}).get(LABEL) == role,
              f'Deployment/{role}: selector differs from the deployment contract')
        containers(template['spec'], names, ['wait-for-postgres'], f'Deployment/{role}')
        replicas = workload['spec'].get('replicas', 1)
        check(isinstance(replicas, int) and replicas > 0, f'Deployment/{role}: required workload is scaled to zero')
        members = [p for p in pods if p.get('metadata', {}).get('labels', {}).get(LABEL) == role]
        check(len(members) >= max(replicas, 1), f'Deployment/{role}: required runtime pods are missing')
        for pod in members:
            label = 'Pod/' + pod['metadata']['name']
            check(pod.get('status', {}).get('phase') == 'Running', f'{label}: required runtime pod is not Running')
            containers(pod['spec'], names, ['wait-for-postgres'], label, pod.get('status', {}), deployment=True)
            runtime_count += len(names)

    for role in CRONJOBS:
        found = [job for job in cronjobs if job.get('metadata', {}).get('name') == role]
        check(len(found) == 1, f'CronJob/{role}: required cleanup workload missing or duplicated')
        if len(found) != 1:
            continue
        job = found[0]
        check(job.get('spec', {}).get('suspend', False) is False, f'CronJob/{role}: required cleanup is suspended')
        template = job['spec']['jobTemplate']['spec']['template']
        check(template.get('metadata', {}).get('labels', {}).get(LABEL) == role,
              f'CronJob/{role}: role label differs from the cleanup contract')
        containers(template['spec'], ['cleanup'], ['wait-for-postgres'], f'CronJob/{role}')

    job_roles = {**OPTIONAL_JOB_ROLES, **{name: (['cleanup'], ['wait-for-postgres']) for name in CRONJOBS}}
    for pod in pods:
        role = pod.get('metadata', {}).get('labels', {}).get(LABEL)
        if role not in job_roles:
            continue
        names, init_names = job_roles[role]
        label = 'Pod/' + pod['metadata']['name']
        containers(pod['spec'], names, init_names, label, pod.get('status', {}))
        runtime_count += len(names)
    return {'passed': not errors, 'requiredDeployments': len(DEPLOYMENTS),
            'requiredCronJobs': len(CRONJOBS), 'activeApplicationContainers': runtime_count,
            'failures': errors}


def main():
    if len(sys.argv) != 3 or not all(re.fullmatch(r'ghcr\.io/noebs/noebs@sha256:[0-9a-f]{64}', x) for x in sys.argv[1:]):
        raise SystemExit('usage: alpha-workload-images.py <application image@sha256> <SDK image@sha256>')
    try:
        report = verify(json.load(sys.stdin), *sys.argv[1:])
    except (ValueError, KeyError, TypeError) as error:
        report = {'passed': False, 'failures': ['Invalid Kubernetes snapshot: ' + type(error).__name__]}
    print(json.dumps(report, sort_keys=True))
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    sys.exit(main())
