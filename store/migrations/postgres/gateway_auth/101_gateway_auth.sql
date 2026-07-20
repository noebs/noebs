-- +goose Up
CREATE TABLE backoffice_auth_flows (
    state_hash BYTEA PRIMARY KEY CHECK (octet_length(state_hash) = 32),
    browser_hash BYTEA NOT NULL CHECK (octet_length(browser_hash) = 32),
    pkce_key_id TEXT NOT NULL CHECK (pkce_key_id <> '' AND length(pkce_key_id) <= 128),
    pkce_nonce BYTEA NOT NULL CHECK (octet_length(pkce_nonce) = 12),
    pkce_ciphertext BYTEA NOT NULL CHECK (octet_length(pkce_ciphertext) >= 16),
    nonce_hash BYTEA NOT NULL CHECK (octet_length(nonce_hash) = 32),
    return_path TEXT NOT NULL CHECK (return_path <> '' AND length(return_path) <= 2048),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > created_at)
);

CREATE INDEX backoffice_auth_flows_expiry_idx
    ON backoffice_auth_flows(expires_at);

CREATE TABLE backoffice_sessions (
    session_hash BYTEA PRIMARY KEY CHECK (octet_length(session_hash) = 32),
    issuer TEXT NOT NULL CHECK (issuer <> '' AND length(issuer) <= 2048),
    subject TEXT NOT NULL CHECK (subject <> '' AND length(subject) <= 512),
    tokens_key_id TEXT NOT NULL CHECK (tokens_key_id <> '' AND length(tokens_key_id) <= 128),
    tokens_nonce BYTEA NOT NULL CHECK (octet_length(tokens_nonce) = 12),
    tokens_ciphertext BYTEA NOT NULL CHECK (octet_length(tokens_ciphertext) >= 16),
    access_expires_at TIMESTAMPTZ NOT NULL,
    refresh_expires_at TIMESTAMPTZ NOT NULL,
    idle_expires_at TIMESTAMPTZ NOT NULL,
    absolute_expires_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (access_expires_at > created_at),
    CHECK (refresh_expires_at > created_at),
    CHECK (idle_expires_at > created_at),
    CHECK (idle_expires_at <= absolute_expires_at),
    CHECK (absolute_expires_at > created_at),
    CHECK (last_seen_at >= created_at),
    CHECK (updated_at >= created_at)
);

CREATE INDEX backoffice_sessions_subject_idx
    ON backoffice_sessions(issuer, subject);
CREATE INDEX backoffice_sessions_refresh_expiry_idx
    ON backoffice_sessions(refresh_expires_at);
CREATE INDEX backoffice_sessions_idle_expiry_idx
    ON backoffice_sessions(idle_expires_at);
CREATE INDEX backoffice_sessions_absolute_expiry_idx
    ON backoffice_sessions(absolute_expires_at);

REVOKE ALL ON backoffice_auth_flows FROM PUBLIC;
REVOKE ALL ON backoffice_sessions FROM PUBLIC;

GRANT USAGE ON SCHEMA public TO gateway_auth_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON backoffice_auth_flows, backoffice_sessions TO gateway_auth_runtime;

GRANT USAGE ON SCHEMA public TO gateway_auth_cleanup;
GRANT SELECT (expires_at), DELETE ON backoffice_auth_flows TO gateway_auth_cleanup;
GRANT SELECT (refresh_expires_at, idle_expires_at, absolute_expires_at), DELETE ON backoffice_sessions TO gateway_auth_cleanup;

-- +goose Down
DROP TABLE backoffice_sessions;
DROP TABLE backoffice_auth_flows;
