import copy
import importlib.util
import json
from pathlib import Path
import shlex
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch


spec = importlib.util.spec_from_file_location('bootstrap_cluster', Path(__file__).with_name('bootstrap-cluster.py'))
bootstrap = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bootstrap)

INGRESS = '''apiVersion: helm.cattle.io/v1
kind: HelmChartConfig
metadata: {name: traefik, namespace: kube-system}
spec:
  valuesContent: |
    hostNetwork: true
    ports:
      web: {port: 8081}
'''


def inventory():
    return {name: {'role': role, 'ssh_destination': name + '.exe.xyz'} for name, role in [
        ('noebs-state', 'control'), ('noebs-server', 'data'),
        ('noebs-w1', 'workers'), ('noebs-w2', 'workers'),
        ('noebs-snapshots', 'backup'), ('noebs-bot', 'telegram'),
    ]}


class BootstrapTest(unittest.TestCase):
    def setUp(self):
        self.events = []
        self.inputs = []
        self.commands = []
        self.lease = Mock()

    def ssh(self, key, destination, command, **kwargs):
        self.commands.append((destination, command))
        if command.startswith('sudo bash /opt/noebs/node-init.sh '):
            parts = shlex.split(command)
            self.events.append((parts[3], parts[6]))
            self.inputs.append((parts[3], parts[6], json.loads(kwargs['input'])))
        output = b''
        if command == 'tailscale ip -4':
            output = b'100.75.61.23\n'
        elif command == 'sudo cat /var/lib/rancher/k3s/server/node-token':
            output = b'test-join-token\n'
        return SimpleNamespace(stdout=output)

    def test_roles_select_server_and_every_worker(self):
        with patch.object(bootstrap, 'ssh', side_effect=self.ssh):
            bootstrap.bootstrap(Path('test-key'), inventory(), self.lease, INGRESS)
        network_nodes = {'noebs-server', 'noebs-w1', 'noebs-w2', 'noebs-snapshots'}
        self.assertEqual(set(self.events[:4]), {(name, 'preflight') for name in network_nodes})
        self.assertEqual(set(self.events[4:8]), {(name, 'network') for name in network_nodes})
        self.assertEqual(self.events[8], ('noebs-server', 'cluster'))
        self.assertEqual(set(self.events[9:]), {('noebs-w1', 'cluster'), ('noebs-w2', 'cluster')})
        self.assertTrue(all(name not in {'noebs-state', 'noebs-bot'} for name, _ in self.events))
        server_input = next(data for name, phase, data in self.inputs if name == 'noebs-server' and phase == 'cluster')
        self.assertEqual(server_input['ingress_config'], INGRESS)
        for name, phase, data in self.inputs:
            if name.startswith('noebs-w') and phase == 'cluster':
                self.assertEqual(data['k3s_token'], 'test-join-token')
        wait = self.commands[-1]
        self.assertEqual(wait[0], 'noebs-server.exe.xyz')
        self.assertIn('node/noebs-server node/noebs-w1 node/noebs-w2', wait[1])

    def test_failed_prerequisite_stops_before_network_setup(self):
        def fail_worker(key, destination, command, **kwargs):
            result = self.ssh(key, destination, command, **kwargs)
            if command.endswith(' preflight') and destination == 'noebs-w2.exe.xyz':
                raise RuntimeError('The VM must expose /dev/net/tun')
            return result
        with patch.object(bootstrap, 'ssh', side_effect=fail_worker):
            with self.assertRaisesRegex(RuntimeError, '/dev/net/tun'):
                bootstrap.bootstrap(Path('test-key'), inventory(), self.lease, INGRESS)
        self.assertTrue(all(phase == 'preflight' for _, phase in self.events))

    def test_invalid_inventory_fails_before_ssh(self):
        cases = [None, {}, {'noebs-x': {'role': []}}]
        for key, value in [('role', ''), ('role', 'control'), ('ssh_destination', ''), ('ssh_destination', '-oProxyCommand=bad')]:
            machines = inventory()
            machines['noebs-server'][key] = value
            cases.append(machines)
        duplicate = inventory()
        duplicate['noebs-w2']['ssh_destination'] = duplicate['noebs-w1']['ssh_destination']
        cases.append(duplicate)
        no_workers = {name: vm for name, vm in inventory().items() if vm['role'] != 'workers'}
        cases.append(no_workers)
        for machines in cases:
            with self.subTest(machines=machines), patch.object(bootstrap, 'ssh') as remote:
                with self.assertRaises(ValueError):
                    bootstrap.bootstrap(Path('test-key'), machines, self.lease, INGRESS)
                remote.assert_not_called()

    def test_missing_or_wrong_ingress_fails_before_ssh(self):
        for config in ['', '[]', '[invalid', INGRESS.replace('name: traefik', 'name: unrelated'),
                       INGRESS.replace('namespace: kube-system', 'namespace: noebs')]:
            with self.subTest(config=config), patch.object(bootstrap, 'ssh') as remote:
                with self.assertRaises(ValueError):
                    bootstrap.bootstrap(Path('test-key'), inventory(), self.lease, config)
                remote.assert_not_called()

    def test_role_selection_does_not_mutate_inputs(self):
        machines = inventory()
        before = copy.deepcopy(machines)
        bootstrap.fleet_roles(machines)
        self.assertEqual(machines, before)

    def test_lost_controller_lease_stops_before_ssh(self):
        self.lease.check.side_effect = RuntimeError('lease lost')
        with patch.object(bootstrap, 'ssh') as remote:
            with self.assertRaisesRegex(RuntimeError, 'lease lost'):
                bootstrap.bootstrap(Path('test-key'), inventory(), self.lease, INGRESS)
            remote.assert_not_called()


class NodeInitializationOrderTest(unittest.TestCase):
    def simulate_server(self, existing=False, missing_authority=False):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name in ['usr/local/bin', 'etc/rancher/k3s', 'etc/systemd/system',
                         'var/lib/rancher/k3s/server/db', 'credentials']:
                (root / name).mkdir(parents=True)
            (root / 'usr/local/bin/k3s').touch(mode=0o755)
            (root / 'credentials/ingress_config').write_text(INGRESS)
            if existing:
                (root / 'var/lib/rancher/k3s/server/db/state.db').write_text('existing authority')
                (root / 'namespace-ready').touch()
            if missing_authority:
                (root / 'var/lib/rancher/k3s/server/token').write_text('existing cluster token')
            script = Path(__file__).with_name('node-init.sh').read_text()
            script = script[script.index('fresh_server=false'):]
            for prefix in ['/usr/local/', '/etc/', '/var/']:
                script = script.replace(prefix, str(root) + prefix)
            harness = '''set -euo pipefail
role=data
name=noebs-data
node_ip=100.75.61.23
credentials="$TEST_ROOT/credentials"
fail() { printf '%s\\n' "$*" >&2; exit 1; }
service_hash() { printf stable; }
systemctl() {
  case "$1" in
    is-active) return 1 ;;
    start|restart)
      if grep -q '^  - traefik$' "$TEST_ROOT/etc/rancher/k3s/config.yaml"; then
        printf 'api-without-ingress\\n'
      else
        test -f "$TEST_ROOT/namespace-ready" || fail 'Ingress enabled before namespace exists'
        printf 'api-with-ingress\\n'
      fi ;;
  esac
}
k3s() {
  if [[ "$1" == --version ]]; then
    printf 'k3s version v1.35.4+k3s1 test\\n'
  elif [[ "$1 $2" == 'kubectl apply' ]]; then
    touch "$TEST_ROOT/namespace-ready"
    printf 'namespace-created\\n' >> "$TEST_ROOT/events"
  elif [[ "$1 $2" == 'kubectl wait' ]]; then
    test -f "$TEST_ROOT/namespace-ready"
  elif [[ "$1 $2" != 'kubectl get' ]]; then
    fail 'Unexpected Kubernetes command'
  fi
}
'''
            result = subprocess.run(['bash', '-c', 'TEST_ROOT=' + shlex.quote(str(root)) + '\n' + harness + script,
                                    'node-init-test'], capture_output=True, text=True)
            events = (root / 'events').read_text() if (root / 'events').exists() else ''
            final_config = (root / 'etc/rancher/k3s/config.yaml').read_text() if (root / 'etc/rancher/k3s/config.yaml').exists() else ''
            return result, events, final_config

    def test_fresh_api_creates_namespace_before_enabling_ingress(self):
        result, events, config = self.simulate_server()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.splitlines(), ['api-without-ingress', 'api-with-ingress'])
        self.assertEqual(events, 'namespace-created\n')
        self.assertNotIn('  - traefik', config)
        self.assertIn('  - servicelb', config)

    def test_existing_server_keeps_ingress_enabled(self):
        result, events, _ = self.simulate_server(existing=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.splitlines(), ['api-with-ingress'])
        self.assertEqual(events, '')

    def test_missing_existing_authority_is_not_initialized_as_fresh(self):
        result, events, _ = self.simulate_server(missing_authority=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Existing Kubernetes authority', result.stderr)
        self.assertEqual(result.stdout, '')
        self.assertEqual(events, '')


if __name__ == '__main__':
    unittest.main()
