-- +goose Up
CREATE TABLE interop_bindings (
  tenant_id TEXT PRIMARY KEY REFERENCES tenants(id),
  fsp_id TEXT NOT NULL UNIQUE CHECK (fsp_id ~ '^[a-z][a-z0-9]{2,31}$'),
  currency TEXT NOT NULL CHECK (currency = 'SDG'),
  currency_unit_version_id BIGINT NOT NULL,
  clearing_wallet_id UUID NOT NULL,
  suspense_wallet_id UUID NOT NULL,
  enabled BOOLEAN NOT NULL,
  FOREIGN KEY (currency_unit_version_id, currency) REFERENCES currency_unit_versions(id, currency_code),
  FOREIGN KEY (tenant_id, clearing_wallet_id, currency, currency_unit_version_id) REFERENCES wallets(tenant_id, id, currency, currency_unit_version_id),
  FOREIGN KEY (tenant_id, suspense_wallet_id, currency, currency_unit_version_id) REFERENCES wallets(tenant_id, id, currency, currency_unit_version_id),
  CHECK (clearing_wallet_id <> suspense_wallet_id)
);

CREATE TABLE interop_aliases (
  tenant_id TEXT NOT NULL REFERENCES interop_bindings(tenant_id),
  identifier TEXT NOT NULL UNIQUE CHECK (identifier ~ '^249[0-9]{9}$'),
  wallet_id UUID NOT NULL,
  display_name TEXT NOT NULL CHECK (length(display_name) BETWEEN 1 AND 128),
  PRIMARY KEY (tenant_id, wallet_id),
  FOREIGN KEY (tenant_id, wallet_id) REFERENCES wallets(tenant_id, id)
);

-- Only the admission switch may change after binding a participant. Existing
-- obligations must continue to use the same system wallets and currency unit.
-- +goose StatementBegin
CREATE FUNCTION guard_interop_binding_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.tenant_id,NEW.fsp_id,NEW.currency,NEW.currency_unit_version_id,NEW.clearing_wallet_id,NEW.suspense_wallet_id)
 IS DISTINCT FROM ROW(OLD.tenant_id,OLD.fsp_id,OLD.currency,OLD.currency_unit_version_id,OLD.clearing_wallet_id,OLD.suspense_wallet_id) THEN
  RAISE EXCEPTION 'interop monetary binding is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER interop_binding_identity BEFORE UPDATE ON interop_bindings FOR EACH ROW EXECUTE FUNCTION guard_interop_binding_identity();

-- Alias reassignment would change the identity of an accepted quote. Retiring
-- an alias requires an explicit versioned protocol, outside this profile.
-- +goose StatementBegin
CREATE FUNCTION guard_interop_alias_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.tenant_id,NEW.identifier,NEW.wallet_id)
 IS DISTINCT FROM ROW(OLD.tenant_id,OLD.identifier,OLD.wallet_id) THEN
  RAISE EXCEPTION 'interop alias identity is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER interop_alias_identity BEFORE UPDATE ON interop_aliases FOR EACH ROW EXECUTE FUNCTION guard_interop_alias_identity();

CREATE TABLE interop_quotes (
  id UUID PRIMARY KEY,
  transfer_id UUID NOT NULL UNIQUE,
  tenant_id TEXT NOT NULL REFERENCES interop_bindings(tenant_id),
  owner_id TEXT NOT NULL CHECK (owner_id <> ''),
  wallet_id UUID NOT NULL,
  direction TEXT NOT NULL DEFAULT 'OUT' CHECK (direction IN ('OUT','IN')),
  idempotency_key TEXT NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 256),
  amount BIGINT NOT NULL CHECK (amount > 0),
  currency TEXT NOT NULL,
  currency_unit_version_id BIGINT NOT NULL,
  request JSON NOT NULL,
  response JSON,
  sdk_state JSON,
  status TEXT NOT NULL DEFAULT 'REQUESTED' CHECK (status IN ('REQUESTED','QUOTING','READY','FAILED')),
  expires_at TIMESTAMPTZ,
  error_code TEXT,
  lease_token UUID,
  lease_until TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, id, transfer_id),
  UNIQUE (tenant_id, owner_id, direction, idempotency_key),
  FOREIGN KEY (tenant_id, wallet_id, currency, currency_unit_version_id) REFERENCES wallets(tenant_id, id, currency, currency_unit_version_id),
  FOREIGN KEY (currency_unit_version_id, currency) REFERENCES currency_unit_versions(id, currency_code),
  CHECK (status <> 'READY' OR (response IS NOT NULL AND expires_at IS NOT NULL))
);

ALTER TABLE balance_holds ADD CONSTRAINT balance_holds_tenant_identity UNIQUE (tenant_id,id);

CREATE TABLE interop_transfers (
  id UUID PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES interop_bindings(tenant_id),
  quote_id UUID NOT NULL UNIQUE,
  owner_id TEXT NOT NULL CHECK (owner_id <> ''),
  idempotency_key TEXT NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 256),
  status TEXT NOT NULL DEFAULT 'REQUESTED' CHECK (status IN ('REQUESTED','ARMED','PENDING','IN_DOUBT','SUCCEEDED','FAILED','SUSPENSE')),
  hub_state TEXT NOT NULL DEFAULT 'UNKNOWN' CHECK (hub_state IN ('UNKNOWN','COMMITTED','ABORTED')),
  hold_id BIGINT,
  ledger_transaction_id BIGINT,
  original_response JSON,
  original_prepare JSON,
  submitted_at TIMESTAMPTZ,
  lease_token UUID,
  lease_until TIMESTAMPTZ,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  error_code TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, owner_id, idempotency_key),
  FOREIGN KEY (tenant_id, quote_id, id) REFERENCES interop_quotes(tenant_id, id, transfer_id),
  FOREIGN KEY (tenant_id, hold_id) REFERENCES balance_holds(tenant_id, id),
  FOREIGN KEY (tenant_id, ledger_transaction_id) REFERENCES ledger_transactions(tenant_id, id),
  CHECK (status NOT IN ('SUCCEEDED','SUSPENSE') OR (hub_state = 'COMMITTED' AND ledger_transaction_id IS NOT NULL)),
  CHECK (submitted_at IS NULL OR hold_id IS NOT NULL OR original_response IS NOT NULL)
);

CREATE TABLE interop_inbox (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES interop_bindings(tenant_id),
  transfer_id UUID NOT NULL,
  event_kind TEXT NOT NULL,
  payload JSON NOT NULL,
  authority TEXT NOT NULL CHECK (authority IN ('sdk-loopback','sdk-hub-query')),
  payload_sha256 TEXT NOT NULL CHECK (length(payload_sha256) = 64),
  received_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  applied_at TIMESTAMPTZ,
  quarantined BOOLEAN NOT NULL DEFAULT false,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  UNIQUE (tenant_id, transfer_id, event_kind, payload_sha256)
);

CREATE INDEX interop_quotes_pending ON interop_quotes(created_at) WHERE status IN ('REQUESTED','QUOTING');
CREATE INDEX interop_transfers_pending ON interop_transfers(next_attempt_at) WHERE status IN ('REQUESTED','ARMED','PENDING','IN_DOUBT');
CREATE INDEX interop_inbox_pending ON interop_inbox(id) WHERE applied_at IS NULL;

-- +goose Down
DROP TABLE interop_inbox;
DROP TABLE interop_transfers;
ALTER TABLE balance_holds DROP CONSTRAINT balance_holds_tenant_identity;
DROP TABLE interop_quotes;
DROP TABLE interop_aliases;
DROP TABLE interop_bindings;
DROP FUNCTION guard_interop_binding_identity();
DROP FUNCTION guard_interop_alias_identity();
