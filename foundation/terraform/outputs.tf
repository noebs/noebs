output "deployment_host" {
  value = var.deployment_host
}

output "argocd_namespace" {
  value = var.argocd_namespace
}

output "noebs_namespace" {
  value = kubernetes_namespace_v1.noebs.metadata[0].name
}

output "noebs_manifest_path" {
  value = var.noebs_manifest_path
}

output "create_noebs_application" {
  value = var.create_noebs_application
}

output "noebs_service_discovery" {
  value = {
    for name, service in local.noebs_service_catalog :
    name => {
      endpoint = "${name}.${kubernetes_namespace_v1.noebs.metadata[0].name}.svc.cluster.local:${service.port}"
      port     = service.port
      protocol = service.protocol
    }
  }
}

output "noebs_database_ownership" {
  value = local.noebs_database_catalog
}

output "noebs_required_kubernetes_secrets" {
  value = sort(local.noebs_required_kubernetes_secrets)
}

output "noebs_required_kubernetes_secret_keys" {
  value = local.noebs_required_kubernetes_secret_keys
}
