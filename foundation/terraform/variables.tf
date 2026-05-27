variable "deployment_host" {
  description = "Existing noebs deployment host."
  type        = string
  default     = "100.102.164.34"

  validation {
    condition     = var.deployment_host == "100.102.164.34"
    error_message = "deployment_host must be the current noebs host: 100.102.164.34."
  }
}

variable "kubeconfig_path" {
  description = "Path to the kubeconfig for the cluster on the deployment host."
  type        = string
  default     = "~/.kube/noebs-current-host.yaml"
}

variable "argocd_namespace" {
  description = "Namespace where Argo CD is installed."
  type        = string
  default     = "argocd"
}

variable "noebs_namespace" {
  description = "Namespace where noebs microservices are deployed."
  type        = string
  default     = "noebs"
}

variable "argocd_chart_version" {
  description = "argo-cd Helm chart version."
  type        = string
  default     = "8.5.7"
}

variable "noebs_repo_url" {
  description = "Git repository URL used by Argo CD."
  type        = string
  default     = "https://github.com/adonese/noebs.git"
}

variable "noebs_target_revision" {
  description = "Git revision used by Argo CD."
  type        = string
  default     = "master"
}

variable "noebs_manifest_path" {
  description = "Kustomize path used by Argo CD for noebs."
  type        = string
  default     = "deploy/kubernetes/overlays/current-host"
}
