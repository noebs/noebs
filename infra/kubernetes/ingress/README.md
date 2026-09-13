# Noebs ingress

The k3s controller owns Traefik. `traefik-config.yaml` configures the bundled
Traefik 3.6.13 chart in k3s v1.35.4+k3s1. Bootstrap installs its rendered
HelmChartConfig before enabling Traefik. The controller listens on the
`noebs-workers` host at port 8081; exe.dev terminates public HTTPS and forwards
requests there. Its health listener binds only to `127.0.0.1:9001`.

Apply `kustomization.yaml` after the Traefik CRDs exist. All routes, transports,
and `edge-internal-transport` are in `noebs`. The Secret contains `ca.crt`,
`tls.crt`, and `tls.key`. Traefik verifies each upstream's CA and service DNS
name; the API gateway additionally authenticates the dedicated edge client
certificate. Kubernetes discovers ready service endpoints. Noebs deployment
does not install or manage external payment providers.

The only public host is `api.noebs.sd`. Explicit method/path rules expose the
required Keycloak browser and metadata endpoints. Other `/auth` paths return
404. Encoded slash, backslash, and NUL characters are rejected before routing.
Android App Links are served by the API gateway. Original Host reaches both
upstreams, and forwarded authority is fixed to `https://api.noebs.sd:443`.

The deployment must provide explicit `ports.web.forwardedHeaders.trustedIPs`.
The exe.dev proxy was verified to append the actual client IP to incoming
`X-Forwarded-For`, while preserving caller-supplied `X-Real-IP`. For a trusted
proxy, Traefik appends the immediate proxy peer after that client address. The
gateway uses this penultimate canonical IP, ignoring caller-controlled prefixes.
For an untrusted direct peer, Traefik discards incoming forwarding headers and
produces one address. The gateway accepts that sole canonical address.
`X-Real-IP` is removed at ingress and never used as client identity. A replacement
trusted proxy must satisfy the same append contract before its CIDR is admitted.
The API TLS listener permits the authenticated edge identity; network reachability
alone does not grant authority to supply source headers.

Keycloak discards the incoming source chain and records the immediate proxy peer;
this prevents a caller-controlled prefix becoming Keycloak's client identity,
while the API gateway retains the verified client IP for Noebs authorization.

Access logs retain timing, response status, method, host, and router/service names.
They omit request paths, queries, and headers to avoid recording login codes,
tokens, authorization values, and redirect locations.

Run the local routing and transport tests with the exact bundled binary:

```sh
TRAEFIK_BINARY=/path/to/traefik python3 -m unittest infra/scripts/test_ingress.py -v
go test ./cli -run 'TestGatewayRequestSource|TestAndroidAssetLinks|TestEdgeIdentityIsTheOnlyExternalGatewayPeer'
```

The integration tests start local TLS upstreams and exercise the actual Traefik
router, including every allowed auth method/path, denied paths, original Host,
spoofed forwarding headers, unknown CAs, wrong DNS names, and absent mTLS identity.
The controller values also render with the upstream Helm chart 39.0.7.

References: [k3s bundled chart](https://github.com/k3s-io/k3s/blob/v1.35.4%2Bk3s1/manifests/traefik.yaml),
[Traefik CRD transport and routing](https://doc.traefik.io/traefik/v3.6/reference/routing-configuration/kubernetes/crd/http/serverstransport/),
[forwarded header implementation](https://github.com/traefik/traefik/blob/v3.6.13/pkg/middlewares/forwardedheaders/forwarded_header.go),
[Helm values](https://github.com/traefik/traefik-helm-chart/blob/v39.0.7/traefik/values.yaml).
