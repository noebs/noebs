variable "deployment_host" {
  description = "Existing noebs deployment host."
  type        = string
  nullable    = false

  validation {
    condition     = var.deployment_host == "100.102.164.34"
    error_message = "deployment_host must be the current noebs host: 100.102.164.34."
  }
}

variable "kubeconfig_path" {
  description = "Path to the kubeconfig for the cluster on the deployment host."
  type        = string
  nullable    = false

  validation {
    condition     = trimspace(var.kubeconfig_path) != ""
    error_message = "kubeconfig_path must be explicit."
  }
}

variable "argocd_namespace" {
  description = "Namespace where Argo CD is installed."
  type        = string
  nullable    = false

  validation {
    condition     = trimspace(var.argocd_namespace) != ""
    error_message = "argocd_namespace must be explicit."
  }
}

variable "noebs_namespace" {
  description = "Namespace where noebs microservices are deployed."
  type        = string
  nullable    = false

  validation {
    condition     = trimspace(var.noebs_namespace) != ""
    error_message = "noebs_namespace must be explicit."
  }
}

variable "edge_namespace" {
  description = "Namespace where the current-host Caddy edge is deployed."
  type        = string
  nullable    = false

  validation {
    condition     = trimspace(var.edge_namespace) != ""
    error_message = "edge_namespace must be explicit."
  }
}

variable "argocd_chart_version" {
  description = "argo-cd Helm chart version."
  type        = string
  nullable    = false

  validation {
    condition     = trimspace(var.argocd_chart_version) != ""
    error_message = "argocd_chart_version must be explicit."
  }
}

variable "argocd_installation_mode" {
  description = "How foundation handles Argo CD installation: existing for the current host install, helm for a foundation-managed Helm install."
  type        = string
  nullable    = false

  validation {
    condition     = contains(["existing", "helm"], var.argocd_installation_mode)
    error_message = "argocd_installation_mode must be either existing or helm."
  }
}

variable "noebs_repo_url" {
  description = "Git repository URL used by Argo CD."
  type        = string
  nullable    = false

  validation {
    condition     = trimspace(var.noebs_repo_url) != ""
    error_message = "noebs_repo_url must be explicit."
  }
}

variable "noebs_target_revision" {
  description = "Git revision used by Argo CD."
  type        = string
  nullable    = false

  validation {
    condition     = trimspace(var.noebs_target_revision) != ""
    error_message = "noebs_target_revision must be explicit."
  }
}

variable "noebs_manifest_path" {
  description = "Kustomize path used by Argo CD for noebs."
  type        = string
  nullable    = false

  validation {
    condition     = trimspace(var.noebs_manifest_path) != ""
    error_message = "noebs_manifest_path must be explicit."
  }
}

variable "edge_manifest_path" {
  description = "Kustomize path used by Argo CD for the current-host edge."
  type        = string
  nullable    = false

  validation {
    condition     = trimspace(var.edge_manifest_path) != ""
    error_message = "edge_manifest_path must be explicit."
  }
}

variable "create_noebs_application" {
  description = "Whether to create the Noebs Argo CD Application after the explicit release Secrets exist."
  type        = bool
  nullable    = false
}

variable "create_edge_application" {
  description = "Whether to create the current-host edge Argo CD Application."
  type        = bool
  nullable    = false
}
