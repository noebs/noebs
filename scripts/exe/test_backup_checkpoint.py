import hashlib
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest

from backup_checkpoint import ARCHIVES, FORMAT, manifest_entries, verify_set
from importlib.util import module_from_spec, spec_from_file_location
spec = spec_from_file_location('recover_delivery', Path(__file__).with_name('recover-delivery.py'))
recovery = module_from_spec(spec)
spec.loader.exec_module(recovery)


class CheckpointTests(unittest.TestCase):
    stamp = '20260912T180000Z'

    def test_old_online_backup_set_is_rejected(self):
        with self.assertRaisesRegex(ValueError, 'predates coordinated'):
            manifest_entries((('a'*64) + ' ./' + self.stamp + '-postgres.sql.gz.age\n').encode(), self.stamp)

    def test_hashes_cannot_hide_mismatched_checkpoint(self):
        checkpoint = {'format': FORMAT, 'id': '20260911T180000Z', 'phase': 'resumed', 'cluster_uid': 'cluster',
                      'fenced_at': '2026-09-12T18:00:00+00:00', 'snapshot_completed_at': '2026-09-12T18:00:10+00:00',
                      'cold_volumes': {'kafka': '/data/kafka', 'noebs-mojaloop-redis': '/data/redis'}}
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            lines = []
            for suffix in ARCHIVES:
                name = self.stamp + '-' + suffix
                value = json.dumps(checkpoint).encode() if suffix == 'checkpoint.json' else b'encrypted-archive'
                (root/name).write_bytes(value)
                lines.append(hashlib.sha256(value).hexdigest() + ' ./' + name)
            (root/(self.stamp+'-SHA256SUMS')).write_text('\n'.join(lines))
            with self.assertRaisesRegex(ValueError, 'different archive set'):
                verify_set(root)

    def test_cold_restore_rejects_path_escape_and_links_before_mutation(self):
        for name, kind in [('../outside', tarfile.REGTYPE), ('link', tarfile.SYMTYPE)]:
            output = io.BytesIO()
            with tarfile.open(fileobj=output, mode='w:gz') as bundle:
                entry = tarfile.TarInfo(name)
                entry.type = kind
                bundle.addfile(entry, io.BytesIO())
            with self.assertRaisesRegex(ValueError, 'unsafe filesystem'):
                recovery.validate_cold_archive(output.getvalue())


if __name__ == '__main__':
    unittest.main()
