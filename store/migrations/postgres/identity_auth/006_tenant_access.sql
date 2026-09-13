-- +goose Up
-- This journal is an immutable command/audit log, not a second role authority.
-- Keycloak role membership remains authoritative. A pending row prevents a new
-- command until the same immutable request has been recovered and verified.
CREATE TABLE tenant_access_operations (
 issuer TEXT NOT NULL CHECK(issuer<>''),
 subject UUID NOT NULL,
 tenant_id TEXT NOT NULL REFERENCES tenants(id),
 operation_id UUID NOT NULL,
 payload_hash TEXT NOT NULL CHECK(payload_hash ~ '^[0-9a-f]{64}$'),
 audit JSONB NOT NULL CHECK(jsonb_typeof(audit)='object'),
 status TEXT NOT NULL CHECK(status IN ('pending','complete')),
 created_at TIMESTAMPTZ NOT NULL,
 completed_at TIMESTAMPTZ,
 completed_roles JSONB CHECK(completed_roles IS NULL OR jsonb_typeof(completed_roles)='array'),
 PRIMARY KEY(issuer,tenant_id,operation_id),
 CHECK((status='pending' AND completed_at IS NULL AND completed_roles IS NULL) OR (status='complete' AND completed_at IS NOT NULL AND completed_roles IS NOT NULL))
);
CREATE UNIQUE INDEX tenant_access_one_pending ON tenant_access_operations(issuer,subject,tenant_id) WHERE status='pending';
CREATE TABLE tenant_access_recovery_attempts (
 issuer TEXT NOT NULL,
 tenant_id TEXT NOT NULL,
 operation_id UUID NOT NULL,
 request_id TEXT NOT NULL CHECK(request_id<>''),
 attempt JSONB NOT NULL CHECK(jsonb_typeof(attempt)='object'),
 created_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(issuer,tenant_id,operation_id,request_id),
 FOREIGN KEY(issuer,tenant_id,operation_id) REFERENCES tenant_access_operations(issuer,tenant_id,operation_id)
);
-- +goose StatementBegin
CREATE FUNCTION enforce_tenant_access_completion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.status <> 'pending' OR NEW.status <> 'complete' OR NEW.completed_at IS NULL THEN
  RAISE EXCEPTION 'Tenant access journal only permits pending-to-complete transitions';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER tenant_access_completion BEFORE UPDATE ON tenant_access_operations FOR EACH ROW EXECUTE FUNCTION enforce_tenant_access_completion();

CREATE TABLE tenant_access_bootstrap (
 issuer TEXT NOT NULL CHECK(issuer<>''),
 tenant_id TEXT NOT NULL REFERENCES tenants(id),
 receipt JSONB NOT NULL CHECK(jsonb_typeof(receipt)='object'),
 created_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(issuer,tenant_id)
);

-- +goose Down
-- Audit and suppression records must survive application rollback.
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'Tenant access audit must be retained; use a forward migration';
END $$;
-- +goose StatementEnd
