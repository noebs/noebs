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

Install the files without restarting K3s:

```sh
sudo install -m 0600 deploy/host/k3s-config.yaml /etc/rancher/k3s/config.yaml
sudo install -d -m 0755 /etc/systemd/system/k3s.service.d
sudo install -m 0644 deploy/host/k3s.service.d/20-noebs-security.conf \
  /etc/systemd/system/k3s.service.d/20-noebs-security.conf
sudo systemctl daemon-reload
```

On a cluster that was created without Secret encryption, use the K3s
existing-cluster transition in this exact order. It requires K3s v1.35.3 or
newer on the v1.35 line:

```sh
sudo install -d -m 0700 /root/noebs-backups
sudo sqlite3 /var/lib/rancher/k3s/server/db/state.db \
  ".backup '/root/noebs-backups/k3s-before-secret-encryption.db'"
sudo chmod 0600 /root/noebs-backups/k3s-before-secret-encryption.db
sudo k3s secrets-encrypt status
sudo k3s secrets-encrypt enable
sudo systemctl restart k3s
sudo k3s secrets-encrypt status
sudo k3s secrets-encrypt rotate-keys
sudo systemctl restart k3s
sudo k3s secrets-encrypt status
```

The first status must report that encryption is disabled with no configuration
file. After the first restart it must report stage `start`. The final status
must report encryption enabled, stage `reencrypt_finished`, matching server
hashes, and an active `secretbox` key. Confirm the node is Ready and
`/readyz?verbose` passes after each restart. Do not remove the generated file at
`/var/lib/rancher/k3s/server/cred/encryption-config.json`; it contains the key
required to decrypt cluster state and is protected by the server filesystem.
The SQLite backup is equally sensitive; remove it after the encrypted cluster
and replacement release credentials have been verified.
