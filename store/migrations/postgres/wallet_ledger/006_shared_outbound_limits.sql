-- +goose Up
-- Explicit opt-in shared daily/monthly budget for local P2P and Mojaloop
-- outbound payments. Existing per-operation limits remain in force. No policy
-- is enabled by migration; deployment operators must choose exact scoped rows.
CREATE TABLE shared_outbound_limits (
 tenant_id TEXT NOT NULL REFERENCES tenants(id),
 kyc_tier TEXT NOT NULL CHECK (kyc_tier<>'' AND kyc_tier=btrim(kyc_tier)),
 currency TEXT NOT NULL,
 currency_unit_version_id BIGINT NOT NULL,
 daily_limit BIGINT NOT NULL CHECK (daily_limit>0),
 monthly_limit BIGINT NOT NULL CHECK (monthly_limit>=daily_limit),
 enabled BOOLEAN NOT NULL,
 PRIMARY KEY (tenant_id,kyc_tier,currency,currency_unit_version_id),
 FOREIGN KEY (currency_unit_version_id,currency) REFERENCES currency_unit_versions(id,currency_code)
);

-- +goose Down
DROP TABLE shared_outbound_limits;
