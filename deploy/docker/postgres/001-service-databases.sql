\getenv workload_auth_migrate_password NOEBS_WORKLOAD_AUTH_MIGRATE_PASSWORD
\getenv workload_auth_runtime_password NOEBS_WORKLOAD_AUTH_RUNTIME_PASSWORD
\getenv workload_auth_cleanup_password NOEBS_WORKLOAD_AUTH_CLEANUP_PASSWORD

SELECT format('CREATE ROLE workload_auth_migrate LOGIN PASSWORD %L', :'workload_auth_migrate_password')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'workload_auth_migrate')\gexec
SELECT format('ALTER ROLE workload_auth_migrate WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD %L', :'workload_auth_migrate_password')\gexec
SELECT format('CREATE ROLE workload_auth_runtime LOGIN PASSWORD %L', :'workload_auth_runtime_password')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'workload_auth_runtime')\gexec
SELECT format('ALTER ROLE workload_auth_runtime WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD %L', :'workload_auth_runtime_password')\gexec
SELECT format('CREATE ROLE workload_auth_cleanup LOGIN PASSWORD %L', :'workload_auth_cleanup_password')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'workload_auth_cleanup')\gexec
SELECT format('ALTER ROLE workload_auth_cleanup WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD %L', :'workload_auth_cleanup_password')\gexec

SELECT 'CREATE DATABASE identity_auth OWNER noebs'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'identity_auth')\gexec
SELECT 'CREATE DATABASE card_vault OWNER noebs'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'card_vault')\gexec
SELECT 'CREATE DATABASE ebs_adapter OWNER noebs'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'ebs_adapter')\gexec
SELECT 'CREATE DATABASE psp_webhook OWNER noebs'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'psp_webhook')\gexec
SELECT 'CREATE DATABASE admin_reporting OWNER noebs'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'admin_reporting')\gexec
SELECT 'CREATE DATABASE notification_chat OWNER noebs'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'notification_chat')\gexec
SELECT 'CREATE DATABASE consumer_beneficiary OWNER noebs'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'consumer_beneficiary')\gexec
SELECT 'CREATE DATABASE wallet_ledger OWNER noebs'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'wallet_ledger')\gexec
SELECT 'CREATE DATABASE workload_auth OWNER workload_auth_migrate'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'workload_auth')\gexec
GRANT CONNECT ON DATABASE workload_auth TO workload_auth_runtime, workload_auth_cleanup;
