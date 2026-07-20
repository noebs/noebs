\getenv identity_auth_migrate_password NOEBS_IDENTITY_AUTH_MIGRATE_PASSWORD
\getenv identity_auth_runtime_password NOEBS_IDENTITY_AUTH_RUNTIME_PASSWORD
\getenv card_vault_migrate_password NOEBS_CARD_VAULT_MIGRATE_PASSWORD
\getenv card_vault_runtime_password NOEBS_CARD_VAULT_RUNTIME_PASSWORD
\getenv ebs_adapter_migrate_password NOEBS_EBS_ADAPTER_MIGRATE_PASSWORD
\getenv ebs_adapter_runtime_password NOEBS_EBS_ADAPTER_RUNTIME_PASSWORD
\getenv ebs_adapter_events_password NOEBS_EBS_ADAPTER_EVENTS_PASSWORD
\getenv admin_reporting_migrate_password NOEBS_ADMIN_REPORTING_MIGRATE_PASSWORD
\getenv admin_reporting_runtime_password NOEBS_ADMIN_REPORTING_RUNTIME_PASSWORD
\getenv admin_reporting_projector_password NOEBS_ADMIN_REPORTING_PROJECTOR_PASSWORD
\getenv notification_chat_migrate_password NOEBS_NOTIFICATION_CHAT_MIGRATE_PASSWORD
\getenv notification_chat_runtime_password NOEBS_NOTIFICATION_CHAT_RUNTIME_PASSWORD
\getenv wallet_ledger_migrate_password NOEBS_WALLET_LEDGER_MIGRATE_PASSWORD
\getenv wallet_ledger_runtime_password NOEBS_WALLET_LEDGER_RUNTIME_PASSWORD
\getenv wallet_ledger_worker_password NOEBS_WALLET_LEDGER_WORKER_PASSWORD
\getenv wallet_ledger_webhook_password NOEBS_WALLET_LEDGER_WEBHOOK_PASSWORD
\getenv workload_auth_migrate_password NOEBS_WORKLOAD_AUTH_MIGRATE_PASSWORD
\getenv workload_auth_runtime_password NOEBS_WORKLOAD_AUTH_RUNTIME_PASSWORD
\getenv workload_auth_cleanup_password NOEBS_WORKLOAD_AUTH_CLEANUP_PASSWORD
\getenv gateway_auth_migrate_password NOEBS_GATEWAY_AUTH_MIGRATE_PASSWORD
\getenv gateway_auth_runtime_password NOEBS_GATEWAY_AUTH_RUNTIME_PASSWORD
\getenv gateway_auth_cleanup_password NOEBS_GATEWAY_AUTH_CLEANUP_PASSWORD

SET password_encryption = 'scram-sha-256';

CREATE TEMP TABLE noebs_role_credentials (
  role_name NAME PRIMARY KEY,
  password TEXT NOT NULL
);

INSERT INTO noebs_role_credentials(role_name, password) VALUES
  ('identity_auth_migrate', :'identity_auth_migrate_password'),
  ('identity_auth_runtime', :'identity_auth_runtime_password'),
  ('card_vault_migrate', :'card_vault_migrate_password'),
  ('card_vault_runtime', :'card_vault_runtime_password'),
  ('ebs_adapter_migrate', :'ebs_adapter_migrate_password'),
  ('ebs_adapter_runtime', :'ebs_adapter_runtime_password'),
  ('ebs_adapter_events', :'ebs_adapter_events_password'),
  ('admin_reporting_migrate', :'admin_reporting_migrate_password'),
  ('admin_reporting_runtime', :'admin_reporting_runtime_password'),
  ('admin_reporting_projector', :'admin_reporting_projector_password'),
  ('notification_chat_migrate', :'notification_chat_migrate_password'),
  ('notification_chat_runtime', :'notification_chat_runtime_password'),
  ('wallet_ledger_migrate', :'wallet_ledger_migrate_password'),
  ('wallet_ledger_runtime', :'wallet_ledger_runtime_password'),
  ('wallet_ledger_worker', :'wallet_ledger_worker_password'),
  ('wallet_ledger_webhook', :'wallet_ledger_webhook_password'),
  ('workload_auth_migrate', :'workload_auth_migrate_password'),
  ('workload_auth_runtime', :'workload_auth_runtime_password'),
  ('workload_auth_cleanup', :'workload_auth_cleanup_password'),
  ('gateway_auth_migrate', :'gateway_auth_migrate_password'),
  ('gateway_auth_runtime', :'gateway_auth_runtime_password'),
  ('gateway_auth_cleanup', :'gateway_auth_cleanup_password');

CREATE TEMP TABLE noebs_database_access (
  database_name NAME NOT NULL,
  role_name NAME NOT NULL REFERENCES noebs_role_credentials(role_name),
  allow_temporary BOOLEAN NOT NULL,
  PRIMARY KEY (database_name, role_name)
);

INSERT INTO noebs_database_access(database_name, role_name, allow_temporary) VALUES
  ('identity_auth', 'identity_auth_migrate', TRUE),
  ('identity_auth', 'identity_auth_runtime', FALSE),
  ('card_vault', 'card_vault_migrate', TRUE),
  ('card_vault', 'card_vault_runtime', FALSE),
  ('ebs_adapter', 'ebs_adapter_migrate', TRUE),
  ('ebs_adapter', 'ebs_adapter_runtime', FALSE),
  ('ebs_adapter', 'ebs_adapter_events', FALSE),
  ('admin_reporting', 'admin_reporting_migrate', TRUE),
  ('admin_reporting', 'admin_reporting_runtime', FALSE),
  ('admin_reporting', 'admin_reporting_projector', FALSE),
  ('notification_chat', 'notification_chat_migrate', TRUE),
  ('notification_chat', 'notification_chat_runtime', FALSE),
  ('wallet_ledger', 'wallet_ledger_migrate', TRUE),
  ('wallet_ledger', 'wallet_ledger_runtime', FALSE),
  ('wallet_ledger', 'wallet_ledger_worker', FALSE),
  ('wallet_ledger', 'wallet_ledger_webhook', FALSE),
  ('workload_auth', 'workload_auth_migrate', TRUE),
  ('workload_auth', 'workload_auth_runtime', FALSE),
  ('workload_auth', 'workload_auth_cleanup', FALSE),
  ('gateway_auth', 'gateway_auth_migrate', TRUE),
  ('gateway_auth', 'gateway_auth_runtime', FALSE),
  ('gateway_auth', 'gateway_auth_cleanup', FALSE);

DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM pg_roles role
    WHERE role.rolname <> 'postgres'
      AND role.rolname !~ '^pg_'
      AND NOT EXISTS (
        SELECT 1 FROM noebs_role_credentials expected
        WHERE expected.role_name = role.rolname
      )
  ) THEN
    RAISE EXCEPTION 'Postgres contains an unexpected role; recreate the retired data volume';
  END IF;
  IF EXISTS (
    SELECT 1
    FROM pg_database database
    WHERE database.datname NOT IN ('postgres', 'template0', 'template1')
      AND NOT EXISTS (
        SELECT 1 FROM noebs_database_access expected
        WHERE expected.database_name = database.datname
      )
  ) THEN
    RAISE EXCEPTION 'Postgres contains an unexpected database; recreate the retired data volume';
  END IF;
END
$$;

DO $$
DECLARE
  credential RECORD;
BEGIN
  FOR credential IN SELECT role_name, password FROM noebs_role_credentials ORDER BY role_name LOOP
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = credential.role_name) THEN
      EXECUTE format('CREATE ROLE %I LOGIN', credential.role_name);
    END IF;
    EXECUTE format(
      'ALTER ROLE %I WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1 VALID UNTIL %L PASSWORD %L',
      credential.role_name,
      'infinity',
      credential.password
    );
    EXECUTE format('ALTER ROLE %I RESET ALL', credential.role_name);
  END LOOP;
END
$$;

DO $$
DECLARE
  membership RECORD;
BEGIN
  FOR membership IN
    SELECT granted.rolname AS granted_role, member.rolname AS member_role
    FROM pg_auth_members edge
    JOIN pg_roles granted ON granted.oid = edge.roleid
    JOIN pg_roles member ON member.oid = edge.member
    WHERE EXISTS (
      SELECT 1 FROM noebs_role_credentials service_role
      WHERE service_role.role_name IN (granted.rolname, member.rolname)
    )
  LOOP
    EXECUTE format('REVOKE %I FROM %I CASCADE', membership.granted_role, membership.member_role);
  END LOOP;
END
$$;

SELECT 'CREATE DATABASE identity_auth OWNER identity_auth_migrate'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'identity_auth')\gexec
SELECT 'CREATE DATABASE card_vault OWNER card_vault_migrate'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'card_vault')\gexec
SELECT 'CREATE DATABASE ebs_adapter OWNER ebs_adapter_migrate'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'ebs_adapter')\gexec
SELECT 'CREATE DATABASE admin_reporting OWNER admin_reporting_migrate'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'admin_reporting')\gexec
SELECT 'CREATE DATABASE notification_chat OWNER notification_chat_migrate'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'notification_chat')\gexec
SELECT 'CREATE DATABASE wallet_ledger OWNER wallet_ledger_migrate'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'wallet_ledger')\gexec
SELECT 'CREATE DATABASE workload_auth OWNER workload_auth_migrate'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'workload_auth')\gexec
SELECT 'CREATE DATABASE gateway_auth OWNER gateway_auth_migrate'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'gateway_auth')\gexec

ALTER DATABASE identity_auth OWNER TO identity_auth_migrate;
ALTER DATABASE card_vault OWNER TO card_vault_migrate;
ALTER DATABASE ebs_adapter OWNER TO ebs_adapter_migrate;
ALTER DATABASE admin_reporting OWNER TO admin_reporting_migrate;
ALTER DATABASE notification_chat OWNER TO notification_chat_migrate;
ALTER DATABASE wallet_ledger OWNER TO wallet_ledger_migrate;
ALTER DATABASE workload_auth OWNER TO workload_auth_migrate;
ALTER DATABASE gateway_auth OWNER TO gateway_auth_migrate;

DO $$
DECLARE
  access RECORD;
  credential RECORD;
  database_record RECORD;
  grantee RECORD;
  system_database NAME;
BEGIN
  FOR database_record IN
    SELECT DISTINCT database_name FROM noebs_database_access ORDER BY database_name
  LOOP
    EXECUTE format('ALTER DATABASE %I RESET ALL', database_record.database_name);
    EXECUTE format('ALTER DATABASE %I ALLOW_CONNECTIONS true', database_record.database_name);
    EXECUTE format('ALTER DATABASE %I CONNECTION LIMIT -1', database_record.database_name);
    EXECUTE format('ALTER DATABASE %I IS_TEMPLATE false', database_record.database_name);
    EXECUTE format('REVOKE ALL PRIVILEGES ON DATABASE %I FROM PUBLIC CASCADE', database_record.database_name);
    FOR grantee IN SELECT rolname FROM pg_roles ORDER BY rolname LOOP
      EXECUTE format(
        'REVOKE ALL PRIVILEGES ON DATABASE %I FROM %I CASCADE',
        database_record.database_name,
        grantee.rolname
      );
    END LOOP;
    FOR credential IN SELECT role_name FROM noebs_role_credentials ORDER BY role_name LOOP
      EXECUTE format(
        'ALTER ROLE %I IN DATABASE %I RESET ALL',
        credential.role_name,
        database_record.database_name
      );
    END LOOP;
  END LOOP;

  FOR access IN SELECT * FROM noebs_database_access ORDER BY database_name, role_name LOOP
    IF access.allow_temporary THEN
      EXECUTE format(
        'GRANT CONNECT, TEMPORARY ON DATABASE %I TO %I',
        access.database_name,
        access.role_name
      );
    ELSE
      EXECUTE format(
        'GRANT CONNECT ON DATABASE %I TO %I',
        access.database_name,
        access.role_name
      );
    END IF;
  END LOOP;

  FOREACH system_database IN ARRAY ARRAY['postgres'::NAME, 'template0'::NAME, 'template1'::NAME] LOOP
    EXECUTE format('REVOKE ALL PRIVILEGES ON DATABASE %I FROM PUBLIC CASCADE', system_database);
    FOR grantee IN SELECT rolname FROM pg_roles ORDER BY rolname LOOP
      EXECUTE format(
        'REVOKE ALL PRIVILEGES ON DATABASE %I FROM %I CASCADE',
        system_database,
        grantee.rolname
      );
    END LOOP;
    FOR credential IN SELECT role_name FROM noebs_role_credentials ORDER BY role_name LOOP
      EXECUTE format(
        'ALTER ROLE %I IN DATABASE %I RESET ALL',
        credential.role_name,
        system_database
      );
    END LOOP;
  END LOOP;
END
$$;

\connect identity_auth
ALTER SCHEMA public OWNER TO identity_auth_migrate;
REVOKE ALL PRIVILEGES ON SCHEMA public FROM PUBLIC CASCADE;
DO $$ DECLARE grantee RECORD; BEGIN FOR grantee IN SELECT rolname FROM pg_roles LOOP EXECUTE format('REVOKE ALL PRIVILEGES ON SCHEMA public FROM %I CASCADE', grantee.rolname); END LOOP; END $$;
GRANT ALL PRIVILEGES ON SCHEMA public TO identity_auth_migrate;
GRANT USAGE ON SCHEMA public TO identity_auth_runtime;

\connect card_vault
ALTER SCHEMA public OWNER TO card_vault_migrate;
REVOKE ALL PRIVILEGES ON SCHEMA public FROM PUBLIC CASCADE;
DO $$ DECLARE grantee RECORD; BEGIN FOR grantee IN SELECT rolname FROM pg_roles LOOP EXECUTE format('REVOKE ALL PRIVILEGES ON SCHEMA public FROM %I CASCADE', grantee.rolname); END LOOP; END $$;
GRANT ALL PRIVILEGES ON SCHEMA public TO card_vault_migrate;
GRANT USAGE ON SCHEMA public TO card_vault_runtime;

\connect ebs_adapter
ALTER SCHEMA public OWNER TO ebs_adapter_migrate;
REVOKE ALL PRIVILEGES ON SCHEMA public FROM PUBLIC CASCADE;
DO $$ DECLARE grantee RECORD; BEGIN FOR grantee IN SELECT rolname FROM pg_roles LOOP EXECUTE format('REVOKE ALL PRIVILEGES ON SCHEMA public FROM %I CASCADE', grantee.rolname); END LOOP; END $$;
GRANT ALL PRIVILEGES ON SCHEMA public TO ebs_adapter_migrate;
GRANT USAGE ON SCHEMA public TO ebs_adapter_runtime, ebs_adapter_events;

\connect admin_reporting
ALTER SCHEMA public OWNER TO admin_reporting_migrate;
REVOKE ALL PRIVILEGES ON SCHEMA public FROM PUBLIC CASCADE;
DO $$ DECLARE grantee RECORD; BEGIN FOR grantee IN SELECT rolname FROM pg_roles LOOP EXECUTE format('REVOKE ALL PRIVILEGES ON SCHEMA public FROM %I CASCADE', grantee.rolname); END LOOP; END $$;
GRANT ALL PRIVILEGES ON SCHEMA public TO admin_reporting_migrate;
GRANT USAGE ON SCHEMA public TO admin_reporting_runtime, admin_reporting_projector;

\connect notification_chat
ALTER SCHEMA public OWNER TO notification_chat_migrate;
REVOKE ALL PRIVILEGES ON SCHEMA public FROM PUBLIC CASCADE;
DO $$ DECLARE grantee RECORD; BEGIN FOR grantee IN SELECT rolname FROM pg_roles LOOP EXECUTE format('REVOKE ALL PRIVILEGES ON SCHEMA public FROM %I CASCADE', grantee.rolname); END LOOP; END $$;
GRANT ALL PRIVILEGES ON SCHEMA public TO notification_chat_migrate;
GRANT USAGE ON SCHEMA public TO notification_chat_runtime;

\connect wallet_ledger
ALTER SCHEMA public OWNER TO wallet_ledger_migrate;
REVOKE ALL PRIVILEGES ON SCHEMA public FROM PUBLIC CASCADE;
DO $$ DECLARE grantee RECORD; BEGIN FOR grantee IN SELECT rolname FROM pg_roles LOOP EXECUTE format('REVOKE ALL PRIVILEGES ON SCHEMA public FROM %I CASCADE', grantee.rolname); END LOOP; END $$;
GRANT ALL PRIVILEGES ON SCHEMA public TO wallet_ledger_migrate;
GRANT USAGE ON SCHEMA public TO wallet_ledger_runtime, wallet_ledger_worker, wallet_ledger_webhook;

\connect workload_auth
ALTER SCHEMA public OWNER TO workload_auth_migrate;
REVOKE ALL PRIVILEGES ON SCHEMA public FROM PUBLIC CASCADE;
DO $$ DECLARE grantee RECORD; BEGIN FOR grantee IN SELECT rolname FROM pg_roles LOOP EXECUTE format('REVOKE ALL PRIVILEGES ON SCHEMA public FROM %I CASCADE', grantee.rolname); END LOOP; END $$;
GRANT ALL PRIVILEGES ON SCHEMA public TO workload_auth_migrate;
GRANT USAGE ON SCHEMA public TO workload_auth_runtime, workload_auth_cleanup;

\connect gateway_auth
ALTER SCHEMA public OWNER TO gateway_auth_migrate;
REVOKE ALL PRIVILEGES ON SCHEMA public FROM PUBLIC CASCADE;
DO $$ DECLARE grantee RECORD; BEGIN FOR grantee IN SELECT rolname FROM pg_roles LOOP EXECUTE format('REVOKE ALL PRIVILEGES ON SCHEMA public FROM %I CASCADE', grantee.rolname); END LOOP; END $$;
GRANT ALL PRIVILEGES ON SCHEMA public TO gateway_auth_migrate;
GRANT USAGE ON SCHEMA public TO gateway_auth_runtime, gateway_auth_cleanup;
