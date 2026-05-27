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
