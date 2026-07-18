-- +goose Up
CREATE TABLE workload_request_nonces (
    key_id TEXT NOT NULL,
    audience TEXT NOT NULL,
    nonce TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (key_id, audience, nonce),
    CHECK (key_id <> ''),
    CHECK (audience <> ''),
    CHECK (nonce <> '')
);

CREATE INDEX workload_request_nonces_expiry_idx
    ON workload_request_nonces(expires_at);

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'workload_auth_runtime') THEN
        GRANT USAGE ON SCHEMA public TO workload_auth_runtime;
        GRANT INSERT ON workload_request_nonces TO workload_auth_runtime;
    END IF;
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'workload_auth_cleanup') THEN
        GRANT USAGE ON SCHEMA public TO workload_auth_cleanup;
        GRANT DELETE ON workload_request_nonces TO workload_auth_cleanup;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE workload_request_nonces;
