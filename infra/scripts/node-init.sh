#!/usr/bin/env bash
set -euo pipefail
name=${1:?set VM name}
role=${2:?set node role}
server_ip=${3-}
phase=${4:?set preflight, network or cluster phase}
fail() { printf '%s\n' "$*" >&2; exit 1; }
[[ "$name" =~ ^noebs-[a-z0-9-]+$ ]] || fail 'A valid noebs- VM name is required'
[[ "$role" == data || "$role" == workers || "$role" == backup ]] || fail 'Unsupported node role'
[[ "$phase" == preflight || "$phase" == network || "$phase" == cluster ]] || fail 'Unsupported initialization phase'
[[ "$phase" != cluster || "$role" != backup ]] || fail 'Backup hosts do not join Kubernetes'
[[ "$EUID" == 0 ]] || fail 'Node initialization requires root'
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || fail 'The fleet requires Linux amd64 hosts'
[[ $(ps -p 1 -o comm=) == systemd ]] || fail 'The fleet node image must boot systemd'
[[ -c /dev/net/tun ]] || fail 'The VM must expose /dev/net/tun for the private network'
for tool in python3 curl tar sha256sum systemctl; do
  command -v "$tool" >/dev/null || fail "Required host command is missing: $tool"
done
if [[ "$role" != backup ]]; then
  python3 - <<'PY'
import gzip
from pathlib import Path

controllers_path = Path('/sys/fs/cgroup/cgroup.controllers')
if not controllers_path.is_file():
    raise SystemExit('Kubernetes fleet nodes require cgroup v2')
missing = {'cpu', 'cpuset', 'memory', 'pids'} - set(controllers_path.read_text().split())
if missing:
    raise SystemExit('Missing cgroup controllers: ' + ', '.join(sorted(missing)))
kernel_path = Path('/proc/config.gz')
if not kernel_path.is_file():
    raise SystemExit('The exe.dev node kernel must expose /proc/config.gz for prerequisite validation')
config = dict(line.split('=', 1) for line in gzip.decompress(kernel_path.read_bytes()).decode().splitlines()
              if line.startswith('CONFIG_') and '=' in line)
required = ['NAMESPACES', 'NET_NS', 'PID_NS', 'IPC_NS', 'UTS_NS', 'CGROUPS',
            'MEMCG', 'CGROUP_PIDS', 'CPUSETS', 'OVERLAY_FS', 'VETH', 'BRIDGE',
            'BRIDGE_NETFILTER', 'NETFILTER', 'NF_CONNTRACK', 'NF_NAT', 'VXLAN', 'TUN']
missing = [name for name in required if config.get('CONFIG_' + name) not in {'y', 'm'}]
if missing:
    raise SystemExit('Kernel features required by Kubernetes are missing: ' + ', '.join(missing))
PY
fi
[[ "$phase" != preflight ]] || exit 0
umask 077
credentials=$(mktemp -d)
trap 'rm -rf "$credentials"' EXIT
cat > "$credentials/input.json"
python3 - "$credentials" <<'PY'
import json,sys
from pathlib import Path
p=Path(sys.argv[1]); data=json.loads((p/'input.json').read_text())
for key in ['auth_key','k3s_token','ingress_config']:
    if not isinstance(data.get(key), str):
        raise SystemExit('Node input requires string field: ' + key)
    (p/key).write_text(data[key])
PY
if [[ "$phase" == cluster && "$role" == data ]]; then
  [[ -s "$credentials/ingress_config" ]] || fail 'The server requires a rendered Traefik HelmChartConfig'
elif [[ "$phase" == cluster ]]; then
  python3 - "$server_ip" <<'PY'
import ipaddress, sys
if ipaddress.ip_address(sys.argv[1]) not in ipaddress.ip_network('100.64.0.0/10'):
    raise SystemExit('Worker server address must be an explicit Tailscale IPv4 address')
PY
  [[ -s "$credentials/k3s_token" ]] || fail 'Workers require the Kubernetes server join token'
fi
service_hash() {
  for file in "$@"; do
    if [[ -f "$file" ]]; then sha256sum "$file"; else printf 'missing %s\n' "$file"; fi
  done | sha256sum
}
tailscale_before=$(service_hash /usr/local/bin/tailscaled /etc/systemd/system/tailscaled.service)
if [[ ! -x /usr/local/bin/tailscale ]] || [[ $(/usr/local/bin/tailscale version | head -1) != 1.102.4 ]]; then
  curl -fsSL https://pkgs.tailscale.com/stable/tailscale_1.102.4_amd64.tgz -o "$credentials/tailscale.tgz"
  echo "50748df1045e60b5b695f19f4c56b0da36c019948b440fb456b6584a50f0d8b9  $credentials/tailscale.tgz" | sha256sum -c - >/dev/null
  tar xzf "$credentials/tailscale.tgz" -C "$credentials"
  install -m 0755 "$credentials/tailscale_1.102.4_amd64/tailscale" /usr/local/bin/tailscale
  install -m 0755 "$credentials/tailscale_1.102.4_amd64/tailscaled" /usr/local/bin/tailscaled
fi
cat > /etc/systemd/system/tailscaled.service <<'UNIT'
[Unit]
Description=Tailscale private network
After=network-online.target
Wants=network-online.target
[Service]
ExecStart=/usr/local/bin/tailscaled --state=/var/lib/tailscale/tailscaled.state --socket=/run/tailscale/tailscaled.sock
Restart=on-failure
RestartSec=5
RuntimeDirectory=tailscale
StateDirectory=tailscale
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable tailscaled
if [[ "$tailscale_before" != "$(service_hash /usr/local/bin/tailscaled /etc/systemd/system/tailscaled.service)" ]]; then
  systemctl restart tailscaled
else
  systemctl start tailscaled
fi
if ! tailscale status --json | python3 -c 'import json,sys; sys.exit(json.load(sys.stdin)["BackendState"]!="Running")'; then
  if [[ ! -s "$credentials/auth_key" ]]; then
    echo "EXE_TAILSCALE_AUTH_KEY is required to enroll $name" >&2
    exit 1
  fi
  tailscale up --auth-key="file:$credentials/auth_key" --hostname="$name" --accept-dns=false --accept-routes=false
fi
node_ip=$(tailscale ip -4)
python3 - "$node_ip" <<'PY'
import ipaddress, sys
if ipaddress.ip_address(sys.argv[1]) not in ipaddress.ip_network('100.64.0.0/10'):
    raise SystemExit('The private network must supply a Tailscale IPv4 address')
PY
if [[ "$role" == backup || "$phase" == network ]]; then
  printf '%s\n' "$node_ip"
  exit 0
fi
fresh_server=false
if [[ "$role" == data && ! -s /var/lib/rancher/k3s/server/db/state.db ]]; then
  if systemctl is-active --quiet k3s || [[ -e /etc/rancher/k3s/k3s.yaml || -e /var/lib/rancher/k3s/server/token || -d /var/lib/rancher/k3s/server/db/etcd ]]; then
    fail 'Existing Kubernetes authority is missing its expected database; restore it before bootstrap'
  fi
  fresh_server=true
fi
k3s_before=$(service_hash /usr/local/bin/k3s /etc/rancher/k3s/config.yaml /etc/rancher/k3s/token /etc/systemd/system/k3s.service)
if [[ ! -x /usr/local/bin/k3s ]] || [[ $(k3s --version | head -1) != 'k3s version v1.35.4+k3s1 '* ]]; then
  curl -fsSL https://github.com/k3s-io/k3s/releases/download/v1.35.4%2Bk3s1/k3s -o "$credentials/k3s"
  echo "080498ece50acdaf23fe5d736f41c616e276a89a4e5605a3f274f39b0fcee85b  $credentials/k3s" | sha256sum -c - >/dev/null
  install -m 0755 "$credentials/k3s" /usr/local/bin/k3s
fi
install -d -m 0700 /etc/rancher/k3s
cat > /etc/rancher/k3s/config.yaml <<EOF
node-name: $name
node-ip: $node_ip
flannel-iface: tailscale0
node-label:
  - noebs.dev/role=$role
kubelet-arg:
  - address=$node_ip
EOF
if [[ "$role" == data ]]; then
  mode=server
  install -d -m 0700 /var/lib/rancher/k3s/server/manifests
  cat > /var/lib/rancher/k3s/server/manifests/00-noebs-namespace.yaml <<'YAML'
apiVersion: v1
kind: Namespace
metadata:
  name: noebs
YAML
  install -m 0600 "$credentials/ingress_config" /var/lib/rancher/k3s/server/manifests/noebs-traefik-config.yaml
  cat >> /etc/rancher/k3s/config.yaml <<EOF
bind-address: $node_ip
advertise-address: $node_ip
tls-san:
  - $node_ip
disable:
  - servicelb
write-kubeconfig-mode: "0600"
secrets-encryption: true
secrets-encryption-provider: secretbox
cluster-cidr: 10.242.0.0/16
service-cidr: 10.243.0.0/16
cluster-dns: 10.243.0.10
EOF
else
  mode=agent
  install -m 0600 "$credentials/k3s_token" /etc/rancher/k3s/token
  cat >> /etc/rancher/k3s/config.yaml <<EOF
server: https://$server_ip:6443
token-file: /etc/rancher/k3s/token
EOF
fi
if [[ "$fresh_server" == true ]]; then
  # Start the new API before installing namespace-scoped Traefik RBAC.
  cp /etc/rancher/k3s/config.yaml "$credentials/k3s-final-config.yaml"
  python3 - <<'PY'
from pathlib import Path
path = Path('/etc/rancher/k3s/config.yaml')
path.write_text(path.read_text().replace('disable:\n', 'disable:\n  - traefik\n', 1))
PY
fi
cat > /etc/systemd/system/k3s.service <<EOF
[Unit]
Description=Noebs Kubernetes $mode
After=network-online.target tailscaled.service
Wants=network-online.target
Requires=tailscaled.service
[Service]
Type=notify
ExecStart=/usr/local/bin/k3s $mode --config /etc/rancher/k3s/config.yaml
KillMode=process
Delegate=yes
LimitNOFILE=1048576
LimitNPROC=infinity
LimitCORE=infinity
TasksMax=infinity
Restart=always
RestartSec=5
[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable k3s
if [[ "$k3s_before" != "$(service_hash /usr/local/bin/k3s /etc/rancher/k3s/config.yaml /etc/rancher/k3s/token /etc/systemd/system/k3s.service)" ]]; then
  systemctl restart k3s
else
  systemctl start k3s
fi
wait_for_api() {
  for attempt in {1..60}; do
    if k3s kubectl get --raw=/readyz >/dev/null 2>&1; then break; fi
    sleep 2
  done
  k3s kubectl get --raw=/readyz >/dev/null
}
if [[ "$role" == data ]]; then
  wait_for_api
  if [[ "$fresh_server" == true ]]; then
    k3s kubectl apply --server-side --field-manager=noebs-bootstrap -f /var/lib/rancher/k3s/server/manifests/00-noebs-namespace.yaml >/dev/null
    k3s kubectl wait --for=create namespace/noebs --timeout=60s >/dev/null
    install -m 0600 "$credentials/k3s-final-config.yaml" /etc/rancher/k3s/config.yaml
    systemctl restart k3s
    wait_for_api
  fi
fi
