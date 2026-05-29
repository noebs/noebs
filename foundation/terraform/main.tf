provider "kubernetes" {
  config_path = var.kubeconfig_path
}

provider "helm" {
  kubernetes = {
    config_path = var.kubeconfig_path
  }
}

resource "kubernetes_namespace_v1" "argocd" {
  count = var.argocd_installation_mode == "helm" ? 1 : 0

  metadata {
    name = var.argocd_namespace
  }
}

data "kubernetes_namespace_v1" "argocd_existing" {
  count = var.argocd_installation_mode == "existing" ? 1 : 0

  metadata {
    name = var.argocd_namespace
  }
}

resource "kubernetes_namespace_v1" "noebs" {
  metadata {
    name = var.noebs_namespace

    labels = {
      "app.kubernetes.io/name"       = var.noebs_namespace
      "app.kubernetes.io/part-of"    = "noebs"
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }
}

resource "helm_release" "argocd" {
  count = var.argocd_installation_mode == "helm" ? 1 : 0

  name       = "argocd"
  repository = "https://argoproj.github.io/argo-helm"
  chart      = "argo-cd"
  version    = var.argocd_chart_version
  namespace  = var.argocd_namespace

  set = [
    {
      name  = "server.service.type"
      value = "ClusterIP"
    },
  ]
}

resource "kubernetes_manifest" "noebs_project" {
  manifest = {
    apiVersion = "argoproj.io/v1alpha1"
    kind       = "AppProject"
    metadata = {
      name      = "noebs"
      namespace = var.argocd_namespace
    }
    spec = {
      sourceRepos = [
        var.noebs_repo_url,
      ]
      destinations = [
        {
          namespace = kubernetes_namespace_v1.noebs.metadata[0].name
          server    = "https://kubernetes.default.svc"
        },
      ]
      namespaceResourceWhitelist = [
        {
          group = "*"
          kind  = "*"
        },
      ]
    }
  }

  depends_on = [
    helm_release.argocd,
    data.kubernetes_namespace_v1.argocd_existing,
  ]
}

data "kubernetes_secret_v1" "noebs_required" {
  for_each = var.create_noebs_application ? toset(local.noebs_required_kubernetes_secrets) : toset([])

  metadata {
    name      = each.key
    namespace = kubernetes_namespace_v1.noebs.metadata[0].name
  }

  depends_on = [kubernetes_namespace_v1.noebs]
}

resource "kubernetes_manifest" "noebs_application" {
  count = var.create_noebs_application ? 1 : 0

  manifest = {
    apiVersion = "argoproj.io/v1alpha1"
    kind       = "Application"
    metadata = {
      name      = "noebs"
      namespace = var.argocd_namespace
    }
    spec = {
      project = "noebs"
      source = {
        repoURL        = var.noebs_repo_url
        targetRevision = var.noebs_target_revision
        path           = var.noebs_manifest_path
      }
      destination = {
        server    = "https://kubernetes.default.svc"
        namespace = kubernetes_namespace_v1.noebs.metadata[0].name
      }
      syncPolicy = {
        automated = {
          prune    = true
          selfHeal = true
        }
        syncOptions = [
          "PruneLast=true",
        ]
      }
    }
  }

  depends_on = [
    kubernetes_manifest.noebs_project,
    kubernetes_namespace_v1.noebs,
    data.kubernetes_secret_v1.noebs_required,
  ]

  lifecycle {
    precondition {
      condition = alltrue(flatten([
        for secret_name, required_keys in local.noebs_required_kubernetes_secret_keys : [
          for required_key in required_keys :
          contains(keys(data.kubernetes_secret_v1.noebs_required[secret_name].data), required_key)
        ]
      ]))
      error_message = "required noebs Kubernetes Secrets must contain the exact required data keys."
    }

    precondition {
      condition = alltrue(flatten([
        for secret_name, required_keys in local.noebs_required_kubernetes_secret_keys : [
          for required_key in required_keys :
          length(trimspace(lookup(data.kubernetes_secret_v1.noebs_required[secret_name].data, required_key, ""))) > 0
        ]
      ]))
      error_message = "required noebs Kubernetes Secret data values must be non-empty."
    }

    precondition {
      condition     = data.kubernetes_secret_v1.noebs_required["noebs-tls"].type == "kubernetes.io/tls"
      error_message = "noebs-tls must be a kubernetes.io/tls Secret."
    }

    precondition {
      condition     = data.kubernetes_secret_v1.noebs_required["ghcr-credentials"].type == "kubernetes.io/dockerconfigjson"
      error_message = "ghcr-credentials must be a kubernetes.io/dockerconfigjson Secret."
    }
  }
}
