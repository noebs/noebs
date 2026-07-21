-- +goose Up
CREATE TABLE currencies (
  code TEXT PRIMARY KEY CHECK (code ~ '^[A-Z]{3}$'),
  numeric_code TEXT UNIQUE CHECK (numeric_code IS NULL OR numeric_code ~ '^[0-9]{3}$'),
  name TEXT NOT NULL CHECK (name <> '' AND name = btrim(name)),
  kind TEXT NOT NULL CHECK (kind IN ('tender', 'fund', 'commodity', 'test', 'no_currency')),
  is_active BOOLEAN NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

-- Currency codes, numeric identities, and kinds are catalog identity. A
-- currency may be renamed or disabled, but deleting or repurposing its code
-- would change the interpretation of every version-bound monetary record.
-- +goose StatementBegin
CREATE FUNCTION enforce_currency_identity_immutable()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'currencies are immutable; disable the currency instead'
      USING ERRCODE = '55000';
  END IF;
  IF NEW.code IS DISTINCT FROM OLD.code OR
     NEW.numeric_code IS DISTINCT FROM OLD.numeric_code OR
     NEW.kind IS DISTINCT FROM OLD.kind OR
     NEW.created_at IS DISTINCT FROM OLD.created_at
  THEN
    RAISE EXCEPTION 'currency identity is immutable'
      USING ERRCODE = '55000';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER currencies_identity_immutable
BEFORE UPDATE OR DELETE ON currencies
FOR EACH ROW EXECUTE FUNCTION enforce_currency_identity_immutable();

CREATE TABLE currency_unit_versions (
  id BIGSERIAL PRIMARY KEY,
  currency_code TEXT NOT NULL REFERENCES currencies(code),
  iso_minor_exponent SMALLINT,
  display_exponent SMALLINT NOT NULL,
  cash_exponent SMALLINT NOT NULL,
  cash_rounding_increment BIGINT NOT NULL,
  valid_from DATE NOT NULL,
  valid_to DATE,
  source TEXT NOT NULL CHECK (source <> '' AND source = btrim(source)),
  source_revision TEXT NOT NULL CHECK (source_revision <> '' AND source_revision = btrim(source_revision)),
  source_published_on DATE NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CHECK (iso_minor_exponent IS NULL OR iso_minor_exponent BETWEEN 0 AND 18),
  CHECK (display_exponent BETWEEN 0 AND 18),
  CHECK (cash_exponent BETWEEN 0 AND 18),
  CHECK (cash_rounding_increment > 0),
  CHECK (iso_minor_exponent IS NULL OR cash_exponent <= iso_minor_exponent),
  CHECK (
    iso_minor_exponent IS NULL OR
    cash_rounding_increment * power(10::NUMERIC, iso_minor_exponent - cash_exponent) <= 9223372036854775807
  ),
  CHECK (valid_to IS NULL OR valid_to > valid_from),
  UNIQUE (currency_code, valid_from),
  UNIQUE (id, currency_code)
);

CREATE UNIQUE INDEX currency_unit_versions_one_current
  ON currency_unit_versions(currency_code)
  WHERE valid_to IS NULL;

CREATE INDEX currency_unit_versions_effective
  ON currency_unit_versions(currency_code, valid_from DESC, valid_to);

-- +goose StatementBegin
CREATE FUNCTION enforce_currency_unit_version_interval()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
	IF TG_OP = 'DELETE' THEN
	  RAISE EXCEPTION 'currency unit versions are immutable and cannot be deleted'
	    USING ERRCODE = '55000';
	END IF;
		IF TG_OP = 'UPDATE' THEN
	  IF NEW.id IS DISTINCT FROM OLD.id OR
	     NEW.currency_code IS DISTINCT FROM OLD.currency_code OR
	     NEW.iso_minor_exponent IS DISTINCT FROM OLD.iso_minor_exponent OR
	     NEW.display_exponent IS DISTINCT FROM OLD.display_exponent OR
	     NEW.cash_exponent IS DISTINCT FROM OLD.cash_exponent OR
	     NEW.cash_rounding_increment IS DISTINCT FROM OLD.cash_rounding_increment OR
	     NEW.valid_from IS DISTINCT FROM OLD.valid_from OR
	     NEW.source IS DISTINCT FROM OLD.source OR
	     NEW.source_revision IS DISTINCT FROM OLD.source_revision OR
	     NEW.source_published_on IS DISTINCT FROM OLD.source_published_on OR
	     NEW.created_at IS DISTINCT FROM OLD.created_at OR
	     (OLD.valid_to IS NOT NULL AND NEW.valid_to IS DISTINCT FROM OLD.valid_to)
		  THEN
	    RAISE EXCEPTION 'currency unit versions are immutable except for first closure of valid_to'
	      USING ERRCODE = '55000';
		  END IF;
		  IF OLD.valid_to IS NULL AND NEW.valid_to IS NOT NULL AND
		     NEW.valid_to <= CAST(clock_timestamp() AT TIME ZONE 'UTC' AS DATE)
		  THEN
		    RAISE EXCEPTION 'currency unit versions cannot be closed retroactively or on the current UTC date'
		      USING ERRCODE = '22007';
		  END IF;
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended(NEW.currency_code, 0));
  -- Wallet identity is one row per tenant/owner/currency, and double-entry
  -- postings require one exact unit version. Until a reviewed redenomination
  -- workflow can move every user and system wallet atomically, admitting a
  -- successor after any wallet exists would strand the currency across two
  -- incompatible ledgers.
  IF TG_OP = 'INSERT' AND
     EXISTS (
       SELECT 1
       FROM currency_unit_versions existing
       WHERE existing.currency_code = NEW.currency_code
     ) AND
     EXISTS (
       SELECT 1
       FROM wallets wallet
       WHERE wallet.currency = NEW.currency_code
     )
  THEN
    RAISE EXCEPTION 'currency unit successor for % requires a reviewed redenomination workflow', NEW.currency_code
      USING ERRCODE = '23514', CONSTRAINT = 'currency_unit_versions_wallet_transition';
  END IF;
  IF EXISTS (
    SELECT 1
    FROM currency_unit_versions existing
    WHERE existing.currency_code = NEW.currency_code
      AND existing.id <> NEW.id
      AND daterange(existing.valid_from, existing.valid_to, '[)') &&
          daterange(NEW.valid_from, NEW.valid_to, '[)')
  ) THEN
    RAISE EXCEPTION 'overlapping currency unit version interval for %', NEW.currency_code
      USING ERRCODE = '23P01';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER currency_unit_versions_no_overlap
BEFORE INSERT OR UPDATE OR DELETE
ON currency_unit_versions
FOR EACH ROW EXECUTE FUNCTION enforce_currency_unit_version_interval();

-- Closing an open version and inserting its successor must happen in the same
-- transaction. The deferred check sees a successor inserted after the UPDATE
-- while preventing a committed gap in the scale catalog.
-- +goose StatementBegin
CREATE FUNCTION require_currency_unit_version_successor()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF OLD.valid_to IS NULL AND NEW.valid_to IS NOT NULL AND NOT EXISTS (
    SELECT 1
    FROM currency_unit_versions successor
    WHERE successor.currency_code = NEW.currency_code
      AND successor.valid_from = NEW.valid_to
      AND successor.valid_to IS NULL
      AND successor.id <> NEW.id
  ) THEN
    RAISE EXCEPTION 'closing currency unit version % requires an open-ended successor beginning on %', NEW.id, NEW.valid_to
      USING ERRCODE = '23514';
  END IF;
  RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER currency_unit_versions_require_successor
AFTER UPDATE OF valid_to ON currency_unit_versions
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION require_currency_unit_version_successor();

CREATE TABLE fx_sources (
  id BIGSERIAL PRIMARY KEY,
  code TEXT NOT NULL UNIQUE CHECK (code ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$'),
  display_name TEXT NOT NULL CHECK (display_name <> '' AND display_name = btrim(display_name)),
  provider TEXT NOT NULL CHECK (provider ~ '^[a-z][a-z0-9]*(_[a-z0-9]+)*$'),
  purpose TEXT NOT NULL CHECK (purpose IN ('reference', 'tax', 'executable')),
  source_url TEXT NOT NULL CHECK (source_url ~ '^https://'),
  max_age_seconds INTEGER NOT NULL CHECK (max_age_seconds > 0),
  is_enabled BOOLEAN NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  UNIQUE (id, purpose)
);

CREATE TABLE fx_source_pairs (
  id BIGSERIAL PRIMARY KEY,
  source_id BIGINT NOT NULL REFERENCES fx_sources(id),
  base_currency_code TEXT NOT NULL REFERENCES currencies(code),
  quote_currency_code TEXT NOT NULL REFERENCES currencies(code),
  external_series TEXT NOT NULL CHECK (external_series <> '' AND external_series = btrim(external_series)),
  is_enabled BOOLEAN NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  CHECK (base_currency_code <> quote_currency_code),
  UNIQUE (source_id, base_currency_code, quote_currency_code),
  UNIQUE (source_id, external_series),
  UNIQUE (id, source_id, external_series, base_currency_code, quote_currency_code)
);

CREATE TABLE fx_source_pair_sides (
  source_pair_id BIGINT NOT NULL REFERENCES fx_source_pairs(id),
  side TEXT NOT NULL CHECK (side IN ('mid', 'bid', 'ask', 'fixed_reference')),
  PRIMARY KEY (source_pair_id, side)
);

-- Provider identity and parsing policy are provenance. Operational changes
-- disable a source/pair or add a new catalog row; they never rewrite or delete
-- the meaning of observations and quotes already attributed to a row.
-- +goose StatementBegin
CREATE FUNCTION enforce_fx_source_identity_immutable()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'FX sources are immutable; disable the source instead'
      USING ERRCODE = '55000';
  END IF;
  IF NEW.id IS DISTINCT FROM OLD.id OR
     NEW.code IS DISTINCT FROM OLD.code OR
     NEW.display_name IS DISTINCT FROM OLD.display_name OR
     NEW.provider IS DISTINCT FROM OLD.provider OR
     NEW.purpose IS DISTINCT FROM OLD.purpose OR
     NEW.source_url IS DISTINCT FROM OLD.source_url OR
     NEW.max_age_seconds IS DISTINCT FROM OLD.max_age_seconds OR
     NEW.created_at IS DISTINCT FROM OLD.created_at
  THEN
    RAISE EXCEPTION 'FX source provenance is immutable'
      USING ERRCODE = '55000';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER fx_sources_identity_immutable
BEFORE UPDATE OR DELETE ON fx_sources
FOR EACH ROW EXECUTE FUNCTION enforce_fx_source_identity_immutable();

-- +goose StatementBegin
CREATE FUNCTION enforce_fx_source_pair_identity_immutable()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'FX source pairs are immutable; disable the pair instead'
      USING ERRCODE = '55000';
  END IF;
  IF NEW.id IS DISTINCT FROM OLD.id OR
     NEW.source_id IS DISTINCT FROM OLD.source_id OR
     NEW.base_currency_code IS DISTINCT FROM OLD.base_currency_code OR
     NEW.quote_currency_code IS DISTINCT FROM OLD.quote_currency_code OR
     NEW.external_series IS DISTINCT FROM OLD.external_series OR
     NEW.created_at IS DISTINCT FROM OLD.created_at
  THEN
    RAISE EXCEPTION 'FX source pair provenance is immutable'
      USING ERRCODE = '55000';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER fx_source_pairs_identity_immutable
BEFORE UPDATE OR DELETE ON fx_source_pairs
FOR EACH ROW EXECUTE FUNCTION enforce_fx_source_pair_identity_immutable();

CREATE TABLE fx_observations (
  id BIGSERIAL PRIMARY KEY,
  source_id BIGINT NOT NULL REFERENCES fx_sources(id),
  source_pair_id BIGINT NOT NULL REFERENCES fx_source_pairs(id),
  external_series TEXT NOT NULL CHECK (external_series <> '' AND external_series = btrim(external_series)),
  base_currency_code TEXT NOT NULL,
  quote_currency_code TEXT NOT NULL,
  base_currency_unit_id BIGINT NOT NULL REFERENCES currency_unit_versions(id),
  quote_currency_unit_id BIGINT NOT NULL REFERENCES currency_unit_versions(id),
  -- Unconstrained NUMERIC plus an explicit scale/range check rejects excess
  -- precision. NUMERIC(38,18) would silently round it before validation.
  rate NUMERIC NOT NULL CHECK (
    rate > 0 AND
    rate < 100000000000000000000::NUMERIC AND
    rate = trunc(rate, 18)
  ),
  side TEXT NOT NULL CHECK (side IN ('mid', 'bid', 'ask', 'fixed_reference')),
  purpose TEXT NOT NULL CHECK (purpose IN ('reference', 'tax', 'executable')),
  observation_at TIMESTAMPTZ NOT NULL,
  published_at TIMESTAMPTZ,
  retrieved_at TIMESTAMPTZ NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  raw_payload_sha256 TEXT NOT NULL CHECK (raw_payload_sha256 ~ '^[0-9a-f]{64}$'),
  source_revision TEXT NOT NULL CHECK (source_revision <> '' AND source_revision = btrim(source_revision)),
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  FOREIGN KEY (source_pair_id, source_id, external_series, base_currency_code, quote_currency_code)
    REFERENCES fx_source_pairs(id, source_id, external_series, base_currency_code, quote_currency_code),
  FOREIGN KEY (source_id, purpose) REFERENCES fx_sources(id, purpose),
	FOREIGN KEY (source_pair_id, side) REFERENCES fx_source_pair_sides(source_pair_id, side),
  FOREIGN KEY (base_currency_unit_id, base_currency_code)
    REFERENCES currency_unit_versions(id, currency_code),
  FOREIGN KEY (quote_currency_unit_id, quote_currency_code)
    REFERENCES currency_unit_versions(id, currency_code),
	CHECK (published_at IS NULL OR (published_at >= observation_at AND published_at <= retrieved_at)),
	CHECK (retrieved_at >= observation_at),
	CHECK (expires_at > observation_at),
  UNIQUE (source_id, external_series, observation_at, side, raw_payload_sha256),
  UNIQUE (id, base_currency_unit_id, quote_currency_unit_id),
  UNIQUE (
    id, base_currency_unit_id, quote_currency_unit_id,
    base_currency_code, quote_currency_code
  ),
  UNIQUE (
    id, base_currency_unit_id, quote_currency_unit_id,
    base_currency_code, quote_currency_code, expires_at
  )
);

CREATE INDEX fx_observations_latest_pair
  ON fx_observations(source_pair_id, side, observation_at DESC, retrieved_at DESC);

-- +goose StatementBegin
CREATE FUNCTION validate_fx_observation_catalog_policy()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  source_max_age_seconds INTEGER;
  observation_day DATE;
BEGIN
  SELECT source.max_age_seconds
  INTO source_max_age_seconds
  FROM fx_sources source
  JOIN fx_source_pairs pair ON pair.source_id = source.id
  WHERE source.id = NEW.source_id
    AND source.is_enabled
    AND source.purpose = NEW.purpose
    AND pair.id = NEW.source_pair_id
    AND pair.is_enabled
    AND pair.external_series = NEW.external_series
    AND pair.base_currency_code = NEW.base_currency_code
    AND pair.quote_currency_code = NEW.quote_currency_code;

  IF NOT FOUND THEN
    RAISE EXCEPTION 'FX observation does not match an enabled source and pair policy'
      USING ERRCODE = '23514';
  END IF;

  IF NEW.expires_at IS DISTINCT FROM
     NEW.observation_at + make_interval(secs => source_max_age_seconds)
  THEN
    RAISE EXCEPTION 'FX observation expiry does not match source max-age policy'
      USING ERRCODE = '23514';
  END IF;

  observation_day := CAST(NEW.observation_at AT TIME ZONE 'UTC' AS DATE);
  IF NOT EXISTS (
    SELECT 1
    FROM currency_unit_versions unit
    WHERE unit.id = NEW.base_currency_unit_id
      AND unit.currency_code = NEW.base_currency_code
      AND unit.valid_from <= observation_day
      AND (unit.valid_to IS NULL OR unit.valid_to > observation_day)
  ) OR NOT EXISTS (
    SELECT 1
    FROM currency_unit_versions unit
    WHERE unit.id = NEW.quote_currency_unit_id
      AND unit.currency_code = NEW.quote_currency_code
      AND unit.valid_from <= observation_day
      AND (unit.valid_to IS NULL OR unit.valid_to > observation_day)
  ) THEN
    RAISE EXCEPTION 'FX observation currency units were not effective on the observation date'
      USING ERRCODE = '23514';
  END IF;

  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER fx_observations_catalog_policy
BEFORE INSERT ON fx_observations
FOR EACH ROW EXECUTE FUNCTION validate_fx_observation_catalog_policy();

-- +goose StatementBegin
CREATE FUNCTION reject_money_audit_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION '% records are append-only', TG_TABLE_NAME
    USING ERRCODE = '55000';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER fx_observations_append_only
BEFORE UPDATE OR DELETE ON fx_observations
FOR EACH ROW EXECUTE FUNCTION reject_money_audit_mutation();

CREATE TRIGGER fx_source_pair_sides_append_only
BEFORE UPDATE OR DELETE ON fx_source_pair_sides
FOR EACH ROW EXECUTE FUNCTION reject_money_audit_mutation();

CREATE TABLE money_conversion_quotes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  requested_by_user_id BIGINT NOT NULL CHECK (requested_by_user_id > 0),
  idempotency_key TEXT NOT NULL CHECK (
    idempotency_key <> '' AND idempotency_key = btrim(idempotency_key) AND
    octet_length(idempotency_key) <= 256
  ),
  observation_id BIGINT NOT NULL REFERENCES fx_observations(id),
  observation_base_currency_unit_id BIGINT NOT NULL,
  observation_quote_currency_unit_id BIGINT NOT NULL,
  observation_base_currency_code TEXT NOT NULL,
  observation_quote_currency_code TEXT NOT NULL,
  observation_expires_at TIMESTAMPTZ NOT NULL,
  input_currency_unit_id BIGINT NOT NULL,
  output_currency_unit_id BIGINT NOT NULL,
  input_currency_code TEXT NOT NULL,
  output_currency_code TEXT NOT NULL,
  input_minor_units BIGINT NOT NULL CHECK (input_minor_units > 0),
  output_minor_units BIGINT NOT NULL CHECK (output_minor_units >= 0),
  inverse BOOLEAN NOT NULL,
  rounding_mode TEXT NOT NULL CHECK (rounding_mode IN ('half_even', 'half_away_from_zero', 'toward_zero', 'floor', 'ceiling')),
  conversion_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  expires_at TIMESTAMPTZ NOT NULL,
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (
    observation_id, observation_base_currency_unit_id,
    observation_quote_currency_unit_id, observation_base_currency_code,
    observation_quote_currency_code, observation_expires_at
  ) REFERENCES fx_observations(
    id, base_currency_unit_id, quote_currency_unit_id,
    base_currency_code, quote_currency_code, expires_at
  ),
	FOREIGN KEY (input_currency_unit_id, input_currency_code)
	  REFERENCES currency_unit_versions(id, currency_code),
	FOREIGN KEY (output_currency_unit_id, output_currency_code)
	  REFERENCES currency_unit_versions(id, currency_code),
  CHECK (input_currency_unit_id <> output_currency_unit_id),
  CHECK (input_currency_code <> output_currency_code),
  CHECK (
    (NOT inverse
      AND input_currency_code = observation_base_currency_code
      AND output_currency_code = observation_quote_currency_code)
    OR
    (inverse
      AND input_currency_code = observation_quote_currency_code
      AND output_currency_code = observation_base_currency_code)
  ),
  CHECK (expires_at > created_at),
  CHECK (conversion_at <= created_at),
  CHECK (conversion_at < expires_at),
  CHECK (expires_at = observation_expires_at),
  UNIQUE (
    tenant_id, id, observation_id, conversion_at,
    input_currency_unit_id, output_currency_unit_id,
    input_currency_code, output_currency_code
  ),
  UNIQUE (tenant_id, requested_by_user_id, idempotency_key)
);

CREATE INDEX money_conversion_quotes_tenant_user
  ON money_conversion_quotes(tenant_id, requested_by_user_id, created_at DESC);

CREATE INDEX money_conversion_quotes_user_observation_quota
  ON money_conversion_quotes(tenant_id, requested_by_user_id, observation_id);

-- Resolve/validate the exact scale snapshot atomically with quote insertion.
-- The same per-currency advisory locks are taken by unit-version transitions,
-- in lexical order to avoid lock inversion between concurrent pairs.
-- +goose StatementBegin
CREATE FUNCTION validate_money_conversion_quote_snapshot()
RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
  first_currency TEXT;
  second_currency TEXT;
  conversion_day DATE;
BEGIN
  first_currency := least(NEW.input_currency_code, NEW.output_currency_code);
  second_currency := greatest(NEW.input_currency_code, NEW.output_currency_code);
  PERFORM pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended(first_currency, 0));
  PERFORM pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended(second_currency, 0));

  IF NEW.conversion_at > pg_catalog.clock_timestamp() THEN
    RAISE EXCEPTION 'money quote conversion time cannot be in the future'
      USING ERRCODE = '22007';
  END IF;
  conversion_day := CAST(NEW.conversion_at AT TIME ZONE 'UTC' AS DATE);

  IF NOT EXISTS (
    SELECT 1
    FROM public.currency_unit_versions unit
    JOIN public.currencies currency ON currency.code = unit.currency_code
    WHERE unit.id = NEW.input_currency_unit_id
      AND unit.currency_code = NEW.input_currency_code
      AND currency.is_active
      AND unit.valid_from <= conversion_day
      AND (unit.valid_to IS NULL OR unit.valid_to > conversion_day)
  ) OR NOT EXISTS (
    SELECT 1
    FROM public.currency_unit_versions unit
    JOIN public.currencies currency ON currency.code = unit.currency_code
    WHERE unit.id = NEW.output_currency_unit_id
      AND unit.currency_code = NEW.output_currency_code
      AND currency.is_active
      AND unit.valid_from <= conversion_day
      AND (unit.valid_to IS NULL OR unit.valid_to > conversion_day)
  ) THEN
    RAISE EXCEPTION 'money quote currency units are not active and effective at conversion time'
      USING ERRCODE = '23514';
  END IF;

  PERFORM 1
  FROM public.fx_observations observation
  JOIN public.fx_sources source ON source.id = observation.source_id
  JOIN public.fx_source_pairs pair ON pair.id = observation.source_pair_id
	  WHERE observation.id = NEW.observation_id
	    AND observation.observation_at <= NEW.conversion_at
	    AND observation.retrieved_at <= NEW.conversion_at
	    AND observation.created_at <= NEW.conversion_at
	    AND observation.expires_at > NEW.conversion_at
    AND source.is_enabled
    AND pair.is_enabled
  FOR SHARE OF observation, source, pair;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'money quote observation was not enabled, available, and fresh at conversion time'
      USING ERRCODE = '23514';
  END IF;

  RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- This trigger needs row locks on immutable catalog snapshots, while the
-- runtime role deliberately has no UPDATE privilege on those tables. Execute
-- only this fixed, schema-qualified body as the migration owner and leave no
-- callable SECURITY DEFINER surface for application roles.
REVOKE ALL ON FUNCTION public.validate_money_conversion_quote_snapshot() FROM PUBLIC;

CREATE TRIGGER money_conversion_quotes_snapshot_policy
BEFORE INSERT ON money_conversion_quotes
FOR EACH ROW EXECUTE FUNCTION validate_money_conversion_quote_snapshot();

CREATE TRIGGER money_conversion_quotes_append_only
BEFORE UPDATE OR DELETE ON money_conversion_quotes
FOR EACH ROW EXECUTE FUNCTION reject_money_audit_mutation();

INSERT INTO currencies(code, numeric_code, name, kind, is_active) VALUES
  ('AED', '784', 'UAE Dirham', 'tender', TRUE),
  ('BHD', '048', 'Bahraini Dinar', 'tender', TRUE),
  ('CHF', '756', 'Swiss Franc', 'tender', TRUE),
  ('CLF', '990', 'Unidad de Fomento', 'fund', TRUE),
  ('EGP', '818', 'Egyptian Pound', 'tender', TRUE),
  ('EUR', '978', 'Euro', 'tender', TRUE),
  ('GBP', '826', 'Pound Sterling', 'tender', TRUE),
  ('JPY', '392', 'Yen', 'tender', TRUE),
  ('KWD', '414', 'Kuwaiti Dinar', 'tender', TRUE),
  ('MRU', '929', 'Ouguiya', 'tender', TRUE),
  ('OMR', '512', 'Rial Omani', 'tender', TRUE),
  ('QAR', '634', 'Qatari Rial', 'tender', TRUE),
  ('SAR', '682', 'Saudi Riyal', 'tender', TRUE),
  ('SDG', '938', 'Sudanese Pound', 'tender', TRUE),
  ('USD', '840', 'US Dollar', 'tender', TRUE);

INSERT INTO currency_unit_versions(
  currency_code, iso_minor_exponent, display_exponent, cash_exponent,
  cash_rounding_increment, valid_from, source, source_revision, source_published_on
) VALUES
  ('AED', 2, 2, 2, 1, '2026-01-01', 'SIX ISO 4217 and Unicode CLDR', 'SIX-2026-01-01+CLDR-48.2.1', '2026-07-08'),
  ('BHD', 3, 3, 3, 1, '2026-01-01', 'SIX ISO 4217 and Unicode CLDR', 'SIX-2026-01-01+CLDR-48.2.1', '2026-07-08'),
  ('CHF', 2, 2, 2, 5, '2026-01-01', 'SIX ISO 4217 and Unicode CLDR', 'SIX-2026-01-01+CLDR-48.2.1', '2026-07-08'),
  ('CLF', 4, 4, 4, 1, '2026-01-01', 'SIX ISO 4217 and Unicode CLDR', 'SIX-2026-01-01+CLDR-48.2.1', '2026-07-08'),
  ('EGP', 2, 2, 2, 1, '2026-01-01', 'SIX ISO 4217 and Unicode CLDR', 'SIX-2026-01-01+CLDR-48.2.1', '2026-07-08'),
  ('EUR', 2, 2, 2, 1, '2026-01-01', 'SIX ISO 4217 and Unicode CLDR', 'SIX-2026-01-01+CLDR-48.2.1', '2026-07-08'),
  ('GBP', 2, 2, 2, 1, '2026-01-01', 'SIX ISO 4217 and Unicode CLDR', 'SIX-2026-01-01+CLDR-48.2.1', '2026-07-08'),
  ('JPY', 0, 0, 0, 1, '2026-01-01', 'SIX ISO 4217 and Unicode CLDR', 'SIX-2026-01-01+CLDR-48.2.1', '2026-07-08'),
  ('KWD', 3, 3, 3, 1, '2026-01-01', 'SIX ISO 4217 and Unicode CLDR', 'SIX-2026-01-01+CLDR-48.2.1', '2026-07-08'),
  -- Mauritania's 1/5-ouguiya coin is legal tender, so the cash quantum is
  -- 0.20 MRU = 20 operational minor units. This policy is grounded in the
  -- Mauritanian government's 2017-12-27 decree implementing Ordinance
  -- 2017-001, independently of unreleased CLDR development data.
  ('MRU', 2, 2, 2, 20, '2026-01-01', 'SIX ISO 4217, Unicode CLDR, and Mauritanian government AMI decree of 2017-12-27', 'SIX-2026-01-01+CLDR-48.2.1+MRU-AMI-2017-12-27', '2026-07-08'),
  ('OMR', 3, 3, 3, 1, '2026-01-01', 'SIX ISO 4217 and Unicode CLDR', 'SIX-2026-01-01+CLDR-48.2.1', '2026-07-08'),
  ('QAR', 2, 2, 2, 1, '2026-01-01', 'SIX ISO 4217 and Unicode CLDR', 'SIX-2026-01-01+CLDR-48.2.1', '2026-07-08'),
  ('SAR', 2, 2, 2, 1, '2026-01-01', 'SIX ISO 4217 and Unicode CLDR', 'SIX-2026-01-01+CLDR-48.2.1', '2026-07-08'),
  ('SDG', 2, 2, 2, 1, '2026-01-01', 'SIX ISO 4217 and Unicode CLDR', 'SIX-2026-01-01+CLDR-48.2.1', '2026-07-08'),
  ('USD', 2, 2, 2, 1, '2026-01-01', 'SIX ISO 4217 and Unicode CLDR', 'SIX-2026-01-01+CLDR-48.2.1', '2026-07-08');

-- Standalone monetary policy rows cannot inherit a unit identity from a
-- wallet. Bind each one to the unit version that was effective when the
-- policy or rate was created. If the catalog cannot prove that identity, the
-- migration fails instead of guessing a current version.
ALTER TABLE fee_configs ADD COLUMN currency_unit_version_id BIGINT;

UPDATE fee_configs fee
SET currency_unit_version_id = unit.id
FROM currency_unit_versions unit
WHERE unit.currency_code = fee.currency
  AND unit.valid_from <= CAST(fee.created_at AT TIME ZONE 'UTC' AS DATE)
  AND (unit.valid_to IS NULL OR unit.valid_to > CAST(fee.created_at AT TIME ZONE 'UTC' AS DATE));

-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM fee_configs WHERE currency_unit_version_id IS NULL) THEN
    RAISE EXCEPTION 'fee config migration found money without a provably effective unit definition'
      USING ERRCODE = '23514';
  END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE fee_configs
  ALTER COLUMN currency_unit_version_id SET NOT NULL,
  ADD CONSTRAINT fee_configs_currency_unit_fk
    FOREIGN KEY (currency_unit_version_id, currency)
    REFERENCES currency_unit_versions(id, currency_code),
  DROP CONSTRAINT fee_configs_tenant_id_transaction_type_currency_tier_min_key,
  ADD CONSTRAINT fee_configs_money_policy_unique
    UNIQUE (tenant_id, transaction_type, currency, currency_unit_version_id, tier_min);

CREATE INDEX fee_configs_exact_unit_lookup
  ON fee_configs(tenant_id, transaction_type, currency, currency_unit_version_id, is_active, tier_min);

ALTER TABLE exchange_rates
  ADD COLUMN base_currency_unit_version_id BIGINT,
  ADD COLUMN quote_currency_unit_version_id BIGINT;

UPDATE exchange_rates rate
SET base_currency_unit_version_id = base_unit.id,
    quote_currency_unit_version_id = quote_unit.id
FROM currency_unit_versions base_unit, currency_unit_versions quote_unit
WHERE base_unit.currency_code = rate.base_currency
  AND base_unit.valid_from <= CAST(rate.effective_from AT TIME ZONE 'UTC' AS DATE)
  AND (base_unit.valid_to IS NULL OR base_unit.valid_to > CAST(rate.effective_from AT TIME ZONE 'UTC' AS DATE))
  AND quote_unit.currency_code = rate.quote_currency
  AND quote_unit.valid_from <= CAST(rate.effective_from AT TIME ZONE 'UTC' AS DATE)
  AND (quote_unit.valid_to IS NULL OR quote_unit.valid_to > CAST(rate.effective_from AT TIME ZONE 'UTC' AS DATE));

-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM exchange_rates
    WHERE base_currency_unit_version_id IS NULL OR quote_currency_unit_version_id IS NULL
  ) THEN
    RAISE EXCEPTION 'exchange rate migration found a rate without provably effective base and quote unit definitions'
      USING ERRCODE = '23514';
  END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE exchange_rates
  ALTER COLUMN base_currency_unit_version_id SET NOT NULL,
  ALTER COLUMN quote_currency_unit_version_id SET NOT NULL,
  ADD CONSTRAINT exchange_rates_base_currency_unit_fk
    FOREIGN KEY (base_currency_unit_version_id, base_currency)
    REFERENCES currency_unit_versions(id, currency_code),
  ADD CONSTRAINT exchange_rates_quote_currency_unit_fk
    FOREIGN KEY (quote_currency_unit_version_id, quote_currency)
    REFERENCES currency_unit_versions(id, currency_code),
  DROP CONSTRAINT exchange_rates_tenant_id_base_currency_quote_currency_effec_key,
  ADD CONSTRAINT exchange_rates_money_policy_unique
    UNIQUE (
      tenant_id, base_currency, base_currency_unit_version_id,
      quote_currency, quote_currency_unit_version_id, effective_from
    );

CREATE INDEX exchange_rates_exact_unit_lookup
  ON exchange_rates(
    tenant_id, base_currency, base_currency_unit_version_id,
    quote_currency, quote_currency_unit_version_id, effective_from DESC
  );

-- Legacy transaction_limits rows have no creation/effective timestamp, so no
-- version can be inferred safely. Production currently has no such rows. A
-- non-empty installation must supply an explicit, reviewed mapping before
-- applying this migration; failing here is intentional.
ALTER TABLE transaction_limits ADD COLUMN currency_unit_version_id BIGINT;

-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM transaction_limits) THEN
    RAISE EXCEPTION 'transaction limit migration requires an explicit currency unit mapping for every existing row'
      USING ERRCODE = '23514';
  END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE transaction_limits
  ALTER COLUMN currency_unit_version_id SET NOT NULL,
  ADD CONSTRAINT transaction_limits_currency_unit_fk
    FOREIGN KEY (currency_unit_version_id, currency)
    REFERENCES currency_unit_versions(id, currency_code),
  DROP CONSTRAINT transaction_limits_tenant_id_kyc_tier_transaction_type_curr_key,
  ADD CONSTRAINT transaction_limits_money_policy_unique
    UNIQUE (tenant_id, kyc_tier, transaction_type, currency, currency_unit_version_id);

CREATE INDEX transaction_limits_exact_unit_lookup
  ON transaction_limits(tenant_id, kyc_tier, transaction_type, currency, currency_unit_version_id, is_active);

ALTER TABLE wallets ADD COLUMN currency_unit_version_id BIGINT;

UPDATE wallets wallet
SET currency_unit_version_id = unit.id
FROM currency_unit_versions unit
WHERE unit.currency_code = wallet.currency
  AND unit.valid_from <= CAST(wallet.created_at AT TIME ZONE 'UTC' AS DATE)
  AND (unit.valid_to IS NULL OR unit.valid_to > CAST(wallet.created_at AT TIME ZONE 'UTC' AS DATE));

-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM wallets wallet
    LEFT JOIN currency_unit_versions unit ON unit.id = wallet.currency_unit_version_id
    WHERE unit.id IS NULL
  ) THEN
    RAISE EXCEPTION 'wallet migration found money without an effective explicit unit definition'
      USING ERRCODE = '23514';
  END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE wallets
  ALTER COLUMN currency_unit_version_id SET NOT NULL,
  ADD CONSTRAINT wallets_currency_unit_fk
    FOREIGN KEY (currency_unit_version_id, currency)
    REFERENCES currency_unit_versions(id, currency_code),
  ADD CONSTRAINT wallets_money_identity_unique
    UNIQUE (tenant_id, id, currency, currency_unit_version_id);

-- Serialize wallet admission with catalog transitions. A wallet may use only
-- the effective open-ended version: once a successor is scheduled, creation
-- pauses for that currency until the successor becomes effective. Together
-- with the successor guard above this closes both orderings of the race
-- between the first wallet and a unit transition.
-- +goose StatementBegin
CREATE FUNCTION enforce_wallet_open_currency_unit()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended(NEW.currency, 0));
  IF NOT EXISTS (
    SELECT 1
    FROM currency_unit_versions unit
    WHERE unit.id = NEW.currency_unit_version_id
      AND unit.currency_code = NEW.currency
      AND unit.valid_from <= CAST(clock_timestamp() AT TIME ZONE 'UTC' AS DATE)
      AND unit.valid_to IS NULL
  ) THEN
    RAISE EXCEPTION 'wallet currency unit % is not the effective open-ended unit for %', NEW.currency_unit_version_id, NEW.currency
      USING ERRCODE = '23514', CONSTRAINT = 'wallets_open_currency_unit_required';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER wallets_open_currency_unit_required
BEFORE INSERT ON wallets
FOR EACH ROW EXECUTE FUNCTION enforce_wallet_open_currency_unit();

ALTER TABLE ledger_transactions ADD COLUMN currency_unit_version_id BIGINT;

UPDATE ledger_transactions AS ledger_transaction
SET currency_unit_version_id = transaction_unit.currency_unit_version_id
FROM (
  SELECT entry.tenant_id,
         entry.transaction_id,
         min(wallet.currency_unit_version_id) AS currency_unit_version_id
  FROM ledger_entries entry
  JOIN wallets wallet
    ON wallet.tenant_id = entry.tenant_id
   AND wallet.id = entry.wallet_id
   AND wallet.currency = entry.currency
  GROUP BY entry.tenant_id, entry.transaction_id
  HAVING count(DISTINCT wallet.currency_unit_version_id) = 1
) transaction_unit
WHERE transaction_unit.tenant_id = ledger_transaction.tenant_id
  AND transaction_unit.transaction_id = ledger_transaction.id;

-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM ledger_transactions ledger_transaction
    LEFT JOIN currency_unit_versions unit ON unit.id = ledger_transaction.currency_unit_version_id
    WHERE unit.id IS NULL
  ) THEN
    RAISE EXCEPTION 'ledger transaction migration found money without an effective explicit unit definition'
      USING ERRCODE = '23514';
  END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE ledger_transactions
  ALTER COLUMN currency_unit_version_id SET NOT NULL,
  ADD CONSTRAINT ledger_transactions_currency_unit_fk
    FOREIGN KEY (currency_unit_version_id, currency)
    REFERENCES currency_unit_versions(id, currency_code),
  ADD CONSTRAINT ledger_transactions_money_identity_unique
    UNIQUE (tenant_id, id, currency, currency_unit_version_id);

ALTER TABLE ledger_entries ADD COLUMN currency_unit_version_id BIGINT;

UPDATE ledger_entries entry
SET currency_unit_version_id = wallet.currency_unit_version_id
FROM wallets wallet
WHERE wallet.tenant_id = entry.tenant_id
  AND wallet.id = entry.wallet_id;

-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM ledger_entries WHERE currency_unit_version_id IS NULL) THEN
    RAISE EXCEPTION 'ledger entry migration found money without an explicit unit definition'
      USING ERRCODE = '23514';
  END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE ledger_entries
  ALTER COLUMN currency_unit_version_id SET NOT NULL,
  ADD CONSTRAINT ledger_entries_currency_unit_fk
    FOREIGN KEY (currency_unit_version_id, currency)
    REFERENCES currency_unit_versions(id, currency_code),
  ADD CONSTRAINT ledger_entries_wallet_money_fk
    FOREIGN KEY (tenant_id, wallet_id, currency, currency_unit_version_id)
    REFERENCES wallets(tenant_id, id, currency, currency_unit_version_id),
  ADD CONSTRAINT ledger_entries_transaction_money_fk
    FOREIGN KEY (tenant_id, transaction_id, currency, currency_unit_version_id)
    REFERENCES ledger_transactions(tenant_id, id, currency, currency_unit_version_id);

-- PSP and manual-transfer records are independently replayable/auditable money
-- facts, so they carry their own exact unit identity even when a wallet also
-- proves it. Existing rows are migrated only when that identity is derivable
-- from a linked, already-versioned record. PSP amount rows are intentionally
-- not inferred: their amount_kind can refer to requested, settlement, wallet,
-- fee, or net money in different currencies.
ALTER TABLE deposit_intents ADD COLUMN currency_unit_version_id BIGINT;

UPDATE deposit_intents intent
SET currency_unit_version_id = wallet.currency_unit_version_id
FROM wallets wallet
WHERE wallet.tenant_id = intent.tenant_id
  AND wallet.id = intent.wallet_id
  AND wallet.currency = intent.currency;

-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM deposit_intents WHERE currency_unit_version_id IS NULL) THEN
    RAISE EXCEPTION 'deposit intent migration found money without an explicit wallet unit definition'
      USING ERRCODE = '23514';
  END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE deposit_intents
  ALTER COLUMN currency_unit_version_id SET NOT NULL,
  ADD CONSTRAINT deposit_intents_currency_unit_fk
    FOREIGN KEY (currency_unit_version_id, currency)
    REFERENCES currency_unit_versions(id, currency_code),
  ADD CONSTRAINT deposit_intents_money_identity_unique
    UNIQUE (tenant_id, id, currency, currency_unit_version_id),
  ADD CONSTRAINT deposit_intents_wallet_money_fk
    FOREIGN KEY (tenant_id, wallet_id, currency, currency_unit_version_id)
    REFERENCES wallets(tenant_id, id, currency, currency_unit_version_id);

ALTER TABLE manual_transfers ADD COLUMN currency_unit_version_id BIGINT;

UPDATE manual_transfers transfer
SET currency_unit_version_id = wallet.currency_unit_version_id
FROM wallets wallet
WHERE wallet.tenant_id = transfer.tenant_id
  AND wallet.id = transfer.wallet_id
  AND wallet.currency = transfer.currency;

-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM manual_transfers WHERE currency_unit_version_id IS NULL) THEN
    RAISE EXCEPTION 'manual transfer migration found money without an explicit wallet unit definition'
      USING ERRCODE = '23514';
  END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE manual_transfers
  ALTER COLUMN currency_unit_version_id SET NOT NULL,
  ADD CONSTRAINT manual_transfers_currency_unit_fk
    FOREIGN KEY (currency_unit_version_id, currency)
    REFERENCES currency_unit_versions(id, currency_code),
  ADD CONSTRAINT manual_transfers_wallet_currency_fk
    FOREIGN KEY (tenant_id, wallet_id, currency, currency_unit_version_id)
    REFERENCES wallets(tenant_id, id, currency, currency_unit_version_id);

ALTER TABLE psp_transactions ADD COLUMN currency_unit_version_id BIGINT;

UPDATE psp_transactions transaction
SET currency_unit_version_id = intent.currency_unit_version_id
FROM deposit_intents intent
WHERE transaction.direction = 'inbound'
  AND intent.tenant_id = transaction.tenant_id
  AND intent.id = transaction.deposit_intent_id
  AND intent.currency = transaction.currency;

-- Historical outbound PSP transactions predate explicit unit identity. The
-- unit effective when the persisted record was created is the only provable
-- interpretation available; overlapping unit intervals are prohibited above.
UPDATE psp_transactions transaction
SET currency_unit_version_id = unit.id
FROM currency_unit_versions unit
WHERE transaction.direction = 'outbound'
  AND unit.currency_code = transaction.currency
  AND unit.valid_from <= CAST(transaction.created_at AT TIME ZONE 'UTC' AS DATE)
  AND (unit.valid_to IS NULL OR unit.valid_to > CAST(transaction.created_at AT TIME ZONE 'UTC' AS DATE));

-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM psp_transactions WHERE currency_unit_version_id IS NULL) THEN
    RAISE EXCEPTION 'PSP transaction migration found money without a provably effective unit definition'
      USING ERRCODE = '23514';
  END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE psp_transactions
  ALTER COLUMN currency_unit_version_id SET NOT NULL,
  ADD CONSTRAINT psp_transactions_currency_unit_fk
    FOREIGN KEY (currency_unit_version_id, currency)
    REFERENCES currency_unit_versions(id, currency_code),
  ADD CONSTRAINT psp_transactions_money_identity_unique
    UNIQUE (tenant_id, id, currency, currency_unit_version_id);

ALTER TABLE psp_transaction_amounts
  ADD COLUMN currency_unit_version_id BIGINT,
  -- Keep exact integers in unconstrained NUMERIC columns. NUMERIC(38,0)
  -- would round fractional direct-SQL input before a CHECK or trigger could
  -- reject it, silently changing the applied rate.
  ADD COLUMN fx_rate_numerator NUMERIC,
  ADD COLUMN fx_rate_denominator NUMERIC,
  ADD COLUMN fx_base_currency_unit_version_id BIGINT,
  ADD COLUMN fx_quote_currency_unit_version_id BIGINT,
  ADD COLUMN fx_observation_id BIGINT,
  ADD COLUMN fx_quote_id UUID,
  ADD COLUMN fx_conversion_at TIMESTAMPTZ,
  ADD COLUMN fx_observation_base_currency TEXT,
  ADD COLUMN fx_observation_quote_currency TEXT,
  ADD COLUMN fx_observation_base_currency_unit_version_id BIGINT,
  ADD COLUMN fx_observation_quote_currency_unit_version_id BIGINT;

-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM psp_transaction_amounts) THEN
    RAISE EXCEPTION 'PSP amount migration requires an explicit currency unit mapping for every existing row'
      USING ERRCODE = '23514';
  END IF;
END;
$$;
-- +goose StatementEnd

-- Requested and reported transaction amounts are durable monetary facts. Fail
-- the migration explicitly if legacy SQL bypassed the existing Go boundary;
-- constraints and immutable-fact triggers are installed immediately below.
-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM manual_transfers WHERE amount <= 0) THEN
    RAISE EXCEPTION 'manual transfer migration found a non-positive amount'
      USING ERRCODE = '23514';
  END IF;
  IF EXISTS (
    SELECT 1 FROM psp_transactions
    WHERE amount <= 0 OR fee_amount < 0 OR net_amount < 0
  ) THEN
    RAISE EXCEPTION 'PSP transaction migration found an invalid amount, fee, or net value'
      USING ERRCODE = '23514';
  END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE manual_transfers
  ADD CONSTRAINT manual_transfers_amount_positive CHECK (amount > 0);
ALTER TABLE psp_transactions
  ADD CONSTRAINT psp_transactions_amount_positive CHECK (amount > 0),
  ADD CONSTRAINT psp_transactions_fee_nonnegative CHECK (fee_amount IS NULL OR fee_amount >= 0),
  ADD CONSTRAINT psp_transactions_net_nonnegative CHECK (net_amount IS NULL OR net_amount >= 0);

ALTER TABLE psp_transaction_amounts
  ALTER COLUMN currency_unit_version_id SET NOT NULL,
  ADD CONSTRAINT psp_transaction_amounts_currency_unit_fk
    FOREIGN KEY (currency_unit_version_id, currency)
    REFERENCES currency_unit_versions(id, currency_code),
  ADD CONSTRAINT psp_transaction_amounts_fx_base_currency_unit_fk
    FOREIGN KEY (fx_base_currency_unit_version_id, fx_base_currency)
    REFERENCES currency_unit_versions(id, currency_code),
  ADD CONSTRAINT psp_transaction_amounts_fx_quote_currency_unit_fk
    FOREIGN KEY (fx_quote_currency_unit_version_id, fx_quote_currency)
    REFERENCES currency_unit_versions(id, currency_code),
  ADD CONSTRAINT psp_transaction_amounts_fx_observation_fk
    FOREIGN KEY (
      fx_observation_id,
      fx_observation_base_currency_unit_version_id,
      fx_observation_quote_currency_unit_version_id,
      fx_observation_base_currency,
      fx_observation_quote_currency
    ) REFERENCES fx_observations(
      id,
      base_currency_unit_id,
      quote_currency_unit_id,
      base_currency_code,
      quote_currency_code
    ),
  ADD CONSTRAINT psp_transaction_amounts_fx_quote_fk
    FOREIGN KEY (
      tenant_id,
      fx_quote_id,
      fx_observation_id,
      fx_conversion_at,
      fx_base_currency_unit_version_id,
      fx_quote_currency_unit_version_id,
      fx_base_currency,
      fx_quote_currency
    ) REFERENCES money_conversion_quotes(
      tenant_id,
      id,
      observation_id,
      conversion_at,
      input_currency_unit_id,
      output_currency_unit_id,
      input_currency_code,
      output_currency_code
    ),
  ADD CONSTRAINT psp_transaction_amounts_amount_positive CHECK (amount > 0),
  ADD CONSTRAINT psp_transaction_amounts_kind_valid CHECK (
    amount_kind IN (
      'requested', 'reported', 'settlement', 'fee', 'net',
      'wallet_credit', 'wallet_debit', 'overpayment', 'underpayment'
    )
  ),
  ADD CONSTRAINT psp_transaction_amounts_fx_identity_complete CHECK (
    (fx_rate IS NULL
      AND fx_rate_numerator IS NULL
      AND fx_rate_denominator IS NULL
      AND fx_base_currency IS NULL
      AND fx_quote_currency IS NULL
      AND fx_base_currency_unit_version_id IS NULL
      AND fx_quote_currency_unit_version_id IS NULL
      AND fx_source IS NULL
      AND fx_observation_id IS NULL
      AND fx_quote_id IS NULL
      AND fx_conversion_at IS NULL
      AND fx_observation_base_currency IS NULL
      AND fx_observation_quote_currency IS NULL
      AND fx_observation_base_currency_unit_version_id IS NULL
      AND fx_observation_quote_currency_unit_version_id IS NULL)
    OR
    (fx_rate IS NOT NULL
      AND fx_rate > 0
      AND fx_rate_numerator IS NOT NULL
      AND fx_rate_numerator >= 1
      AND fx_rate_numerator < 100000000000000000000000000000000000000::NUMERIC
      AND fx_rate_numerator = trunc(fx_rate_numerator)
      AND fx_rate_denominator IS NOT NULL
      AND fx_rate_denominator >= 1
      AND fx_rate_denominator < 100000000000000000000000000000000000000::NUMERIC
      AND fx_rate_denominator = trunc(fx_rate_denominator)
      AND fx_rate * 100000000::NUMERIC =
        div(fx_rate_numerator * 100000000::NUMERIC, fx_rate_denominator) +
        CASE
          WHEN mod(fx_rate_numerator * 100000000::NUMERIC, fx_rate_denominator) * 2 >= fx_rate_denominator
          THEN 1
          ELSE 0
        END
      AND fx_base_currency IS NOT NULL
      AND fx_quote_currency IS NOT NULL
      AND fx_base_currency <> fx_quote_currency
      AND fx_base_currency_unit_version_id IS NOT NULL
      AND fx_quote_currency_unit_version_id IS NOT NULL
      AND fx_source IS NOT NULL
      AND fx_source <> ''
      AND fx_source = btrim(fx_source)
      AND fx_conversion_at IS NOT NULL
      AND fx_conversion_at <= created_at
      AND (
        (currency = fx_base_currency
          AND currency_unit_version_id = fx_base_currency_unit_version_id)
        OR
        (currency = fx_quote_currency
          AND currency_unit_version_id = fx_quote_currency_unit_version_id)
      )
      AND (
        (fx_observation_id IS NULL
          AND fx_quote_id IS NULL
          AND fx_observation_base_currency IS NULL
          AND fx_observation_quote_currency IS NULL
          AND fx_observation_base_currency_unit_version_id IS NULL
          AND fx_observation_quote_currency_unit_version_id IS NULL)
        OR
        (fx_observation_id IS NOT NULL
          AND fx_observation_base_currency IS NOT NULL
          AND fx_observation_quote_currency IS NOT NULL
          AND fx_observation_base_currency_unit_version_id IS NOT NULL
          AND fx_observation_quote_currency_unit_version_id IS NOT NULL)
      ))
  );

-- +goose StatementBegin
CREATE FUNCTION validate_psp_transaction_amount_fx_provenance()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  conversion_day DATE;
  observation_record RECORD;
  quote_record RECORD;
  inverse_rate BOOLEAN;
  rate_gcd NUMERIC;
  rate_divisor NUMERIC;
  rate_remainder NUMERIC;
  projected_rate_scaled NUMERIC;
BEGIN
  IF NEW.fx_rate IS NULL THEN
    RETURN NEW;
  END IF;

  IF NEW.fx_rate_numerator IS NULL OR NEW.fx_rate_denominator IS NULL OR
     NOT (
       NEW.fx_rate_numerator >= 1 AND
       NEW.fx_rate_numerator < 100000000000000000000000000000000000000::NUMERIC AND
       NEW.fx_rate_numerator = trunc(NEW.fx_rate_numerator) AND
       NEW.fx_rate_denominator >= 1 AND
       NEW.fx_rate_denominator < 100000000000000000000000000000000000000::NUMERIC AND
       NEW.fx_rate_denominator = trunc(NEW.fx_rate_denominator)
     )
  THEN
    RAISE EXCEPTION 'PSP FX exact rate fraction is missing or invalid'
      USING ERRCODE = '23514';
  END IF;
  projected_rate_scaled :=
    div(NEW.fx_rate_numerator * 100000000::NUMERIC, NEW.fx_rate_denominator) +
    CASE
      WHEN mod(NEW.fx_rate_numerator * 100000000::NUMERIC, NEW.fx_rate_denominator) * 2 >= NEW.fx_rate_denominator
      THEN 1
      ELSE 0
    END;
  IF NEW.fx_rate * 100000000::NUMERIC IS DISTINCT FROM projected_rate_scaled THEN
    RAISE EXCEPTION 'PSP FX decimal rate does not equal its exact fraction projection'
      USING ERRCODE = '23514';
  END IF;
  rate_gcd := NEW.fx_rate_numerator;
  rate_divisor := NEW.fx_rate_denominator;
  WHILE rate_divisor <> 0 LOOP
    rate_remainder := mod(rate_gcd, rate_divisor);
    rate_gcd := rate_divisor;
    rate_divisor := rate_remainder;
  END LOOP;
  IF rate_gcd <> 1 THEN
    RAISE EXCEPTION 'PSP FX exact rate fraction is not reduced'
      USING ERRCODE = '23514';
  END IF;

  conversion_day := CAST(NEW.fx_conversion_at AT TIME ZONE 'UTC' AS DATE);
  IF NOT EXISTS (
    SELECT 1
    FROM currency_unit_versions unit
    WHERE unit.id = NEW.fx_base_currency_unit_version_id
      AND unit.currency_code = NEW.fx_base_currency
      AND unit.valid_from <= conversion_day
      AND (unit.valid_to IS NULL OR unit.valid_to > conversion_day)
  ) OR NOT EXISTS (
    SELECT 1
    FROM currency_unit_versions unit
    WHERE unit.id = NEW.fx_quote_currency_unit_version_id
      AND unit.currency_code = NEW.fx_quote_currency
      AND unit.valid_from <= conversion_day
      AND (unit.valid_to IS NULL OR unit.valid_to > conversion_day)
  ) THEN
    RAISE EXCEPTION 'PSP FX currency units were not effective at conversion time'
      USING ERRCODE = '23514';
  END IF;

  IF NEW.fx_observation_id IS NULL THEN
    RETURN NEW;
  END IF;

  SELECT observation.*, source.code AS source_code
  INTO observation_record
  FROM fx_observations observation
  JOIN fx_sources source ON source.id = observation.source_id
  WHERE observation.id = NEW.fx_observation_id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'PSP FX observation was not found'
      USING ERRCODE = '23514';
  END IF;
  IF observation_record.base_currency_code IS DISTINCT FROM NEW.fx_observation_base_currency OR
     observation_record.quote_currency_code IS DISTINCT FROM NEW.fx_observation_quote_currency OR
     observation_record.base_currency_unit_id IS DISTINCT FROM NEW.fx_observation_base_currency_unit_version_id OR
     observation_record.quote_currency_unit_id IS DISTINCT FROM NEW.fx_observation_quote_currency_unit_version_id OR
	     observation_record.source_code IS DISTINCT FROM NEW.fx_source OR
	     observation_record.observation_at > NEW.fx_conversion_at OR
	     observation_record.retrieved_at > NEW.fx_conversion_at OR
	     observation_record.created_at > NEW.fx_conversion_at OR
	     observation_record.expires_at <= NEW.fx_conversion_at
  THEN
    RAISE EXCEPTION 'PSP FX observation provenance mismatch'
      USING ERRCODE = '23514';
  END IF;

  IF NEW.fx_base_currency = observation_record.base_currency_code AND
     NEW.fx_quote_currency = observation_record.quote_currency_code
  THEN
    inverse_rate := FALSE;
    IF NEW.fx_rate_numerator IS DISTINCT FROM NEW.fx_rate_denominator * observation_record.rate THEN
      RAISE EXCEPTION 'PSP FX exact rate does not equal its direct observation'
        USING ERRCODE = '23514';
    END IF;
  ELSIF NEW.fx_base_currency = observation_record.quote_currency_code AND
        NEW.fx_quote_currency = observation_record.base_currency_code
  THEN
    inverse_rate := TRUE;
    IF NEW.fx_rate_numerator * observation_record.rate IS DISTINCT FROM NEW.fx_rate_denominator THEN
      RAISE EXCEPTION 'PSP FX exact rate does not equal its inverse observation'
        USING ERRCODE = '23514';
    END IF;
  ELSE
    RAISE EXCEPTION 'PSP FX applied pair does not match its observation'
      USING ERRCODE = '23514';
  END IF;

  IF NEW.fx_quote_id IS NULL THEN
    RETURN NEW;
  END IF;

  SELECT quote.*
  INTO quote_record
  FROM money_conversion_quotes quote
  WHERE quote.tenant_id = NEW.tenant_id
    AND quote.id = NEW.fx_quote_id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'PSP FX quote was not found for tenant'
      USING ERRCODE = '23514';
  END IF;
  IF quote_record.observation_id IS DISTINCT FROM NEW.fx_observation_id OR
     quote_record.conversion_at IS DISTINCT FROM NEW.fx_conversion_at OR
     quote_record.input_currency_code IS DISTINCT FROM NEW.fx_base_currency OR
     quote_record.output_currency_code IS DISTINCT FROM NEW.fx_quote_currency OR
     quote_record.input_currency_unit_id IS DISTINCT FROM NEW.fx_base_currency_unit_version_id OR
     quote_record.output_currency_unit_id IS DISTINCT FROM NEW.fx_quote_currency_unit_version_id OR
     quote_record.inverse IS DISTINCT FROM inverse_rate
  THEN
    RAISE EXCEPTION 'PSP FX quote provenance mismatch'
      USING ERRCODE = '23514';
  END IF;
  IF NEW.currency = NEW.fx_base_currency AND
     NEW.currency_unit_version_id = NEW.fx_base_currency_unit_version_id
  THEN
    IF NEW.amount IS DISTINCT FROM quote_record.input_minor_units THEN
      RAISE EXCEPTION 'PSP FX amount does not equal quoted input money'
        USING ERRCODE = '23514';
    END IF;
  ELSIF NEW.currency = NEW.fx_quote_currency AND
        NEW.currency_unit_version_id = NEW.fx_quote_currency_unit_version_id
  THEN
    IF NEW.amount IS DISTINCT FROM quote_record.output_minor_units THEN
      RAISE EXCEPTION 'PSP FX amount does not equal quoted output money'
        USING ERRCODE = '23514';
    END IF;
  ELSE
    RAISE EXCEPTION 'PSP FX amount is not a declared quote side'
      USING ERRCODE = '23514';
  END IF;

  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER psp_transaction_amounts_fx_provenance
BEFORE INSERT ON psp_transaction_amounts
FOR EACH ROW EXECUTE FUNCTION validate_psp_transaction_amount_fx_provenance();

-- Legacy PSP min/max columns are scale-ambiguous whenever a provider supports
-- more than one currency. Production has no configured rows, so this release
-- intentionally refuses to guess a mapping on any non-empty installation.
-- Operators must supply reviewed psp_amount_policies rows first.
-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM psp_configs WHERE min_amount IS NOT NULL OR max_amount IS NOT NULL
  ) OR EXISTS (
    SELECT 1 FROM psp_config_overrides WHERE min_amount IS NOT NULL OR max_amount IS NOT NULL
  ) THEN
    RAISE EXCEPTION 'PSP amount-bound migration requires explicit currency unit policies for legacy min/max values'
      USING ERRCODE = '23514';
  END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE psp_configs
  ADD CONSTRAINT psp_configs_legacy_amount_bounds_disabled
    CHECK (min_amount IS NULL AND max_amount IS NULL);
ALTER TABLE psp_config_overrides
  ADD CONSTRAINT psp_config_overrides_legacy_amount_bounds_disabled
    CHECK (min_amount IS NULL AND max_amount IS NULL);

CREATE TABLE psp_amount_policies (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL CONSTRAINT tenant_id_not_reserved CHECK (lower(btrim(tenant_id)) <> 'default'),
  provider_code TEXT NOT NULL,
  currency TEXT NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  currency_unit_version_id BIGINT NOT NULL,
  direction TEXT NOT NULL CHECK (direction IN ('deposit', 'withdrawal')),
  region TEXT NOT NULL DEFAULT '' CHECK (region = btrim(region) AND length(region) <= 128),
  min_amount BIGINT CHECK (min_amount IS NULL OR min_amount > 0),
  max_amount BIGINT CHECK (max_amount IS NULL OR max_amount > 0),
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
  FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  FOREIGN KEY (tenant_id, provider_code) REFERENCES psp_configs(tenant_id, provider_code),
  FOREIGN KEY (currency_unit_version_id, currency)
    REFERENCES currency_unit_versions(id, currency_code),
  CHECK (min_amount IS NOT NULL OR max_amount IS NOT NULL),
  CHECK (min_amount IS NULL OR max_amount IS NULL OR min_amount <= max_amount)
);

CREATE UNIQUE INDEX psp_amount_policies_one_active_scope
  ON psp_amount_policies(
    tenant_id, provider_code, currency, currency_unit_version_id, direction, region
  ) WHERE is_active;

CREATE INDEX psp_amount_policies_exact_lookup
  ON psp_amount_policies(
    tenant_id, provider_code, currency, currency_unit_version_id, direction, region, is_active
  );

-- Money identity is immutable once a wallet, transaction, or entry exists.
-- Wallet-linked operational records below can therefore inherit an exact unit
-- version through their wallet or ledger-entry foreign key without duplicating
-- a caller-supplied identifier at every write boundary.
-- +goose StatementBegin
CREATE FUNCTION enforce_persisted_money_identity_immutable()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.currency IS DISTINCT FROM OLD.currency OR
     NEW.currency_unit_version_id IS DISTINCT FROM OLD.currency_unit_version_id
  THEN
    RAISE EXCEPTION 'persisted money currency and unit version are immutable'
      USING ERRCODE = '55000';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER wallets_money_identity_immutable
BEFORE UPDATE ON wallets
FOR EACH ROW EXECUTE FUNCTION enforce_persisted_money_identity_immutable();

CREATE TRIGGER ledger_transactions_money_identity_immutable
BEFORE UPDATE ON ledger_transactions
FOR EACH ROW EXECUTE FUNCTION enforce_persisted_money_identity_immutable();

CREATE TRIGGER ledger_entries_money_identity_immutable
BEFORE UPDATE ON ledger_entries
FOR EACH ROW EXECUTE FUNCTION enforce_persisted_money_identity_immutable();

CREATE TRIGGER deposit_intents_money_identity_immutable
BEFORE UPDATE ON deposit_intents
FOR EACH ROW EXECUTE FUNCTION enforce_persisted_money_identity_immutable();

CREATE TRIGGER manual_transfers_money_identity_immutable
BEFORE UPDATE ON manual_transfers
FOR EACH ROW EXECUTE FUNCTION enforce_persisted_money_identity_immutable();

CREATE TRIGGER psp_transactions_money_identity_immutable
BEFORE UPDATE ON psp_transactions
FOR EACH ROW EXECUTE FUNCTION enforce_persisted_money_identity_immutable();

-- +goose StatementBegin
CREATE FUNCTION enforce_manual_transfer_money_fact_immutable()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.amount IS DISTINCT FROM OLD.amount THEN
    RAISE EXCEPTION 'persisted manual transfer amount is immutable'
      USING ERRCODE = '55000';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER manual_transfers_money_fact_immutable
BEFORE UPDATE ON manual_transfers
FOR EACH ROW EXECUTE FUNCTION enforce_manual_transfer_money_fact_immutable();

-- +goose StatementBegin
CREATE FUNCTION enforce_psp_transaction_money_fact_immutable()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.amount IS DISTINCT FROM OLD.amount OR
     NEW.fee_amount IS DISTINCT FROM OLD.fee_amount OR
     NEW.net_amount IS DISTINCT FROM OLD.net_amount
  THEN
    RAISE EXCEPTION 'persisted PSP transaction amount, fee, and net values are immutable'
      USING ERRCODE = '55000';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER psp_transactions_money_fact_immutable
BEFORE UPDATE ON psp_transactions
FOR EACH ROW EXECUTE FUNCTION enforce_psp_transaction_money_fact_immutable();

-- +goose StatementBegin
CREATE FUNCTION enforce_psp_amount_money_identity_immutable()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'persisted PSP amount facts are append-only'
    USING ERRCODE = '55000';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER psp_transaction_amounts_money_identity_immutable
BEFORE UPDATE OR DELETE ON psp_transaction_amounts
FOR EACH ROW EXECUTE FUNCTION enforce_psp_amount_money_identity_immutable();

-- +goose StatementBegin
CREATE FUNCTION enforce_psp_amount_policy_immutable()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'DELETE' OR
     NEW.id IS DISTINCT FROM OLD.id OR
     NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR
     NEW.provider_code IS DISTINCT FROM OLD.provider_code OR
     NEW.currency IS DISTINCT FROM OLD.currency OR
     NEW.currency_unit_version_id IS DISTINCT FROM OLD.currency_unit_version_id OR
     NEW.direction IS DISTINCT FROM OLD.direction OR
     NEW.region IS DISTINCT FROM OLD.region OR
     NEW.min_amount IS DISTINCT FROM OLD.min_amount OR
     NEW.max_amount IS DISTINCT FROM OLD.max_amount OR
     NEW.created_at IS DISTINCT FROM OLD.created_at OR
     (OLD.is_active = FALSE AND NEW.is_active = TRUE)
  THEN
    RAISE EXCEPTION 'PSP amount policies are immutable except for one-way deactivation'
      USING ERRCODE = '55000';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER psp_amount_policies_immutable
BEFORE UPDATE OR DELETE ON psp_amount_policies
FOR EACH ROW EXECUTE FUNCTION enforce_psp_amount_policy_immutable();

ALTER TABLE wallets
  ADD CONSTRAINT wallets_tenant_id_currency_unique UNIQUE (tenant_id, id, currency);
ALTER TABLE ledger_entries
  ADD CONSTRAINT ledger_entries_tenant_id_currency_unique UNIQUE (tenant_id, id, currency);
ALTER TABLE funding_sources
  ADD CONSTRAINT funding_sources_tenant_id_currency_unique UNIQUE (tenant_id, id, currency),
  ADD CONSTRAINT funding_sources_wallet_currency_fk
    FOREIGN KEY (tenant_id, wallet_id, currency)
    REFERENCES wallets(tenant_id, id, currency);
ALTER TABLE funding_source_withdrawal_reservations
  ADD CONSTRAINT funding_withdrawal_reservations_currency_unique
    UNIQUE (tenant_id, id, currency),
  ADD CONSTRAINT funding_source_withdrawal_reservations_source_currency_fk
    FOREIGN KEY (tenant_id, funding_source_id, currency)
    REFERENCES funding_sources(tenant_id, id, currency),
  ADD CONSTRAINT funding_source_withdrawal_reservations_entry_currency_fk
    FOREIGN KEY (tenant_id, ledger_entry_id, currency)
    REFERENCES ledger_entries(tenant_id, id, currency);
ALTER TABLE withdrawal_destinations
  ADD CONSTRAINT withdrawal_destinations_tenant_id_currency_unique UNIQUE (tenant_id, id, currency),
  ADD CONSTRAINT withdrawal_destinations_wallet_currency_fk
    FOREIGN KEY (tenant_id, wallet_id, currency)
    REFERENCES wallets(tenant_id, id, currency),
  ADD CONSTRAINT withdrawal_destinations_source_currency_fk
    FOREIGN KEY (tenant_id, linked_funding_source_id, currency)
    REFERENCES funding_sources(tenant_id, id, currency);
ALTER TABLE deposit_intents
  ADD CONSTRAINT deposit_intents_tenant_id_currency_unique UNIQUE (tenant_id, id, currency);
ALTER TABLE transaction_limit_period_usage
  ADD CONSTRAINT transaction_limit_period_usage_wallet_currency_fk
    FOREIGN KEY (tenant_id, wallet_id, currency)
    REFERENCES wallets(tenant_id, id, currency);
ALTER TABLE transaction_limit_reservations
  ADD CONSTRAINT transaction_limit_reservations_wallet_currency_fk
    FOREIGN KEY (tenant_id, wallet_id, currency)
    REFERENCES wallets(tenant_id, id, currency);
ALTER TABLE ledger_funding_links
  ADD CONSTRAINT ledger_funding_links_entry_currency_fk
    FOREIGN KEY (tenant_id, ledger_entry_id, currency)
    REFERENCES ledger_entries(tenant_id, id, currency),
  ADD CONSTRAINT ledger_funding_links_source_currency_fk
    FOREIGN KEY (tenant_id, funding_source_id, currency)
    REFERENCES funding_sources(tenant_id, id, currency),
  ADD CONSTRAINT ledger_funding_links_reservation_currency_fk
    FOREIGN KEY (tenant_id, withdrawal_reservation_id, currency)
    REFERENCES funding_source_withdrawal_reservations(tenant_id, id, currency);
ALTER TABLE ledger_withdrawal_destination_links
  ADD CONSTRAINT ledger_withdrawal_destination_links_entry_currency_fk
    FOREIGN KEY (tenant_id, ledger_entry_id, currency)
    REFERENCES ledger_entries(tenant_id, id, currency),
  ADD CONSTRAINT ledger_withdrawal_destination_links_destination_currency_fk
    FOREIGN KEY (tenant_id, destination_id, currency)
    REFERENCES withdrawal_destinations(tenant_id, id, currency);
ALTER TABLE psp_transactions
  ADD CONSTRAINT psp_transactions_deposit_intent_currency_fk
    FOREIGN KEY (tenant_id, deposit_intent_id, currency, currency_unit_version_id)
    REFERENCES deposit_intents(tenant_id, id, currency, currency_unit_version_id);

INSERT INTO fx_sources(
  code, display_name, provider, purpose, source_url, max_age_seconds, is_enabled
) VALUES
  ('ecb-reference', 'European Central Bank reference rates', 'ecb_sdmx', 'reference',
   'https://data-api.ecb.europa.eu/service/data/EXR', 604800, TRUE),
  ('cbos-reference', 'Central Bank of Sudan published rates', 'cbos_html', 'reference',
   'https://cbos.gov.sd/en/exchange-rates', 345600, FALSE);

INSERT INTO fx_source_pairs(
  source_id, base_currency_code, quote_currency_code, external_series, is_enabled
)
SELECT source.id, pair.base_currency_code, pair.quote_currency_code, pair.external_series, TRUE
FROM fx_sources source
JOIN (VALUES
  ('EUR', 'CHF', 'D.CHF.EUR.SP00.A'),
  ('EUR', 'GBP', 'D.GBP.EUR.SP00.A'),
  ('EUR', 'JPY', 'D.JPY.EUR.SP00.A'),
  ('EUR', 'USD', 'D.USD.EUR.SP00.A')
) AS pair(base_currency_code, quote_currency_code, external_series) ON TRUE
WHERE source.code = 'ecb-reference';

INSERT INTO fx_source_pairs(
  source_id, base_currency_code, quote_currency_code, external_series, is_enabled
)
SELECT source.id, pair.base_currency_code, pair.quote_currency_code, pair.external_series, TRUE
FROM fx_sources source
JOIN (VALUES
  ('AED', 'SDG', 'U.A.E Dirham'),
  ('SAR', 'SDG', 'Saudi Riyal'),
  ('USD', 'SDG', 'USD')
) AS pair(base_currency_code, quote_currency_code, external_series) ON TRUE
WHERE source.code = 'cbos-reference';

INSERT INTO fx_source_pair_sides(source_pair_id, side)
SELECT pair.id, configured.side
FROM fx_source_pairs pair
JOIN fx_sources source ON source.id = pair.source_id
JOIN LATERAL (
  SELECT 'mid'::TEXT AS side
  WHERE source.provider = 'ecb_sdmx'
  UNION ALL
  SELECT side
  FROM (VALUES ('bid'::TEXT), ('ask'::TEXT), ('mid'::TEXT)) AS cbos(side)
  WHERE source.provider = 'cbos_html'
) configured ON TRUE;

-- +goose Down
DROP TRIGGER psp_transaction_amounts_fx_provenance ON psp_transaction_amounts;
DROP FUNCTION validate_psp_transaction_amount_fx_provenance();
ALTER TABLE psp_transaction_amounts
  DROP CONSTRAINT psp_transaction_amounts_fx_quote_fk,
  DROP CONSTRAINT psp_transaction_amounts_fx_observation_fk;
DROP TABLE money_conversion_quotes;
DROP FUNCTION validate_money_conversion_quote_snapshot();
DROP TRIGGER fx_observations_append_only ON fx_observations;
DROP TRIGGER fx_observations_catalog_policy ON fx_observations;
DROP TRIGGER fx_source_pair_sides_append_only ON fx_source_pair_sides;
DROP FUNCTION reject_money_audit_mutation();
DROP FUNCTION validate_fx_observation_catalog_policy();
DROP TABLE fx_observations;
DROP TABLE fx_source_pair_sides;
DROP TRIGGER fx_source_pairs_identity_immutable ON fx_source_pairs;
DROP FUNCTION enforce_fx_source_pair_identity_immutable();
DROP TABLE fx_source_pairs;
DROP TRIGGER fx_sources_identity_immutable ON fx_sources;
DROP FUNCTION enforce_fx_source_identity_immutable();
DROP TABLE fx_sources;
ALTER TABLE psp_transactions
  DROP CONSTRAINT psp_transactions_deposit_intent_currency_fk;
ALTER TABLE ledger_withdrawal_destination_links
  DROP CONSTRAINT ledger_withdrawal_destination_links_destination_currency_fk,
  DROP CONSTRAINT ledger_withdrawal_destination_links_entry_currency_fk;
ALTER TABLE ledger_funding_links
  DROP CONSTRAINT ledger_funding_links_reservation_currency_fk,
  DROP CONSTRAINT ledger_funding_links_source_currency_fk,
  DROP CONSTRAINT ledger_funding_links_entry_currency_fk;
ALTER TABLE transaction_limit_reservations
  DROP CONSTRAINT transaction_limit_reservations_wallet_currency_fk;
ALTER TABLE transaction_limit_period_usage
  DROP CONSTRAINT transaction_limit_period_usage_wallet_currency_fk;
ALTER TABLE deposit_intents
  DROP CONSTRAINT deposit_intents_tenant_id_currency_unique;
ALTER TABLE withdrawal_destinations
  DROP CONSTRAINT withdrawal_destinations_source_currency_fk,
  DROP CONSTRAINT withdrawal_destinations_wallet_currency_fk,
  DROP CONSTRAINT withdrawal_destinations_tenant_id_currency_unique;
ALTER TABLE funding_source_withdrawal_reservations
  DROP CONSTRAINT funding_source_withdrawal_reservations_entry_currency_fk,
  DROP CONSTRAINT funding_source_withdrawal_reservations_source_currency_fk,
  DROP CONSTRAINT funding_withdrawal_reservations_currency_unique;
ALTER TABLE funding_sources
  DROP CONSTRAINT funding_sources_wallet_currency_fk,
  DROP CONSTRAINT funding_sources_tenant_id_currency_unique;
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_tenant_id_currency_unique;
ALTER TABLE wallets DROP CONSTRAINT wallets_tenant_id_currency_unique;
DROP TRIGGER ledger_entries_money_identity_immutable ON ledger_entries;
DROP TRIGGER ledger_transactions_money_identity_immutable ON ledger_transactions;
DROP TRIGGER wallets_money_identity_immutable ON wallets;
DROP TRIGGER psp_transaction_amounts_money_identity_immutable ON psp_transaction_amounts;
DROP FUNCTION enforce_psp_amount_money_identity_immutable();
DROP TRIGGER psp_transactions_money_fact_immutable ON psp_transactions;
DROP FUNCTION enforce_psp_transaction_money_fact_immutable();
DROP TRIGGER manual_transfers_money_fact_immutable ON manual_transfers;
DROP FUNCTION enforce_manual_transfer_money_fact_immutable();
DROP TRIGGER psp_transactions_money_identity_immutable ON psp_transactions;
DROP TRIGGER manual_transfers_money_identity_immutable ON manual_transfers;
DROP TRIGGER deposit_intents_money_identity_immutable ON deposit_intents;
DROP TRIGGER psp_amount_policies_immutable ON psp_amount_policies;
DROP FUNCTION enforce_psp_amount_policy_immutable();
DROP TABLE psp_amount_policies;
ALTER TABLE psp_config_overrides
  DROP CONSTRAINT psp_config_overrides_legacy_amount_bounds_disabled;
ALTER TABLE psp_configs
  DROP CONSTRAINT psp_configs_legacy_amount_bounds_disabled;
DROP FUNCTION enforce_persisted_money_identity_immutable();
ALTER TABLE psp_transaction_amounts
  DROP CONSTRAINT psp_transaction_amounts_fx_identity_complete,
  DROP CONSTRAINT psp_transaction_amounts_kind_valid,
  DROP CONSTRAINT psp_transaction_amounts_amount_positive,
  DROP CONSTRAINT psp_transaction_amounts_fx_quote_currency_unit_fk,
  DROP CONSTRAINT psp_transaction_amounts_fx_base_currency_unit_fk,
  DROP CONSTRAINT psp_transaction_amounts_currency_unit_fk,
  DROP COLUMN fx_observation_quote_currency_unit_version_id,
  DROP COLUMN fx_observation_base_currency_unit_version_id,
  DROP COLUMN fx_observation_quote_currency,
  DROP COLUMN fx_observation_base_currency,
  DROP COLUMN fx_conversion_at,
  DROP COLUMN fx_quote_id,
  DROP COLUMN fx_observation_id,
  DROP COLUMN fx_rate_denominator,
  DROP COLUMN fx_rate_numerator,
  DROP COLUMN fx_quote_currency_unit_version_id,
  DROP COLUMN fx_base_currency_unit_version_id,
  DROP COLUMN currency_unit_version_id;
ALTER TABLE psp_transactions
  DROP CONSTRAINT psp_transactions_net_nonnegative,
  DROP CONSTRAINT psp_transactions_fee_nonnegative,
  DROP CONSTRAINT psp_transactions_amount_positive,
  DROP CONSTRAINT psp_transactions_money_identity_unique,
  DROP CONSTRAINT psp_transactions_currency_unit_fk,
  DROP COLUMN currency_unit_version_id;
ALTER TABLE manual_transfers
  DROP CONSTRAINT manual_transfers_amount_positive,
  DROP CONSTRAINT manual_transfers_wallet_currency_fk,
  DROP CONSTRAINT manual_transfers_currency_unit_fk,
  DROP COLUMN currency_unit_version_id;
ALTER TABLE deposit_intents
  DROP CONSTRAINT deposit_intents_wallet_money_fk,
  DROP CONSTRAINT deposit_intents_money_identity_unique,
  DROP CONSTRAINT deposit_intents_currency_unit_fk,
  DROP COLUMN currency_unit_version_id;
ALTER TABLE ledger_entries DROP COLUMN currency_unit_version_id;
ALTER TABLE ledger_transactions DROP COLUMN currency_unit_version_id;
DROP TRIGGER wallets_open_currency_unit_required ON wallets;
DROP FUNCTION enforce_wallet_open_currency_unit();
ALTER TABLE wallets DROP COLUMN currency_unit_version_id;
DROP INDEX transaction_limits_exact_unit_lookup;
ALTER TABLE transaction_limits
  DROP CONSTRAINT transaction_limits_money_policy_unique,
  DROP CONSTRAINT transaction_limits_currency_unit_fk,
  DROP COLUMN currency_unit_version_id,
  ADD CONSTRAINT transaction_limits_tenant_id_kyc_tier_transaction_type_curr_key
    UNIQUE (tenant_id, kyc_tier, transaction_type, currency);
DROP INDEX exchange_rates_exact_unit_lookup;
ALTER TABLE exchange_rates
  DROP CONSTRAINT exchange_rates_money_policy_unique,
  DROP CONSTRAINT exchange_rates_quote_currency_unit_fk,
  DROP CONSTRAINT exchange_rates_base_currency_unit_fk,
  DROP COLUMN quote_currency_unit_version_id,
  DROP COLUMN base_currency_unit_version_id,
  ADD CONSTRAINT exchange_rates_tenant_id_base_currency_quote_currency_effec_key
    UNIQUE (tenant_id, base_currency, quote_currency, effective_from);
DROP INDEX fee_configs_exact_unit_lookup;
ALTER TABLE fee_configs
  DROP CONSTRAINT fee_configs_money_policy_unique,
  DROP CONSTRAINT fee_configs_currency_unit_fk,
  DROP COLUMN currency_unit_version_id,
  ADD CONSTRAINT fee_configs_tenant_id_transaction_type_currency_tier_min_key
    UNIQUE (tenant_id, transaction_type, currency, tier_min);
DROP TRIGGER currency_unit_versions_require_successor ON currency_unit_versions;
DROP FUNCTION require_currency_unit_version_successor();
DROP TRIGGER currency_unit_versions_no_overlap ON currency_unit_versions;
DROP FUNCTION enforce_currency_unit_version_interval();
DROP TABLE currency_unit_versions;
DROP TRIGGER currencies_identity_immutable ON currencies;
DROP FUNCTION enforce_currency_identity_immutable();
DROP TABLE currencies;
