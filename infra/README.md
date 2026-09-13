# Noebs infrastructure

`infra/deploy` provisions the exe.dev fleet, joins its private Kubernetes cluster,
prepares runtime credentials, runs database migrations and identity bootstrap,
deploys noebs, and verifies the public API before reporting success. Re-running
the command deploys another immutable application release. There is one deployment
path; it does not install Mojaloop or invoke another repository.

## Fleet

| VM | Responsibility |
| --- | --- |
| `noebs-control` | Terraform state and deployment credentials |
| `noebs-data` | k3s control plane, PostgreSQL, Kafka, Keycloak and Temporal |
| `noebs-workers` | Noebs services, workers and Traefik ingress |
| `noebs-backup` | Encrypted backup storage |
| `noebs-telegram` | Existing notification VM, outside Kubernetes |

The desired machines and capacities are in `exedev/fleet.json`. Terraform owns
their lifecycle; Kubernetes schedules workloads and CoreDNS resolves their
Services. Internal calls use names such as `wallet-ledger.noebs.svc.cluster.local`.
The explicit `public_http` field keeps only `noebs-workers` public across releases;
the other VMs remain private. Promotion sets its HTTP proxy port to 8081.
The data node is the single Kubernetes control plane and storage node. This
topology does not provide control-plane or database failover.

Tailscale provides the private network between VMs. k3s uses `tailscale0` for its
node traffic and Flannel network. Its API binds to the private node address;
cluster Secrets are encrypted at rest. The bootstrap validates systemd, cgroups,
TUN and kernel features before installing services. The current pinned versions
are k3s `v1.35.4+k3s1` and Tailscale `1.102.4`, with download checksums in
`scripts/node-init.sh`.

Public traffic follows `api.noebs.sd → exe.dev TLS proxy → Traefik → Kubernetes
Service`. Traefik runs on `noebs-workers:8081`, discovers endpoints from Kubernetes,
and verifies the gateway's TLS certificate using the release CA and an mTLS client
identity. Public Keycloak routes are limited by method and path. Administration
and master-realm routes return 404. Android app association is served by noebs.

## Deploy

Install Go at the version in `go.mod`, Python 3 with PyYAML, OpenSSH, `dig`, SOPS
`3.10.2`, crane `0.21.0` and kustomize `5.8.1`. The deployment installs its pinned
Terraform binary and builds the repository's exe.dev provider. Publishing an image
also requires Docker Buildx authenticated to `ghcr.io/noebs/noebs`.

Use a registered exe.dev SSH key and an age identity that decrypts
`exe/release.secrets.yaml` and `exe/bootstrap.secrets.yaml`. Both key files must
have mode 0600. Supply `EXE_TAILSCALE_AUTH_KEY` when enrolling new nodes. Existing
Tailscale identities are retained.

Copy `deployment.yaml.example` to a private local file and set the exact ingress
proxy source CIDRs. These are the trusted proxies that may supply the client IP;
they must be established from the provider's forwarding contract. The deployment
does not infer trust from an address being private. `service_config` contains
explicit application settings keyed by service role; `{}` enables no external
banking integration.

Configure a DNS-only CNAME `api.noebs.sd → noebs-workers.exe.xyz`. The deploy
command checks it and verifies domain registration over HTTPS. An already
registered domain needs no registration permission on subsequent releases.
When EXE reports `Domain Not Configured`, registration requires an SSH key allowed
to run `domain add`. An owner can register it once with
`ssh exe.dev domain add noebs-workers api.noebs.sd`.
exe.dev supplies the public certificate. DNS credentials are not needed by the deployment.

```sh
infra/deploy \
  --config /private/noebs/deployment.yaml \
  --key /private/noebs/exe-key \
  --age-key /private/noebs/age-key.txt \
  --work /private/noebs/release
```

The source checkout must be committed and clean. Without `--receipts`, the command
builds and publishes that commit, then verifies its image receipt against GHCR.
To use an already published image, add `--receipts /private/noebs/receipts`; the
directory must contain `noebs-receipt.json` for the exact source revision.

Add `--check` to validate fleet configuration, decrypt and validate credentials,
prepare service configuration and render manifests without modifying the fleet.
Prepared plaintext is kept in private temporary directories and removed afterward.
Successful deployment writes `release-receipt.json` and encrypted Terraform state
under `--work`. It waits for migrations, workloads and public HTTPS checks; a failed
step exits with an error.

The `Immutable noebs release` GitHub workflow publishes only the noebs image. The
`Deploy exe fleet` workflow calls the same command. It requires secrets
`EXE_SSH_PRIVATE_KEY`, `EXE_AGE_PRIVATE_KEY`, and `EXE_TAILSCALE_AUTH_KEY`, plus the
repository variable `NOEBS_DEPLOYMENT_CONFIG` containing the deployment YAML.

## External banking services

Mojaloop owns its own deployment, realm, data, availability and release schedule.
Managing both repositories in development does not change that production
boundary. Noebs connects as a client; its deployment does not provision a switch,
seed switch participants, transfer switch credentials or wait for a switch release.

The current application adapter speaks the SDK HTTP contract. To enable it, put
the following explicit application settings under `service_config.wallet-worker`:

```yaml
service_config:
  wallet-worker:
    interop_tenant: tenant-bank
    interop_fsp_id: noebs
    interop_sdk_outbound_url: http://100.64.0.10:4001
    interop_sdk_inbound_url: http://100.64.0.10:4000
    interop_backend_listen_address: 0.0.0.0:4002
    interop_backend_allowed_peers: [100.64.0.10]
```

These are example addresses, not deployed endpoints. The configured tenant must
exist in the tenant catalog. The integration operator supplies the reachable SDK
contract; a switch FSPIOP URL is not interchangeable with that SDK endpoint.
The callback is exposed at the worker's private address on port `30402`, with
source IP preservation and an exact allowed-peer policy. Forwarded HTTP headers
cannot authorize callbacks. Configure the external SDK to use that callback URL.
IP endpoints receive destination-specific egress rules; DNS endpoints receive
rules limited to their configured TCP ports because Kubernetes NetworkPolicy does
not resolve DNS names.

A VPN can supply private connectivity without sharing a Kubernetes cluster.
If the service requires OIDC federation or another authentication contract, that
must be implemented and configured in the client integration; this infrastructure
does not assume that two services share a realm or claim to provide federation.

## Operations

Use the data node's private kubeconfig or `sudo k3s kubectl` over SSH to inspect
the cluster. The `noebs-release` ConfigMap records the deployed source revision,
image digest and configuration fingerprint. Identity maintenance uses
`kubernetes/operations/run-keycloak-job.sh` and validates that release identity.

Storage uses the `noebs-retain` class. Existing PVCs are not replaced by deployment.
Backup tooling captures only noebs-owned databases, Kafka and Kubernetes state in
an encrypted coordinated snapshot. Scheduled backups retain the existing disabled
policy; installing the tools does not introduce a recurring downtime window.
Run `scripts/coordinated_backup.py --help` for the explicit backup operation.

The first successful release removes the superseded fleet Caddy deployment and
embedded adapter workloads, while retaining their persistent volume claims.
The old server also serves unrelated domains; this deployment does not manage it.
Pointing the noebs domain directly at exe.dev removes it from the noebs traffic path.

Provider and platform references: [exe.dev private networking](https://exe.dev/docs/faq/cross-vm-networking),
[custom domains](https://exe.dev/docs/cnames), and
[k3s networking services](https://docs.k3s.io/networking/networking-services).
