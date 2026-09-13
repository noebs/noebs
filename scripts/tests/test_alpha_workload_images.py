"""Execute the image checker with representative and deliberately broken snapshots."""
import copy
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import unittest

ROOT = Path(__file__).resolve().parents[2]
CHECKER = ROOT / 'scripts/alpha-workload-images.py'
spec = importlib.util.spec_from_file_location('alpha_images', CHECKER)
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)
APP = 'ghcr.io/noebs/noebs@sha256:' + 'a' * 64
SDK = 'ghcr.io/noebs/noebs@sha256:' + 'b' * 64
FOREIGN = 'ghcr.io/mojaloop/sdk-scheme-adapter:latest'


def fixture():
    workloads, pods, cronjobs = [], [], []
    for role, names in checker.DEPLOYMENTS.items():
        labels = {checker.LABEL: role}
        containers = [{'name': name, 'image': SDK if name == 'mojaloop-sdk' else APP} for name in names]
        init = [{'name': 'wait-for-postgres', 'image': APP}]
        pod_spec = {'containers': containers, 'initContainers': init}
        workloads.append({'kind': 'Deployment', 'metadata': {'name': role},
            'spec': {'replicas': 1, 'selector': {'matchLabels': labels},
                     'template': {'metadata': {'labels': labels}, 'spec': copy.deepcopy(pod_spec)}}})
        pods.append({'metadata': {'name': role + '-pod', 'labels': labels}, 'spec': copy.deepcopy(pod_spec),
            'status': {'phase': 'Running', 'containerStatuses': [
                {'name': c['name'], 'image': 'sha256:config-id', 'imageID': c['image'], 'ready': True} for c in containers],
                'initContainerStatuses': [{'name': 'wait-for-postgres', 'imageID': APP,
                    'state': {'terminated': {'exitCode': 0}}}]}})
    for role in checker.CRONJOBS:
        cronjobs.append({'metadata': {'name': role}, 'spec': {'jobTemplate': {'spec': {'template': {
            'metadata': {'labels': {checker.LABEL: role}}, 'spec': {
                'containers': [{'name': 'cleanup', 'image': APP}],
                'initContainers': [{'name': 'wait-for-postgres', 'image': APP}]}}}}}})
    return {'workloads': {'items': workloads}, 'pods': {'items': pods}, 'cronjobs': {'items': cronjobs}}


class WorkloadImagesTest(unittest.TestCase):
    def execute(self, value, passed):
        result = subprocess.run([sys.executable, str(CHECKER), APP], input=json.dumps(value),
                                text=True, capture_output=True, check=False)
        report = json.loads(result.stdout)
        self.assertEqual(result.returncode, 0 if passed else 1, report)
        self.assertEqual(report['passed'], passed)
        return report

    def test_config_ids_are_not_manifest_ids_and_third_party_workloads_are_untouched(self):
        value = fixture()
        value['workloads']['items'].append({'kind': 'StatefulSet', 'metadata': {'name': 'postgres'}})
        value['pods']['items'].append({'metadata': {'name': 'postgres-0'},
                                      'spec': {'containers': [{'name': 'postgres', 'image': 'postgres@sha256:third-party'}]}})
        self.execute(value, True)

    def test_required_workload_and_runtime_presence(self):
        for section in ('workloads', 'pods', 'cronjobs'):
            with self.subTest(section=section):
                value = fixture(); value[section]['items'].pop()
                self.execute(value, False)

    def test_sdk_foreign_repository_missing_role_and_wrong_manifest_are_rejected(self):
        for mutation in ('foreign-application', 'foreign-template', 'foreign-pod', 'missing-sidecar', 'missing-status', 'wrong-manifest', 'extra-sidecar'):
            with self.subTest(mutation=mutation):
                value = fixture(); worker = value['pods']['items'][-1]
                if mutation == 'foreign-application': value['pods']['items'][0]['spec']['containers'][0]['image'] = FOREIGN
                elif mutation == 'foreign-template': value['workloads']['items'][-1]['spec']['template']['spec']['containers'][-1]['image'] = FOREIGN
                elif mutation == 'foreign-pod':
                    worker['spec']['containers'][-1]['image'] = FOREIGN
                    worker['status']['containerStatuses'][-1]['imageID'] = FOREIGN
                elif mutation == 'missing-sidecar': worker['spec']['containers'].pop()
                elif mutation == 'missing-status': worker['status']['containerStatuses'].pop()
                elif mutation == 'wrong-manifest': worker['status']['containerStatuses'][-1]['imageID'] = SDK
                if mutation == 'extra-sidecar': worker['spec']['containers'].append({'name': 'sdk', 'image': SDK})
                self.execute(value, False)

    def test_cronjob_every_container_is_checked_independent_of_repository(self):
        for key in ('containers', 'initContainers'):
            with self.subTest(key=key):
                value = fixture()
                value['cronjobs']['items'][0]['spec']['jobTemplate']['spec']['template']['spec'][key][0]['image'] = FOREIGN
                self.execute(value, False)

    def test_application_init_container_running_digest_is_checked(self):
        value = fixture()
        value['pods']['items'][0]['status']['initContainerStatuses'][0]['imageID'] = SDK
        self.execute(value, False)

    def test_missing_duplicate_or_unready_runtime_status_is_rejected(self):
        for mutation in ('missing', 'duplicate', 'unready', 'scaled-zero'):
            with self.subTest(mutation=mutation):
                value = fixture(); statuses = value['pods']['items'][0]['status']['containerStatuses']
                if mutation == 'missing': statuses.clear()
                elif mutation == 'duplicate': statuses.append(copy.deepcopy(statuses[0]))
                elif mutation == 'unready': statuses[0]['ready'] = False
                else: value['workloads']['items'][0]['spec']['replicas'] = 0
                self.execute(value, False)

    def test_only_completed_job_pods_are_historical(self):
        for phase in ('Succeeded', 'Failed'):
            with self.subTest(phase=phase):
                value = fixture()
                value['pods']['items'].append({'metadata': {'name': 'old-cleanup',
                    'labels': {checker.LABEL: checker.CRONJOBS[0]}, 'ownerReferences': [{'kind': 'Job'}]},
                    'spec': {'containers': [{'name': 'cleanup', 'image': 'old-image'}]}, 'status': {'phase': phase}})
                self.execute(value, True)
                value['pods']['items'][-1]['metadata']['ownerReferences'] = [{'kind': 'ReplicaSet'}]
                self.execute(value, False)

    def test_active_cleanup_with_foreign_image_cannot_escape(self):
        value = fixture()
        value['pods']['items'].append({'metadata': {'name': 'active-cleanup',
            'labels': {checker.LABEL: checker.CRONJOBS[0]}, 'ownerReferences': [{'kind': 'Job'}]},
            'spec': {'containers': [{'name': 'cleanup', 'image': FOREIGN}]}, 'status': {'phase': 'Running'}})
        self.execute(value, False)



if __name__ == '__main__':
    unittest.main()
