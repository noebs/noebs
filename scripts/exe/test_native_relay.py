import json
import subprocess
import unittest

from native_relay import UNITS, switch_native_relay, verify_native_relay, verify_native_handoff


class FakeHost:
    def __init__(self, name, events, active, state='source-fenced', replicas=0):
        self.name, self.events, self.active = name, events, active
        self.state, self.replicas = state, replicas
        self.enabled = active
        self.hash = b'fixed-credential-hash'
        self.fail_stop = False
        self.stale_interface = False

    def run(self, command, payload=None):
        self.events.append((self.name, command))
        if command.startswith('! ip link') and self.stale_interface:
            raise subprocess.CalledProcessError(1, command)
        if 'get configmap' in command:
            return json.dumps({'data': {'state': self.state}}).encode()
        if 'get deployments' in command:
            return json.dumps({'items': [{'spec': {'replicas': self.replicas}}]}).encode()
        if '-p ActiveState' in command:
            return b'active\n' if self.active else b'inactive\n'
        if '-p UnitFileState' in command:
            return (('enabled' if self.enabled else 'disabled') + '\n').encode() * len(UNITS)
        if 'cat /etc/systemd/system/noebs-mojaloop-relay.service' in command:
            return b'ExecStart=relay --als 10.243.1.1:4000 --quotes 10.243.1.1:4000'
        if command.startswith('curl '):
            return b'404'
        if 'sha256sum' in command:
            return self.hash
        if 'disable --now' in command:
            if self.fail_stop:
                raise subprocess.CalledProcessError(1, command)
            self.active = self.enabled = False
        if 'systemctl enable' in command:
            self.active = self.enabled = True
        return b''


class NativeRelayTests(unittest.TestCase):
    def setUp(self):
        self.events = []
        self.source = FakeHost('source', self.events, True)
        self.worker = FakeHost('worker', self.events, False)

    def test_handoff_stops_and_disables_source_before_target_starts(self):
        switch_native_relay(self.source, self.worker)
        stop = next(i for i, (_, cmd) in enumerate(self.events) if 'disable --now' in cmd)
        start = next(i for i, (_, cmd) in enumerate(self.events) if 'systemctl enable' in cmd)
        self.assertLess(stop, start)
        self.assertFalse(self.source.active)
        self.assertFalse(self.source.enabled)
        self.assertTrue(self.worker.active)

    def test_completed_handoff_retry_verifies_without_restarting_the_peer(self):
        switch_native_relay(self.source, self.worker)
        self.events.clear()
        switch_native_relay(self.source, self.worker)
        self.assertFalse(any('disable' in cmd or 'systemctl enable' in cmd for _, cmd in self.events))

    def test_unfenced_or_running_source_rejects_before_mutation(self):
        for state, replicas in [('destination-active', 0), ('source-fenced', 1)]:
            self.source.state, self.source.replicas = state, replicas
            with self.assertRaises(ValueError):
                switch_native_relay(self.source, self.worker)
        self.assertFalse(any('disable' in cmd or 'systemctl enable' in cmd for _, cmd in self.events))

    def test_source_stop_failure_never_activates_worker(self):
        self.source.fail_stop = True
        with self.assertRaises(subprocess.CalledProcessError):
            switch_native_relay(self.source, self.worker)
        self.assertFalse(self.worker.active)
        self.assertFalse(any('systemctl enable' in cmd for _, cmd in self.events))

    def test_interface_left_after_service_stop_blocks_target_start(self):
        self.source.stale_interface = True
        with self.assertRaises(subprocess.CalledProcessError):
            switch_native_relay(self.source, self.worker)
        self.assertFalse(self.worker.active)

    def test_changed_peer_identity_rejects_staging(self):
        self.worker.hash = b'other-key'
        with self.assertRaisesRegex(ValueError, 'credentials differ'):
            verify_native_relay(self.source, self.worker)

    def test_stopped_but_enabled_source_rejects_completed_handoff(self):
        self.source.active = False
        self.worker.active = True
        with self.assertRaisesRegex(ValueError, 'disabled across restart'):
            verify_native_handoff(self.source, self.worker)


if __name__ == '__main__':
    unittest.main()
