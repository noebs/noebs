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
    keycloak = {
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
    "temporal-postgres" = {
      port     = 5432
      protocol = "postgres"
    }
    "temporal-ui" = {
      port     = 8080
      protocol = "http"
    }
    postgres = {
      port     = 5432
      protocol = "postgres"
    }
    "keycloak-postgres" = {
      port     = 5432
      protocol = "postgres"
    }
  }

  noebs_database_catalog = {
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
    "wallet-worker" = {
      database    = "wallet_ledger"
      secret_name = "wallet-worker-secrets"
    }
    keycloak = {
      database    = "keycloak"
      secret_name = "keycloak-secrets"
      managed_by  = "keycloak"
    }
    temporal = {
      database       = "temporal"
      secret_name    = "temporal-postgres-credentials"
      migration_role = "temporal-schema-migrate"
      managed_by     = "temporal"
    }
    "temporal-visibility" = {
      database       = "temporal_visibility"
      secret_name    = "temporal-postgres-credentials"
      migration_role = "temporal-schema-migrate"
      managed_by     = "temporal"
    }
  }

  noebs_required_kubernetes_secrets = [
    "api-gateway-secrets",
    "identity-auth-secrets",
    "card-vault-secrets",
    "ebs-adapter-secrets",
    "psp-webhook-secrets",
    "admin-reporting-secrets",
    "notification-chat-secrets",
    "consumer-beneficiary-secrets",
    "wallet-api-secrets",
    "wallet-ledger-secrets",
    "wallet-worker-secrets",
    "sops-age-key",
    "postgres-credentials",
    "temporal-postgres-credentials",
    "keycloak-postgres-credentials",
    "keycloak-secrets",
    "noebs-tls",
  ]
}

check "noebs_manifest_path_exists" {
  assert {
    condition     = fileexists("${local.repo_root}/${var.noebs_manifest_path}/kustomization.yaml")
    error_message = "noebs_manifest_path must contain a kustomization.yaml under the repository root."
  }
}
