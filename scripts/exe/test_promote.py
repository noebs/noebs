import hashlib
import json
from pathlib import Path
from subprocess import CompletedProcess
import tempfile
import unittest
from unittest.mock import patch

from promote import sdk_secret, verify_receipt


class SDKSecretTests(unittest.TestCase):
    def test_required_secret_is_preserved(self):
        self.assertEqual(sdk_secret({'ilp_secret': 'preserved-secret'})['stringData'],
                         {'ilp-secret': 'preserved-secret'})

    def test_missing_invalid_or_unknown_authority_is_rejected(self):
        for value in [None, {}, {'ilp_secret': ''}, {'ilp_secret': ' trimmed '},
                      {'ilp_secret': 42}, {'ilp_secret': 'valid', 'unexpected': 'value'}]:
            with self.subTest(value=value), self.assertRaises(ValueError):
                sdk_secret(value)


class ReceiptTests(unittest.TestCase):
    revision = 'a' * 40
    manifest = b'{"schemaVersion":2}'

    def receipt(self):
        digest = 'sha256:' + hashlib.sha256(self.manifest).hexdigest()
        return {'source_sha': self.revision, 'digest': digest,
                'digest_ref': 'ghcr.io/noebs/noebs@' + digest,
                'tag': 'ghcr.io/noebs/noebs:' + self.revision}

    def verify(self, receipt, role='app', manifest=None):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'receipt.json'
            path.write_text(json.dumps(receipt))
            result = CompletedProcess([], 0, self.manifest if manifest is None else manifest)
            with patch('promote.run', return_value=result):
                return verify_receipt(path, self.revision, role)

    def test_verified_manifest(self):
        receipt = self.receipt()
        self.assertEqual(self.verify(receipt), receipt['digest_ref'])

    def test_receipt_cannot_change_source_registry_or_workload(self):
        for field, value in [('source_sha', 'b' * 40),
                             ('digest_ref', 'example.org/other@' + self.receipt()['digest']),
                             ('tag', 'ghcr.io/noebs/noebs:mojaloop-sdk-' + self.revision)]:
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.verify(self.receipt() | {field: value})

    def test_manifest_bytes_must_match_receipt(self):
        with self.assertRaises(ValueError):
            self.verify(self.receipt(), manifest=b'changed')

    def test_sdk_profile_is_pinned(self):
        receipt = self.receipt() | {'tag': 'ghcr.io/noebs/noebs:mojaloop-sdk-' + self.revision,
                                   'profile': 'sdg-msisdn-v1',
                                   'upstream': '6594dc5689a95dffece23d481a407e4368d80a96'}
        self.assertEqual(self.verify(receipt, 'sdk'), receipt['digest_ref'])
        with self.assertRaises(ValueError):
            self.verify(receipt | {'upstream': 'changed'}, 'sdk')


if __name__ == '__main__':
    unittest.main()
