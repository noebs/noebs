# Run only inside an unshared user AND network namespace; never on a host network.
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time

sys.path.insert(0, str(Path(__file__).parent))
from external_transport_source import callback_tuple, firewall_script


def run(*cmd, **kw):
    return subprocess.run(cmd, check=True, capture_output=True, **kw).stdout.decode().strip()


assert run('id', '-u') == '0'
assert str(Path('/proc/self/ns/net').readlink()) != os.environ['NOEBS_TEST_ORIGINAL_NETNS']
children = []
try:
    for _ in range(2):
        children.append(subprocess.Popen(['unshare', '-n', 'sleep', '120']))
    time.sleep(.2)
    peer, backend = [str(child.pid) for child in children]
    def inside(pid, *args):
        return run('nsenter', '-t', pid, '-n', *args)
    for outer, inner, pid, outerip, innerip in [
        ('tailscale0', 'peer0', peer, '100.85.107.107/10', '100.76.217.90/10'),
        ('pod0', 'backend0', backend, '10.123.0.1/24', '10.123.0.2/24')]:
        run('ip', 'link', 'add', outer, 'type', 'veth', 'peer', 'name', inner)
        run('ip', 'link', 'set', inner, 'netns', pid)
        run('ip', 'addr', 'add', outerip, 'dev', outer)
        run('ip', 'link', 'set', outer, 'up')
        inside(pid, 'ip', 'addr', 'add', innerip, 'dev', inner)
        inside(pid, 'ip', 'link', 'set', inner, 'up')
        inside(pid, 'ip', 'link', 'set', 'lo', 'up')
        inside(pid, 'ip', 'route', 'add', 'default', 'via', outerip.split('/')[0])
    run('ip', 'addr', 'add', '100.85.107.108/32', 'dev', 'tailscale0')
    inside(peer, 'ip', 'addr', 'add', '100.76.217.91/32', 'dev', 'peer0')
    run('sysctl', '-qw', 'net.ipv4.ip_forward=1')
    def ipt(table, *args): return run('iptables', '-w', '10', '-t', table, *args)
    ipt('filter', '-N', 'ts-forward')
    ipt('filter', '-A', 'FORWARD', '-j', 'ts-forward')
    ipt('filter', '-A', 'ts-forward', '-i', 'tailscale0', '-j', 'MARK', '--set-xmark', '0x40000/0xff0000')
    ipt('nat', '-N', 'ts-postrouting')
    ipt('nat', '-A', 'POSTROUTING', '-j', 'ts-postrouting')
    ipt('nat', '-A', 'ts-postrouting', '-m', 'mark', '--mark', '0x40000/0xff0000', '-j', 'MASQUERADE')
    for port, translated in [('30402', '4002'), ('30403', '4002'), ('30404', '4003')]:
        ipt('nat', '-A', 'PREROUTING', '-p', 'tcp', '--dport', port, '-j', 'DNAT', '--to-destination', '10.123.0.2:' + translated)
    server = '''import http.server, threading, time
class H(http.server.BaseHTTPRequestHandler):
 def do_GET(self):
  self.send_response(404); self.end_headers(); self.wfile.write(self.client_address[0].encode())
 def log_message(self,*a): pass
for p in [4002,4003]:
 s=http.server.HTTPServer(('0.0.0.0',p),H); threading.Thread(target=s.serve_forever,daemon=True).start()
time.sleep(120)
'''
    children.append(subprocess.Popen(['nsenter', '-t', backend, '-n', 'python3', '-c', server]))
    time.sleep(.2)
    client = '''import http.client,sys
c=http.client.HTTPConnection(sys.argv[1],int(sys.argv[2]),timeout=3,source_address=(sys.argv[3],0))
c.request('GET','/proof'); r=c.getresponse(); print(r.read().decode()); c.close()
'''
    def probe(src='100.76.217.90', dst='100.85.107.107', port='30402'):
        return inside(peer, 'python3', '-c', client, dst, port, src)
    results = {}
    results['baseline_snat'] = probe()
    assert results['baseline_snat'] == '10.123.0.1'
    settings = {'interop_tenant': 'noebs', 'interop_backend_allowed_peers': ['100.76.217.90'],
                'interop_backend_listen_address': '0.0.0.0:4002'}
    script = firewall_script(callback_tuple(settings, '100.85.107.107'))
    # The harness supplies only the local synthetic node identity; real iptables
    # enforces all selectors and mark operations in the isolated kernel.
    script = script.replace('$(tailscale ip -4)', '100.85.107.107')
    run('sh', '-s', 'apply', input=script.encode())
    results['exact_peer'] = probe()
    assert results['exact_peer'] == '100.76.217.90'
    results['wrong_peer'] = probe(src='100.76.217.91')
    results['wrong_original_destination'] = probe(dst='100.85.107.108')
    results['wrong_original_port'] = probe(port='30403')
    results['wrong_translated_port'] = probe(port='30404')
    assert all(results[name] == '10.123.0.1' for name in results if name.startswith('wrong_'))
    # Reapply is idempotent and does not accumulate jumps/rules.
    run('sh', '-s', 'apply', input=script.encode())
    assert ipt('mangle', '-S', 'POSTROUTING').count('-j NOEBS_CALLBACK_SOURCE') == 1
    # An unrelated low-order Kubernetes mark survives the masking operation.
    ipt('filter', '-A', 'FORWARD', '-i', 'tailscale0', '-j', 'MARK', '--or-mark', '0x4000')
    ipt('mangle', '-A', 'POSTROUTING', '-p', 'tcp', '--dport', '4002', '-m', 'mark', '--mark', '0x4000/0xffffffff', '-j', 'RETURN')
    assert probe() == '100.76.217.90'
    counters = ipt('mangle', '-L', 'POSTROUTING', '-n', '-v', '-x')
    matched = next(line.split()[0] for line in counters.splitlines() if 'RETURN' in line)
    assert int(matched) > 0, counters
    results['unrelated_kubernetes_mark_preserved'] = True
    run('sh', '-s', 'remove', input=script.encode())
    results['disabled_restores_existing_snat'] = probe()
    assert results['disabled_restores_existing_snat'] == '10.123.0.1'
    run('sh', '-s', 'remove', input=script.encode())
    assert 'NOEBS_CALLBACK_SOURCE' not in ipt('mangle', '-S')
    print(json.dumps(results, indent=2))
finally:
    for child in children:
        child.terminate()
    for child in children:
        child.wait()
