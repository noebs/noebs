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
