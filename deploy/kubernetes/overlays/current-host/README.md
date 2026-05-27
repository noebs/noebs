# current-host Overlay

This overlay targets the existing deployment host `100.102.164.34`.

Required DNS records:

- `api.noebs.sd A 100.102.164.34`
- `dsa.adonese.sd A 100.102.164.34`

Required Kubernetes Secrets in namespace `noebs`:

- `noebs-secrets` with key `secrets.yaml`. It must contain the merged secret material expected by the runtime, including `noebs.default_tenant_id`, `noebs.db_url`, JWT/admin keys, EBS credentials, data key, and PSP secrets.
- `sops-age-key` with key `age-key.txt`.
- `postgres-credentials` with key `password`.
- `temporal-postgres-credentials` with key `password`.
- `noebs-tls` TLS secret for `api.noebs.sd` and `dsa.adonese.sd`.

Noebs service roles are selected by mounted config, not environment variables. The base `noebs-config` ConfigMap provides shared `config.yaml` and one `*.service.yaml` key per workload.

Render check:

```sh
kubectl kustomize deploy/kubernetes/overlays/current-host
```
