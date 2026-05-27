variable "deployment_host" {
  description = "Existing noebs deployment host."
  type        = string

  validation {
    condition     = var.deployment_host == "100.102.164.34"
    error_message = "deployment_host must be the current noebs host: 100.102.164.34."
  }
}

variable "kubeconfig_path" {
  description = "Path to the kubeconfig for the cluster on the deployment host."
  type        = string
}

variable "argocd_namespace" {
  description = "Namespace where Argo CD is installed."
  type        = string
}

variable "argocd_chart_version" {
  description = "argo-cd Helm chart version."
  type        = string
}

variable "noebs_repo_url" {
  description = "Git repository URL used by Argo CD."
  type        = string
}

variable "noebs_target_revision" {
  description = "Git revision used by Argo CD."
  type        = string
}

variable "noebs_manifest_path" {
  description = "Kustomize path used by Argo CD for noebs."
  type        = string
}
