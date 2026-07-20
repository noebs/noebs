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
        {
          namespace = var.edge_namespace
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
      syncPolicy = merge(
        {
          syncOptions = [
            "PruneLast=true",
          ]
        },
        var.noebs_automated_sync ? {
          automated = {
            prune    = true
            selfHeal = true
          }
        } : {},
      )
    }
  }

  depends_on = [
    kubernetes_manifest.noebs_project,
    kubernetes_namespace_v1.noebs,
  ]
}

resource "kubernetes_manifest" "noebs_edge_application" {
  count = var.create_edge_application ? 1 : 0

  manifest = {
    apiVersion = "argoproj.io/v1alpha1"
    kind       = "Application"
    metadata = {
      name      = "noebs-edge"
      namespace = var.argocd_namespace
    }
    spec = {
      project = "noebs"
      source = {
        repoURL        = var.noebs_repo_url
        targetRevision = var.noebs_target_revision
        path           = var.edge_manifest_path
      }
      destination = {
        server    = "https://kubernetes.default.svc"
        namespace = var.edge_namespace
      }
      syncPolicy = {
        automated = {
          prune    = true
          selfHeal = true
        }
        syncOptions = [
          "CreateNamespace=true",
          "PruneLast=true",
        ]
      }
    }
  }

  depends_on = [
    kubernetes_manifest.noebs_project,
  ]
}
