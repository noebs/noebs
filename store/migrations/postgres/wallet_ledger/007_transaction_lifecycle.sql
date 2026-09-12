-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION wallet_psp_lifecycle(provider_status TEXT, settled_at TIMESTAMPTZ, fulfillment_method TEXT)
RETURNS TEXT[] LANGUAGE SQL IMMUTABLE AS $$
 SELECT CASE
   WHEN provider_status = 'success' AND settled_at IS NOT NULL THEN ARRAY['completed', fulfillment_method]
   WHEN provider_status = 'success' THEN ARRAY['processing', 'settlement_pending']
   WHEN provider_status = 'initiated' THEN ARRAY['pending', 'created']
   WHEN provider_status = 'held' THEN ARRAY['pending', 'approval_required']
   WHEN provider_status = 'pending' THEN ARRAY['processing', 'provider_pending']
   WHEN provider_status = 'processing' THEN ARRAY['processing', 'provider_processing']
   WHEN provider_status = 'failed' THEN ARRAY['failed', CASE WHEN fulfillment_method = 'automated' THEN 'provider_failed' ELSE fulfillment_method END]
   WHEN provider_status = 'cancelled' THEN ARRAY['cancelled', 'cancelled']
 END
$$;
-- +goose StatementEnd

ALTER TABLE psp_transactions
 ADD COLUMN fulfillment_method TEXT NOT NULL DEFAULT 'automated' CHECK (fulfillment_method IN ('automated','manual','offline')),
 ADD COLUMN settled_at TIMESTAMPTZ,
 ADD COLUMN status_version BIGINT NOT NULL DEFAULT 1 CHECK (status_version > 0),
 ADD COLUMN lifecycle_status TEXT GENERATED ALWAYS AS ((wallet_psp_lifecycle(status, settled_at, fulfillment_method))[1]) STORED,
 ADD COLUMN substatus TEXT GENERATED ALWAYS AS (CASE
   WHEN status = 'initiated' AND approval_timeout_seconds IS NOT NULL THEN 'approval_required'
   WHEN status = 'cancelled' AND last_error_type IN ('approval_rejected','approval_timeout') THEN last_error_type
   ELSE (wallet_psp_lifecycle(status, settled_at, fulfillment_method))[2] END) STORED,
 ADD CONSTRAINT psp_settlement_requires_success CHECK (settled_at IS NULL OR status = 'success');

UPDATE psp_transactions p SET settled_at = l.created_at
FROM ledger_transactions l
WHERE p.tenant_id = l.tenant_id AND p.client_reference = l.reference_id AND p.status = 'success'
 AND l.status = 'completed'
 AND l.reference_type = CASE p.direction WHEN 'inbound' THEN 'deposit' ELSE 'withdrawal' END;

CREATE TABLE psp_manual_resolutions (
 tenant_id TEXT NOT NULL REFERENCES tenants(id),
 client_reference TEXT NOT NULL,
 idempotency_key TEXT NOT NULL CHECK (idempotency_key <> '' AND idempotency_key = btrim(idempotency_key)),
 operator_id BIGINT NOT NULL REFERENCES operator_identities(id),
 expected_version BIGINT NOT NULL CHECK (expected_version > 0),
 status TEXT NOT NULL CHECK (status IN ('success','failed')),
 fulfillment_method TEXT NOT NULL CHECK (fulfillment_method IN ('manual','offline')),
 reason TEXT NOT NULL CHECK (reason <> '' AND length(reason) <= 4096),
 evidence_reference TEXT NOT NULL CHECK (evidence_reference <> '' AND length(evidence_reference) <= 4096),
 settlement_reference TEXT NOT NULL CHECK (status <> 'success' OR settlement_reference <> ''),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (tenant_id, client_reference),
 UNIQUE (tenant_id, idempotency_key),
 FOREIGN KEY (tenant_id, client_reference) REFERENCES psp_transactions(tenant_id, client_reference)
);

CREATE TABLE transaction_status_events (
 id BIGSERIAL PRIMARY KEY,
 event_id UUID NOT NULL DEFAULT gen_random_uuid() UNIQUE,
 tenant_id TEXT NOT NULL REFERENCES tenants(id),
 aggregate_id TEXT NOT NULL,
 version BIGINT NOT NULL CHECK (version > 0),
 status TEXT NOT NULL CHECK (status IN ('pending','processing','completed','failed','cancelled')),
 substatus TEXT NOT NULL CHECK (substatus <> ''),
 user_id BIGINT,
 operator_id BIGINT REFERENCES operator_identities(id),
 reason TEXT,
 evidence_reference TEXT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 publish_attempts INTEGER NOT NULL DEFAULT 0,
 published_at TIMESTAMPTZ,
 claim_token UUID,
 claimed_until TIMESTAMPTZ,
 last_error TEXT,
 UNIQUE (tenant_id, aggregate_id, version)
);
CREATE INDEX transaction_status_events_pending ON transaction_status_events(id) WHERE published_at IS NULL;

-- +goose StatementBegin
CREATE FUNCTION guard_psp_lifecycle() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.status IN ('success','failed','cancelled') AND NEW.status <> OLD.status THEN
   RAISE EXCEPTION 'invalid terminal transaction transition' USING ERRCODE = '23514';
 END IF;
 IF NEW.status = 'initiated' AND OLD.status <> 'initiated' THEN
   RAISE EXCEPTION 'transaction cannot return to initiated' USING ERRCODE = '23514';
 END IF;
 IF OLD.settled_at IS NOT NULL AND NEW.settled_at IS DISTINCT FROM OLD.settled_at THEN
   RAISE EXCEPTION 'settlement timestamp is immutable' USING ERRCODE = '23514';
 END IF;
 IF OLD.fulfillment_method <> 'automated' AND NEW.fulfillment_method <> OLD.fulfillment_method THEN
   RAISE EXCEPTION 'manual resolution is immutable' USING ERRCODE = '23514';
 END IF;
 IF NEW.fulfillment_method <> OLD.fulfillment_method AND NOT EXISTS (
   SELECT 1 FROM psp_manual_resolutions r
   WHERE r.tenant_id = NEW.tenant_id AND r.client_reference = NEW.client_reference
    AND r.status = NEW.status AND r.fulfillment_method = NEW.fulfillment_method
 ) THEN
   RAISE EXCEPTION 'manual resolution evidence required' USING ERRCODE = '23514';
 END IF;
 NEW.status_version := OLD.status_version + CASE WHEN
   (NEW.status, NEW.fulfillment_method, NEW.settled_at) IS DISTINCT FROM
   (OLD.status, OLD.fulfillment_method, OLD.settled_at) THEN 1 ELSE 0 END;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER guard_psp_lifecycle BEFORE UPDATE ON psp_transactions FOR EACH ROW EXECUTE FUNCTION guard_psp_lifecycle();

-- +goose StatementBegin
CREATE FUNCTION record_psp_lifecycle() RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE recipient BIGINT;
BEGIN
 IF TG_OP = 'UPDATE' AND NEW.status_version = OLD.status_version THEN RETURN NEW; END IF;
 SELECT w.user_id INTO recipient FROM wallets w
 WHERE w.tenant_id = NEW.tenant_id AND w.owner_type = 'user' AND w.id = CASE NEW.direction
   WHEN 'outbound' THEN NEW.wallet_id
   ELSE (SELECT d.wallet_id FROM deposit_intents d WHERE d.tenant_id = NEW.tenant_id AND d.id = NEW.deposit_intent_id) END;
 INSERT INTO transaction_status_events(tenant_id,aggregate_id,version,status,substatus,user_id,operator_id,reason,evidence_reference)
 SELECT NEW.tenant_id,'psp:' || NEW.client_reference,NEW.status_version,NEW.lifecycle_status,NEW.substatus,recipient,
   COALESCE(r.operator_id,d.decided_by_operator_id),COALESCE(r.reason,d.reason,NEW.response_message),COALESCE(r.evidence_reference,d.proof_of_payment)
 FROM (SELECT 1) seed LEFT JOIN psp_manual_resolutions r
   ON r.tenant_id = NEW.tenant_id AND r.client_reference = NEW.client_reference
 LEFT JOIN workflow_decisions d ON d.tenant_id = NEW.tenant_id AND d.workflow_id = NEW.workflow_id AND d.decision_kind = 'withdrawal';
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER record_psp_lifecycle AFTER INSERT OR UPDATE ON psp_transactions FOR EACH ROW EXECUTE FUNCTION record_psp_lifecycle();

ALTER TABLE manual_transfers
 ADD COLUMN status_version BIGINT NOT NULL DEFAULT 1 CHECK (status_version > 0),
 ADD COLUMN lifecycle_status TEXT GENERATED ALWAYS AS (CASE status WHEN 'pending' THEN 'pending' WHEN 'approved' THEN 'processing' WHEN 'rejected' THEN 'failed' WHEN 'completed' THEN 'completed' END) STORED,
 ADD COLUMN substatus TEXT GENERATED ALWAYS AS (CASE status WHEN 'pending' THEN 'approval_required' WHEN 'approved' THEN 'settlement_pending' WHEN 'rejected' THEN 'rejected' WHEN 'completed' THEN 'manual' END) STORED;

-- +goose StatementBegin
CREATE FUNCTION guard_manual_lifecycle() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status <> OLD.status AND NOT (
   (OLD.status = 'pending' AND NEW.status IN ('approved','rejected')) OR
   (OLD.status = 'approved' AND NEW.status = 'completed')
 ) THEN RAISE EXCEPTION 'invalid manual transaction transition' USING ERRCODE = '23514'; END IF;
 NEW.status_version := OLD.status_version + CASE WHEN NEW.status <> OLD.status THEN 1 ELSE 0 END;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER guard_manual_lifecycle BEFORE UPDATE ON manual_transfers FOR EACH ROW EXECUTE FUNCTION guard_manual_lifecycle();

-- +goose StatementBegin
CREATE FUNCTION record_manual_lifecycle() RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
 IF TG_OP = 'UPDATE' AND NEW.status_version = OLD.status_version THEN RETURN NEW; END IF;
 INSERT INTO transaction_status_events(tenant_id,aggregate_id,version,status,substatus,user_id,operator_id,reason,evidence_reference)
 VALUES(NEW.tenant_id,'manual:' || NEW.workflow_id,NEW.status_version,NEW.lifecycle_status,NEW.substatus,
   (SELECT user_id FROM wallets WHERE tenant_id = NEW.tenant_id AND id = NEW.wallet_id AND owner_type = 'user'),
   COALESCE(NEW.approved_by_operator_id,NEW.requested_by_operator_id),COALESCE(NEW.rejection_reason,NEW.reason),NEW.proof_of_payment);
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER record_manual_lifecycle AFTER INSERT OR UPDATE ON manual_transfers FOR EACH ROW EXECUTE FUNCTION record_manual_lifecycle();

-- +goose StatementBegin
CREATE FUNCTION settle_psp_lifecycle() RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
 IF NEW.reference_type IN ('deposit','withdrawal') AND NEW.status = 'completed' THEN
   UPDATE psp_transactions SET settled_at = NEW.created_at
   WHERE tenant_id = NEW.tenant_id AND client_reference = NEW.reference_id AND status = 'success' AND settled_at IS NULL
    AND direction = CASE NEW.reference_type WHEN 'deposit' THEN 'inbound' ELSE 'outbound' END;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER settle_psp_lifecycle AFTER INSERT OR UPDATE OF status ON ledger_transactions FOR EACH ROW EXECUTE FUNCTION settle_psp_lifecycle();

-- +goose StatementBegin
CREATE FUNCTION apply_psp_manual_resolution() RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE target psp_transactions%ROWTYPE; provider_reference TEXT; response JSONB;
BEGIN
 SELECT * INTO STRICT target FROM psp_transactions WHERE tenant_id = NEW.tenant_id AND client_reference = NEW.client_reference FOR UPDATE;
 IF target.status_version <> NEW.expected_version THEN
   RAISE EXCEPTION 'transaction status version conflict' USING ERRCODE = '23514', CONSTRAINT = 'psp_manual_resolution_version';
 END IF;
 IF target.status NOT IN ('pending','processing') OR target.workflow_id IS NULL OR target.workflow_id = '' THEN
   RAISE EXCEPTION 'manual resolution not allowed' USING ERRCODE = '23514', CONSTRAINT = 'psp_manual_resolution_target';
 END IF;
 provider_reference := target.psp_transaction_id;
 IF provider_reference IS NULL AND NEW.status = 'success' THEN provider_reference := NEW.settlement_reference; END IF;
 response := jsonb_build_object('fulfillment_method',NEW.fulfillment_method,'settlement_reference',NEW.settlement_reference);
 UPDATE psp_transactions SET status = NEW.status, fulfillment_method = NEW.fulfillment_method,
   psp_transaction_id = provider_reference, confirmed_at = CASE WHEN NEW.status = 'success' THEN clock_timestamp() END,
   raw_response = response,
   workflow_signal_payload = jsonb_build_object('provider_transaction_id',COALESCE(provider_reference,''),'amount',target.amount,'currency',target.currency,'status',NEW.status,'raw_response',response),
   workflow_signal_delivered_at = NULL
 WHERE tenant_id = NEW.tenant_id AND client_reference = NEW.client_reference;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER apply_psp_manual_resolution AFTER INSERT ON psp_manual_resolutions FOR EACH ROW EXECUTE FUNCTION apply_psp_manual_resolution();

-- +goose StatementBegin
CREATE FUNCTION reject_withdrawal_lifecycle() RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
 IF NEW.decision_kind = 'withdrawal' AND NOT NEW.approved THEN
   UPDATE psp_transactions SET status = 'cancelled', response_message = NEW.reason,
     last_error_type = 'approval_rejected', last_error_at = NEW.decided_at
   WHERE tenant_id = NEW.tenant_id AND id = NEW.subject_id AND workflow_id = NEW.workflow_id
     AND status IN ('initiated','pending','held');
   IF NOT FOUND THEN RAISE EXCEPTION 'withdrawal already resolved' USING ERRCODE = '23514'; END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER reject_withdrawal_lifecycle AFTER INSERT ON workflow_decisions FOR EACH ROW EXECUTE FUNCTION reject_withdrawal_lifecycle();

-- +goose StatementBegin
CREATE FUNCTION record_p2p_lifecycle() RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE tenant TEXT; command_key TEXT; next_status TEXT; next_substatus TEXT; recipient BIGINT; prior transaction_status_events%ROWTYPE;
BEGIN
 tenant := NEW.tenant_id;
 command_key := NEW.idempotency_key;
 IF TG_TABLE_NAME = 'p2p_commands' THEN
   IF TG_OP = 'INSERT' THEN next_status := 'pending'; next_substatus := 'created';
   ELSIF NEW.run_id IS DISTINCT FROM OLD.run_id AND NEW.run_id IS NOT NULL THEN next_status := 'processing'; next_substatus := 'workflow_running';
   ELSE RETURN NEW; END IF;
 ELSIF TG_TABLE_NAME = 'p2p_command_failures' THEN next_status := 'failed'; next_substatus := NEW.error_code;
 ELSE
   IF NEW.reference_type <> 'p2p' OR NEW.status <> 'completed' THEN RETURN NEW; END IF;
   next_status := 'completed'; next_substatus := 'automated';
 END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('transaction_status:' || tenant || ':p2p:' || command_key,0));
 SELECT * INTO prior FROM transaction_status_events WHERE tenant_id = tenant AND aggregate_id = 'p2p:' || command_key ORDER BY version DESC LIMIT 1;
 IF prior.status IN ('completed','failed') THEN
   IF TG_TABLE_NAME = 'p2p_commands' OR prior.status = next_status THEN RETURN NEW; END IF;
   RAISE EXCEPTION 'terminal P2P outcome cannot change' USING ERRCODE = '23514';
 END IF;
 SELECT w.user_id INTO recipient FROM p2p_commands c JOIN wallets w ON w.tenant_id = c.tenant_id AND w.id = c.from_wallet_id
 WHERE c.tenant_id = tenant AND c.idempotency_key = command_key AND w.owner_type = 'user';
 IF NOT EXISTS(SELECT 1 FROM p2p_commands WHERE tenant_id = tenant AND idempotency_key = command_key) THEN RETURN NEW; END IF;
 INSERT INTO transaction_status_events(tenant_id,aggregate_id,version,status,substatus,user_id)
 VALUES(tenant,'p2p:' || command_key,COALESCE(prior.version,0)+1,next_status,next_substatus,recipient);
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER record_p2p_command_lifecycle AFTER INSERT OR UPDATE OF run_id ON p2p_commands FOR EACH ROW EXECUTE FUNCTION record_p2p_lifecycle();
CREATE TRIGGER record_p2p_failure_lifecycle AFTER INSERT ON p2p_command_failures FOR EACH ROW EXECUTE FUNCTION record_p2p_lifecycle();
CREATE TRIGGER record_p2p_settlement_lifecycle AFTER INSERT ON ledger_transactions FOR EACH ROW EXECUTE FUNCTION record_p2p_lifecycle();

ALTER TABLE interop_transfers
 ADD COLUMN lifecycle_status TEXT GENERATED ALWAYS AS (CASE status WHEN 'REQUESTED' THEN 'pending' WHEN 'SUCCEEDED' THEN 'completed' WHEN 'FAILED' THEN 'failed' ELSE 'processing' END) STORED,
 ADD COLUMN substatus TEXT GENERATED ALWAYS AS (CASE status
   WHEN 'REQUESTED' THEN 'created' WHEN 'ARMED' THEN 'funds_held' WHEN 'PENDING' THEN 'provider_pending'
   WHEN 'IN_DOUBT' THEN 'outcome_unknown' WHEN 'SUSPENSE' THEN 'reconciliation_required'
   WHEN 'SUCCEEDED' THEN 'automated' WHEN 'FAILED' THEN 'provider_failed' END) STORED,
 ADD COLUMN status_version BIGINT NOT NULL DEFAULT 1 CHECK (status_version > 0);

-- +goose StatementBegin
CREATE FUNCTION version_interop_lifecycle() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 NEW.status_version := OLD.status_version + CASE WHEN NEW.status <> OLD.status THEN 1 ELSE 0 END;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER version_interop_lifecycle BEFORE UPDATE ON interop_transfers FOR EACH ROW EXECUTE FUNCTION version_interop_lifecycle();

-- +goose StatementBegin
CREATE FUNCTION record_interop_lifecycle() RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
 IF TG_OP = 'UPDATE' AND OLD.status_version = NEW.status_version THEN RETURN NEW; END IF;
 INSERT INTO transaction_status_events(tenant_id,aggregate_id,version,status,substatus,user_id)
 VALUES(NEW.tenant_id,'interop:' || NEW.id,NEW.status_version,NEW.lifecycle_status,NEW.substatus,
   (SELECT w.user_id FROM interop_quotes q JOIN wallets w ON w.tenant_id = q.tenant_id AND w.id = q.wallet_id
    WHERE q.tenant_id = NEW.tenant_id AND q.id = NEW.quote_id AND w.owner_type = 'user'));
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER record_interop_lifecycle AFTER INSERT OR UPDATE ON interop_transfers FOR EACH ROW EXECUTE FUNCTION record_interop_lifecycle();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'Transaction resolutions and status events are durable; use a forward migration';
END $$;
-- +goose StatementEnd
