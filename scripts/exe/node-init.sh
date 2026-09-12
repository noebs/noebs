#!/usr/bin/env bash
set -euo pipefail
name=${1:?set VM name}
role=${2:?set node role}
server_ip=${3-}
phase=${4:?set network or cluster phase}
[[ "$name" =~ ^noebs-[a-z0-9-]+$ ]]
[[ "$role" == data || "$role" == workers || "$role" == backup ]]
umask 077
credentials=$(mktemp -d)
trap 'rm -rf "$credentials"' EXIT
cat > "$credentials/input.json"
python3 - "$credentials" <<'PY'
import json,sys
from pathlib import Path
p=Path(sys.argv[1]); data=json.loads((p/'input.json').read_text())
for key in ['auth_key','k3s_token']:
    (p/key).write_text(data[key])
PY
[[ $(ps -p 1 -o comm=) == systemd ]]
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
if [[ "$role" == backup || "$phase" == network ]]; then
  printf '%s\n' "$node_ip"
  exit 0
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
  cat >> /etc/rancher/k3s/config.yaml <<EOF
bind-address: $node_ip
advertise-address: $node_ip
tls-san:
  - $node_ip
disable:
  - traefik
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
  [[ "$server_ip" =~ ^100\.[0-9]+\.[0-9]+\.[0-9]+$ ]]
  test -s "$credentials/k3s_token"
  install -m 0600 "$credentials/k3s_token" /etc/rancher/k3s/token
  cat >> /etc/rancher/k3s/config.yaml <<EOF
server: https://$server_ip:6443
token-file: /etc/rancher/k3s/token
EOF
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
if [[ "$role" == data ]]; then
  for attempt in {1..60}; do
    if k3s kubectl get --raw=/readyz >/dev/null 2>&1; then break; fi
    sleep 2
  done
  k3s kubectl get --raw=/readyz >/dev/null
fi
