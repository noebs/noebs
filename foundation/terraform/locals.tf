locals {
  repo_root = abspath("${path.module}/../..")

  noebs_service_catalog = {
    "api-gateway" = {
      port     = 8080
      protocol = "http"
    }
    "identity-auth" = {
      port     = 8080
      protocol = "https"
    }
    keycloak = {
      port     = 8443
      protocol = "https"
    }
    "card-vault" = {
      port     = 8080
      protocol = "https"
    }
    "ebs-adapter" = {
      port     = 8080
      protocol = "https"
    }
    "psp-webhook" = {
      port     = 8080
      protocol = "https"
    }
    "admin-reporting" = {
      port     = 8080
      protocol = "https"
    }
    "notification-chat" = {
      port     = 8080
      protocol = "https"
    }
    "wallet-api" = {
      port     = 8080
      protocol = "https"
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
    kafka = {
      port     = 9092
      protocol = "kafka"
    }
    "keycloak-postgres" = {
      port     = 5432
      protocol = "postgres"
    }
  }

  noebs_database_catalog = {
    "api-gateway" = {
      database       = "gateway_auth"
      secret_name    = "api-gateway-secrets"
      migration_role = "gateway-auth-migrate"
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
    "wallet-ledger" = {
      database       = "wallet_ledger"
      secret_name    = "wallet-ledger-secrets"
      migration_role = "wallet-ledger-migrate"
    }
    "wallet-worker" = {
      database    = "wallet_ledger"
      secret_name = "wallet-worker-secrets"
    }
    "workload-auth" = {
      database       = "workload_auth"
      secret_name    = "workload-auth-migrate-secrets"
      migration_role = "workload-auth-migrate"
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
    "noebs-release-manifest",
    "api-gateway-secrets",
    "identity-auth-secrets",
    "card-vault-secrets",
    "ebs-adapter-secrets",
    "ebs-adapter-events-secrets",
    "psp-webhook-secrets",
    "admin-reporting-secrets",
    "admin-reporting-projector-secrets",
    "notification-chat-secrets",
    "wallet-api-secrets",
    "wallet-ledger-secrets",
    "wallet-worker-secrets",
    "workload-auth-migrate-secrets",
    "workload-auth-cleanup-secrets",
    "gateway-auth-migrate-secrets",
    "gateway-auth-cleanup-secrets",
    "identity-auth-migrate-secrets",
    "card-vault-migrate-secrets",
    "ebs-adapter-migrate-secrets",
    "psp-webhook-migrate-secrets",
    "admin-reporting-migrate-secrets",
    "notification-chat-migrate-secrets",
    "wallet-ledger-migrate-secrets",
    "sops-age-key",
    "postgres-credentials",
    "workload-auth-postgres-roles",
    "gateway-auth-postgres-roles",
    "internal-transport-platform",
    "temporal-postgres-credentials",
    "keycloak-postgres-credentials",
    "keycloak-secrets",
    "keycloak-transport-ca",
    "keycloak-reconciler-credentials",
    "ghcr-credentials",
  ]

  noebs_required_kubernetes_secret_keys = {
    "noebs-release-manifest" = [
      "release-manifest.yaml",
    ]
    "api-gateway-secrets" = [
      "secrets.yaml",
    ]
    "identity-auth-secrets" = [
      "secrets.yaml",
    ]
    "card-vault-secrets" = [
      "secrets.yaml",
    ]
    "ebs-adapter-secrets" = [
      "secrets.yaml",
    ]
    "ebs-adapter-events-secrets" = [
      "secrets.yaml",
    ]
    "psp-webhook-secrets" = [
      "secrets.yaml",
    ]
    "admin-reporting-secrets" = [
      "secrets.yaml",
    ]
    "admin-reporting-projector-secrets" = [
      "secrets.yaml",
    ]
    "notification-chat-secrets" = [
      "secrets.yaml",
    ]
    "wallet-api-secrets" = [
      "secrets.yaml",
    ]
    "wallet-ledger-secrets" = [
      "secrets.yaml",
    ]
    "wallet-worker-secrets" = [
      "secrets.yaml",
    ]
    "workload-auth-migrate-secrets" = [
      "secrets.yaml",
    ]
    "workload-auth-cleanup-secrets" = [
      "secrets.yaml",
    ]
    "gateway-auth-migrate-secrets" = [
      "secrets.yaml",
    ]
    "gateway-auth-cleanup-secrets" = [
      "secrets.yaml",
    ]
    "identity-auth-migrate-secrets" = [
      "secrets.yaml",
    ]
    "card-vault-migrate-secrets" = [
      "secrets.yaml",
    ]
    "ebs-adapter-migrate-secrets" = [
      "secrets.yaml",
    ]
    "psp-webhook-migrate-secrets" = [
      "secrets.yaml",
    ]
    "admin-reporting-migrate-secrets" = [
      "secrets.yaml",
    ]
    "notification-chat-migrate-secrets" = [
      "secrets.yaml",
    ]
    "wallet-ledger-migrate-secrets" = [
      "secrets.yaml",
    ]
    "sops-age-key" = [
      "age-key.txt",
    ]
    "postgres-credentials" = [
      "password",
      "tls.crt",
      "tls.key",
    ]
    "workload-auth-postgres-roles" = [
      "migrate-password",
      "runtime-password",
      "cleanup-password",
      "roles.yaml",
    ]
    "gateway-auth-postgres-roles" = [
      "migrate-password",
      "runtime-password",
      "cleanup-password",
      "roles.yaml",
    ]
    "internal-transport-platform" = [
      "credentials.yaml",
    ]
    "temporal-postgres-credentials" = [
      "password",
    ]
    "keycloak-postgres-credentials" = [
      "password",
      "tls.crt",
      "tls.key",
    ]
    "keycloak-secrets" = [
      "keycloak.conf",
      "db-ca.pem",
      "tls.crt",
      "tls.key",
    ]
    "keycloak-transport-ca" = [
      "ca.pem",
    ]
    "keycloak-reconciler-credentials" = [
      "config.yaml",
    ]
    "ghcr-credentials" = [
      ".dockerconfigjson",
    ]
  }
}

check "noebs_manifest_path_exists" {
  assert {
    condition     = fileexists("${local.repo_root}/${var.noebs_manifest_path}/kustomization.yaml")
    error_message = "noebs_manifest_path must contain a kustomization.yaml under the repository root."
  }
}

check "edge_manifest_path_exists" {
  assert {
    condition     = fileexists("${local.repo_root}/${var.edge_manifest_path}/kustomization.yaml")
    error_message = "edge_manifest_path must contain a kustomization.yaml under the repository root."
  }
}
