# Edge Caddy

This kustomization owns the existing single-node `edge/caddy` deployment and
its complete shared Caddy configuration. It deliberately retains the other
host routes already served by that process while adding Noebs Android App
Links and the safe browser fallback for payment capability URLs.

The Caddy image is pinned by digest. Validate and apply it from the repository
root:

```sh
kubectl kustomize deploy/kubernetes/edge >/dev/null
kubectl apply -k deploy/kubernetes/edge
kubectl -n edge rollout status deployment/caddy --timeout=120s
```

The deployment uses `Recreate` because it binds ports 80 and 443 directly on
the single current host. Applying a pod-template change therefore causes a
brief edge interruption. The ConfigMap is mounted with `subPath`, so a
ConfigMap-only apply must also be followed by a deliberate rollout restart
after the candidate Caddyfile has been validated.
