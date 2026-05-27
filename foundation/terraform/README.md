# noebs Foundation Terraform

This root owns platform-level deployment wiring for the existing host `100.102.164.34`:

- install Argo CD into the configured cluster;
- create the noebs Argo CD project;
- create the noebs Argo CD application pointing at `deploy/kubernetes/overlays/current-host`.

Required variables are listed in `terraform.tfvars.example`. Copying that file is not required by the code; provide the variables through your normal Terraform workflow.

Commands:

```sh
terraform -chdir=foundation/terraform init
terraform -chdir=foundation/terraform plan -var-file=terraform.tfvars
terraform -chdir=foundation/terraform apply -var-file=terraform.tfvars
```

The Kubernetes cluster itself must already be reachable through `kubeconfig_path`. Cluster bootstrap for the host should be handled before applying this root so Terraform can manage Argo CD through the Kubernetes API.
