# Noebs host policy

`noebs-public-docker-firewall` makes Docker's public ingress policy independent
of the ports individual Compose projects publish. New connections arriving on
the public interface cannot be forwarded to Docker bridge networks. Host input
(including Caddy on ports 80 and 443) and traffic arriving through Tailscale are
not affected.

Install the version from the checked-out release:

```sh
sudo install -m 0755 deploy/host/noebs-public-docker-firewall \
  /usr/local/sbin/noebs-public-docker-firewall
sudo install -m 0644 deploy/host/noebs-public-docker-firewall.service \
  /etc/systemd/system/noebs-public-docker-firewall.service
sudo systemctl daemon-reload
sudo systemctl enable --now noebs-public-docker-firewall.service
sudo systemctl disable --now noebs-public-management-firewall.service
```

The service is coupled to Docker restarts so that the rules are restored after
Docker recreates `DOCKER-USER`. Override `PUBLIC_INTERFACE` in a systemd drop-in
when the public interface is not `eth0`.

## K3s control-plane policy

`k3s-config.yaml` is the complete server configuration for the current host.
It keeps the cluster-admin kubeconfig at mode `0600`, declares the API server
SANs, and selects `secretbox` encryption for Kubernetes Secrets. The systemd
drop-in removes install-time command-line arguments so they cannot override the
repository-owned configuration.

On a cluster that was created without Secret encryption, use the K3s
existing-cluster transition in this exact order. Do not install the
`secrets-encryption` configuration before `k3s secrets-encrypt enable`. The
transition requires K3s v1.35.3 or newer on the v1.35 line. The database and
server token backup are one restore artifact; a restored datastore is unusable
without the matching token.

```bash
set -euo pipefail

: "${K3S_BACKUP_ROOT:?set an approved encrypted mounted backup root}"

wait_for_k3s() {
  for _ in $(seq 1 90); do
    if sudo k3s kubectl get --raw=/readyz >/dev/null 2>&1 \
      && sudo k3s kubectl wait --for=condition=Ready node --all \
        --timeout=10s >/dev/null 2>&1
    then
      return 0
    fi
    sleep 2
  done
  return 1
}

k3s_version="$(sudo k3s --version | awk 'NR == 1 { print $3 }')"
[[ "$k3s_version" =~ ^v1\.35\.([0-9]+)\+k3s[0-9]+$ ]]
((BASH_REMATCH[1] >= 3))

backup_root="$(sudo realpath -e "$K3S_BACKUP_ROOT")"
backup_mount="$(findmnt -n -o TARGET -T "$backup_root")"
test -n "$backup_mount"
test "$backup_mount" != "/"
mountpoint -q "$backup_mount"
backup_dir="$backup_root/k3s-before-secret-encryption-$(date -u +%Y%m%dT%H%M%SZ)"
sudo install -d -m 0700 "$backup_dir"
sudo systemctl stop k3s
sudo cp -a /var/lib/rancher/k3s/server/db "$backup_dir/db"
sudo install -m 0600 /var/lib/rancher/k3s/server/token \
  "$backup_dir/server-token"
sudo test -s "$backup_dir/db/state.db"
sudo test -s "$backup_dir/server-token"
sudo systemctl start k3s
wait_for_k3s

encryption_status="$(sudo k3s secrets-encrypt status)"
test "$encryption_status" = 'Encryption Status: Disabled, no configuration file found'
encryption_json="$(sudo k3s secrets-encrypt status --output json)"
jq -e '
  keys == ["activekey", "stage"]
  and .stage == ""
  and .activekey == ""
' <<<"$encryption_json" >/dev/null

sudo k3s secrets-encrypt enable
sudo install -m 0600 deploy/host/k3s-config.yaml /etc/rancher/k3s/config.yaml
sudo install -d -m 0755 /etc/systemd/system/k3s.service.d
sudo install -m 0644 deploy/host/k3s.service.d/20-noebs-security.conf \
  /etc/systemd/system/k3s.service.d/20-noebs-security.conf
sudo systemctl daemon-reload
sudo systemctl restart k3s
wait_for_k3s

encryption_status="$(sudo k3s secrets-encrypt status)"
grep -Fx 'Encryption Status: Disabled' <<<"$encryption_status" >/dev/null
grep -Fx 'Current Rotation Stage: start' <<<"$encryption_status" >/dev/null
grep -Fx 'Server Encryption Hashes: All hashes match' \
  <<<"$encryption_status" >/dev/null
encryption_json="$(sudo k3s secrets-encrypt status --output json)"
jq -e '
  .enable == false
  and .stage == "start"
  and .hashmatch == true
  and .activekey == ""
  and any(.inactivekeys[]?;
    startswith("XSalsa20-POLY1305 secretboxkey-"))
' <<<"$encryption_json" >/dev/null

sudo k3s secrets-encrypt rotate-keys
sudo systemctl restart k3s
wait_for_k3s

encryption_status="$(sudo k3s secrets-encrypt status)"
grep -Fx 'Encryption Status: Enabled' <<<"$encryption_status" >/dev/null
grep -Fx 'Current Rotation Stage: reencrypt_finished' \
  <<<"$encryption_status" >/dev/null
grep -Fx 'Server Encryption Hashes: All hashes match' \
  <<<"$encryption_status" >/dev/null
encryption_json="$(sudo k3s secrets-encrypt status --output json)"
jq -e '
  .enable == true
  and .stage == "reencrypt_finished"
  and .hashmatch == true
  and (.activekey | startswith("XSalsa20-POLY1305 secretboxkey-"))
' <<<"$encryption_json" >/dev/null
```

The assertions stop the transition unless the initial status is the exact
unconfigured state. `enable` completes before the repository config is
installed. The first restart must be API-ready with every node Ready, disabled
at stage `start`, hash-consistent, and holding the expected inactive secretbox
key; only then may rotation begin. The final restart must pass both readiness
checks and report enabled encryption, `reencrypt_finished`, matching hashes,
and an active secretbox key. Status output is captured only for assertions so
key names are not written to a log.

`K3S_BACKUP_ROOT` must already be a mounted, operator-approved encrypted
filesystem; the procedure rejects the unencrypted root mount and writes the
database and token directly to the approved store. Never print or separate the
copied token from its database backup. Retain the pair according to the cluster
recovery policy. Do not remove the generated file at
`/var/lib/rancher/k3s/server/cred/encryption-config.json`; it contains the key
required to decrypt current cluster state and is protected by the server
filesystem.

For a new cluster, or one whose status already reports encryption enabled,
installing the repository config and systemd drop-in does not require the
existing-cluster `enable` transition:

```sh
sudo install -m 0600 deploy/host/k3s-config.yaml /etc/rancher/k3s/config.yaml
sudo install -d -m 0755 /etc/systemd/system/k3s.service.d
sudo install -m 0644 deploy/host/k3s.service.d/20-noebs-security.conf \
  /etc/systemd/system/k3s.service.d/20-noebs-security.conf
sudo systemctl daemon-reload
```

The ordering follows the official K3s
[existing-cluster encryption procedure](https://docs.k3s.io/cli/secrets-encrypt#enable-secrets-encryption-on-an-existing-cluster)
and [datastore backup and restore requirements](https://docs.k3s.io/datastore/backup-restore).
