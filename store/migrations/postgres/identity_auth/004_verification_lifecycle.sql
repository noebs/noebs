-- +goose Up
ALTER TABLE identity_sessions ADD COLUMN verification_status TEXT GENERATED ALWAYS AS
  (CASE status WHEN 'submitted' THEN 'pending' WHEN 'needs_information' THEN 'pending'
   WHEN 'approved' THEN 'verified' WHEN 'rejected' THEN 'rejected' ELSE 'unverified' END) STORED;
ALTER TABLE identity_sessions ADD COLUMN substatus TEXT GENERATED ALWAYS AS
  (CASE status WHEN 'draft' THEN 'documents_required' WHEN 'submitted' THEN 'manual_review'
   WHEN 'needs_information' THEN 'information_required' ELSE status END) STORED;

CREATE TABLE identity_verifications (
  tenant_id TEXT NOT NULL,
  user_id BIGINT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('unverified','pending','verified','rejected')),
  substatus TEXT NOT NULL CHECK (substatus IN ('documents_required','manual_review','information_required','approved','rejected','withdrawn','discarded')),
  revision BIGINT NOT NULL CHECK (revision > 0),
  source_session_id UUID,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (tenant_id,user_id),
  FOREIGN KEY (tenant_id,user_id) REFERENCES users(tenant_id,id),
  FOREIGN KEY (tenant_id,user_id,source_session_id) REFERENCES identity_sessions(tenant_id,user_id,id)
);

INSERT INTO identity_verifications(tenant_id,user_id,status,substatus,revision,source_session_id,updated_at)
SELECT u.tenant_id,u.id,COALESCE(s.verification_status,'unverified'),COALESCE(s.substatus,'documents_required'),1,s.id,clock_timestamp()
FROM users u LEFT JOIN LATERAL (
 SELECT id,verification_status,substatus FROM identity_sessions
 WHERE tenant_id=u.tenant_id AND user_id=u.id AND NOT synthetic
 ORDER BY (status='approved') DESC,created_at DESC,id DESC LIMIT 1
) s ON true;

CREATE TABLE identity_status_events (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL REFERENCES tenants(id),
  user_id BIGINT NOT NULL CHECK (user_id > 0),
  source_session_id UUID,
  revision BIGINT NOT NULL CHECK (revision > 0),
  payload JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  published_at TIMESTAMPTZ,
  claimed_until TIMESTAMPTZ,
  claim_token UUID,
  publish_attempts INTEGER NOT NULL DEFAULT 0,
  last_error TEXT,
  UNIQUE (tenant_id,user_id,revision),
  FOREIGN KEY (tenant_id,user_id) REFERENCES identity_verifications(tenant_id,user_id),
  FOREIGN KEY (tenant_id,user_id,source_session_id) REFERENCES identity_sessions(tenant_id,user_id,id)
);
CREATE INDEX identity_status_events_pending ON identity_status_events(id) WHERE published_at IS NULL;

-- +goose StatementBegin
CREATE FUNCTION guard_identity_transition() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = OLD.status THEN RETURN NEW; END IF;
  IF NEW.revision <> OLD.revision + 1 OR NOT (
    (OLD.status = 'draft' AND NEW.status IN ('submitted','discarded','withdrawn')) OR
    (OLD.status = 'submitted' AND NEW.status IN ('approved','needs_information','rejected','withdrawn')) OR
    (OLD.status IN ('approved','needs_information','rejected') AND NEW.status = 'withdrawn')
  ) THEN RAISE EXCEPTION 'invalid verification transition' USING ERRCODE='23514'; END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER identity_transition BEFORE UPDATE ON identity_sessions FOR EACH ROW EXECUTE FUNCTION guard_identity_transition();

-- +goose StatementBegin
CREATE FUNCTION record_identity_status_event() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
BEGIN
  IF TG_OP='UPDATE' AND NEW.status=OLD.status AND NEW.substatus=OLD.substatus THEN RETURN NEW; END IF;
  INSERT INTO public.identity_status_events(tenant_id,user_id,source_session_id,revision,payload)
  VALUES(NEW.tenant_id,NEW.user_id,NEW.source_session_id,NEW.revision,jsonb_build_object(
    'event_id',gen_random_uuid(),'type','verification.status.changed.v1',
    'tenant_id',NEW.tenant_id,'user_id',NEW.user_id,'aggregate_type','verification',
    'aggregate_id','user:' || NEW.user_id,'version',NEW.revision,'status',NEW.status,
    'substatus',NEW.substatus,'occurred_at',NEW.updated_at));
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER identity_status_event AFTER INSERT OR UPDATE ON identity_verifications FOR EACH ROW EXECUTE FUNCTION record_identity_status_event();

-- +goose StatementBegin
CREATE FUNCTION initialize_identity_verification() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
BEGIN
  INSERT INTO public.identity_verifications(tenant_id,user_id,status,substatus,revision,updated_at)
  VALUES(NEW.tenant_id,NEW.id,'unverified','documents_required',1,clock_timestamp());
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER initialize_identity_verification AFTER INSERT ON users FOR EACH ROW EXECUTE FUNCTION initialize_identity_verification();

-- +goose StatementBegin
CREATE FUNCTION refresh_identity_verification() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public,pg_temp AS $$
DECLARE current_state public.identity_verifications%ROWTYPE; source public.identity_sessions%ROWTYPE;
BEGIN
  IF NEW.synthetic THEN RETURN NEW; END IF;
  IF TG_OP='UPDATE' AND NEW.status=OLD.status THEN RETURN NEW; END IF;
  SELECT * INTO STRICT current_state FROM public.identity_verifications WHERE tenant_id=NEW.tenant_id AND user_id=NEW.user_id FOR UPDATE;
  SELECT * INTO STRICT source FROM public.identity_sessions
    WHERE tenant_id=NEW.tenant_id AND user_id=NEW.user_id AND NOT synthetic
    ORDER BY (status='approved') DESC,created_at DESC,id DESC LIMIT 1;
  IF (current_state.status,current_state.substatus,current_state.source_session_id) IS NOT DISTINCT FROM
     (source.verification_status,source.substatus,source.id) THEN RETURN NEW; END IF;
  UPDATE public.identity_verifications SET status=source.verification_status,substatus=source.substatus,
    source_session_id=source.id,revision=revision+1,updated_at=clock_timestamp()
    WHERE tenant_id=NEW.tenant_id AND user_id=NEW.user_id;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER refresh_identity_verification AFTER INSERT OR UPDATE ON identity_sessions FOR EACH ROW EXECUTE FUNCTION refresh_identity_verification();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'Account verification revisions and events are durable; use a forward migration';
END $$;
-- +goose StatementEnd
