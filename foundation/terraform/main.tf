provider "kubernetes" {
  config_path = var.kubeconfig_path
}

provider "helm" {
  kubernetes {
    config_path = var.kubeconfig_path
  }
}

resource "kubernetes_namespace_v1" "argocd" {
  metadata {
    name = var.argocd_namespace
  }
}

resource "helm_release" "argocd" {
  name       = "argocd"
  repository = "https://argoproj.github.io/argo-helm"
  chart      = "argo-cd"
  version    = var.argocd_chart_version
  namespace  = kubernetes_namespace_v1.argocd.metadata[0].name

  set {
    name  = "server.service.type"
    value = "ClusterIP"
  }
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
          namespace = "noebs"
          server    = "https://kubernetes.default.svc"
        },
      ]
      clusterResourceWhitelist = [
        {
          group = ""
          kind  = "Namespace"
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

  depends_on = [helm_release.argocd]
}

resource "kubernetes_manifest" "noebs_application" {
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
        namespace = "noebs"
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

  depends_on = [kubernetes_manifest.noebs_project]
}
