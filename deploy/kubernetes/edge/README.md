# Edge Caddy

This kustomization owns the existing single-node `edge/caddy` deployment and
its complete shared Caddy configuration. It deliberately retains the other
host routes already served by that process while adding Noebs Android App
Links and the safe browser fallback for payment capability URLs.

The Caddy image is pinned by digest. Validate it from the repository root:

```sh
kubectl kustomize deploy/kubernetes/edge >/dev/null
kubectl apply --dry-run=server -k deploy/kubernetes/edge >/dev/null
```

Normal ownership is the foundation-managed `noebs-edge` Argo CD Application;
do not maintain a parallel manual apply workflow. The first adoption is gated
by `create_edge_application` in `foundation/terraform` so its plan and live
diff can be reviewed before self-heal is enabled.

The deployment uses `Recreate` because it binds ports 80 and 443 directly on
the single current host. A pod-template change therefore causes a brief edge
interruption. Kustomize gives the generated Caddy ConfigMap a content hash, so
a reviewed configuration change updates the pod template and Argo performs the
required rollout automatically; the old ConfigMap is pruned last.
