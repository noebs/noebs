locals {
  repo_root = abspath("${path.module}/../..")

  noebs_service_catalog = {
    "api-gateway" = {
      port     = 8080
      protocol = "http"
    }
    "identity-auth" = {
      port     = 8080
      protocol = "http"
    }
    "card-vault" = {
      port     = 8080
      protocol = "http"
    }
    "ebs-adapter" = {
      port     = 8080
      protocol = "http"
    }
    "psp-webhook" = {
      port     = 8080
      protocol = "http"
    }
    "admin-reporting" = {
      port     = 8080
      protocol = "http"
    }
    "notification-chat" = {
      port     = 8080
      protocol = "http"
    }
    "consumer-beneficiary" = {
      port     = 8080
      protocol = "http"
    }
    "wallet-api" = {
      port     = 8080
      protocol = "http"
    }
    "wallet-ledger" = {
      port     = 9090
      protocol = "grpc"
    }
    "temporal-frontend" = {
      port     = 7233
      protocol = "grpc"
    }
    postgres = {
      port     = 5432
      protocol = "postgres"
    }
  }
}

check "noebs_manifest_path_exists" {
  assert {
    condition     = fileexists("${local.repo_root}/${var.noebs_manifest_path}/kustomization.yaml")
    error_message = "noebs_manifest_path must contain a kustomization.yaml under the repository root."
  }
}
