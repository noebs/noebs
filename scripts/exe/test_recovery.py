import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('delivery_recovery', Path(__file__).with_name('recover-delivery.py'))
recovery = importlib.util.module_from_spec(spec)
spec.loader.exec_module(recovery)


class RecoveryFenceTests(unittest.TestCase):
    marker = {'state': 'destination-staged', 'migration_id': 'rehearsal-1'}

    def test_matching_marker_and_stopped_runtime(self):
        recovery.require_fenced(self.marker, 'rehearsal-1', [
            {'kind': 'Deployment', 'metadata': {'name': 'wallet-worker'}, 'spec': {'replicas': 0}},
            {'kind': 'CronJob', 'metadata': {'name': 'cleanup'}, 'spec': {'suspend': True}},
            {'kind': 'Pod', 'metadata': {'name': 'postgres-0'}, 'status': {'phase': 'Running'}},
        ])

    def test_wrong_recovery_and_live_writers_are_rejected(self):
        with self.assertRaises(ValueError):
            recovery.require_fenced(self.marker, 'another-recovery', [])
        for item in [
            {'kind': 'Deployment', 'metadata': {'name': 'worker'}, 'spec': {'replicas': 1}},
            {'kind': 'CronJob', 'metadata': {'name': 'cleanup'}, 'spec': {}},
            {'kind': 'Pod', 'metadata': {'name': 'unmanaged-writer'}, 'status': {'phase': 'Running'}},
        ]:
            with self.subTest(kind=item['kind']), self.assertRaises(ValueError):
                recovery.require_fenced(self.marker, 'rehearsal-1', [item])

    def test_recovery_in_progress_does_not_automatically_repeat_mutations(self):
        with self.assertRaises(ValueError):
            recovery.require_fenced(self.marker | {'state': 'destination-recovery-running'}, 'rehearsal-1', [])


if __name__ == '__main__':
    unittest.main()
