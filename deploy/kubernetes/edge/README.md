# Edge Caddy

This kustomization owns the existing single-node `edge/caddy` deployment and
its complete shared Caddy configuration. It deliberately retains the other
host routes already served by that process while adding Noebs Android App
Links.

The `iptv.2t.sd` site sends `/account` and `/account/*` to the host-loopback
OpenIPTV account service on port `8090`. Its final handle sends every other
path to the catalog on the host's Tailscale address and port `8088`. Keep the
account handle before that fallback so Caddy never forwards account bearer
requests to the catalog service.

The public Keycloak surface is an exact method-and-path allowlist for OIDC
discovery, JWKS, authorization-code exchange, logout, login actions, Google
brokering, and theme assets. A blanket `/auth` denial follows that allowlist;
admin, account, dynamic-registration, SAML, device, PAR, CIBA, introspection,
userinfo, revocation, and broker token/link endpoints are not public routes.

The Caddy image is pinned by digest. Validate it from the repository root:

```sh
kubectl kustomize deploy/kubernetes/edge >/dev/null
kubectl apply --dry-run=server -k deploy/kubernetes/edge >/dev/null
```

Normal ownership is the foundation-managed `noebs-edge` Argo CD Application;
do not maintain a parallel manual apply workflow. The first adoption is gated
by `create_edge_application` in `foundation/terraform` so its plan and live
diff can be reviewed before self-heal is enabled.

On the current host, prepare the two retained Docker-volume directories for
Caddy's fixed `10001:10001` identity immediately before enabling that first
adoption. These commands do not read or print the retained TLS material:

```sh
sudo install -d -m 0700 /var/lib/docker/volumes/noebs_caddy_data/_data /var/lib/docker/volumes/noebs_caddy_config/_data
sudo chown -R -- 10001:10001 /var/lib/docker/volumes/noebs_caddy_data/_data /var/lib/docker/volumes/noebs_caddy_config/_data
sudo stat --format='%u:%g %a %n' /var/lib/docker/volumes/noebs_caddy_data/_data /var/lib/docker/volumes/noebs_caddy_config/_data
```

Both `stat` lines must start with `10001:10001`. Do not enable the edge
Application until that check succeeds.

The deployment uses the host network because this shared edge still proxies
host-loopback services owned outside this repository. `Recreate` is required
because only one process can bind ports 80 and 443 on the single current host.
A pod-template change therefore causes a brief edge interruption. Kustomize
gives the generated Caddy ConfigMap a content hash, so a reviewed configuration
change updates the pod template and Argo performs the required rollout
automatically; the old ConfigMap is pruned last.
