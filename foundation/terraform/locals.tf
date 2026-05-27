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

  noebs_database_catalog = {
    "api-gateway" = {
      database    = "api_gateway"
      secret_name = "api-gateway-secrets"
    }
    "identity-auth" = {
      database       = "identity_auth"
      secret_name    = "identity-auth-secrets"
      migration_role = "identity-auth-migrate"
    }
    "card-vault" = {
      database       = "card_vault"
      secret_name    = "card-vault-secrets"
      migration_role = "card-vault-migrate"
    }
    "ebs-adapter" = {
      database       = "ebs_adapter"
      secret_name    = "ebs-adapter-secrets"
      migration_role = "ebs-adapter-migrate"
    }
    "psp-webhook" = {
      database       = "psp_webhook"
      secret_name    = "psp-webhook-secrets"
      migration_role = "psp-webhook-migrate"
    }
    "admin-reporting" = {
      database       = "admin_reporting"
      secret_name    = "admin-reporting-secrets"
      migration_role = "admin-reporting-migrate"
    }
    "notification-chat" = {
      database       = "notification_chat"
      secret_name    = "notification-chat-secrets"
      migration_role = "notification-chat-migrate"
    }
    "consumer-beneficiary" = {
      database       = "consumer_beneficiary"
      secret_name    = "consumer-beneficiary-secrets"
      migration_role = "consumer-beneficiary-migrate"
    }
    "wallet-ledger" = {
      database       = "wallet_ledger"
      secret_name    = "wallet-ledger-secrets"
      migration_role = "wallet-ledger-migrate"
    }
    "wallet-api" = {
      database    = "wallet_ledger"
      secret_name = "wallet-ledger-secrets"
    }
    "wallet-worker" = {
      database    = "wallet_ledger"
      secret_name = "wallet-ledger-secrets"
    }
  }
}

check "noebs_manifest_path_exists" {
  assert {
    condition     = fileexists("${local.repo_root}/${var.noebs_manifest_path}/kustomization.yaml")
    error_message = "noebs_manifest_path must contain a kustomization.yaml under the repository root."
  }
}
