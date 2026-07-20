-- +goose Up
CREATE TABLE tenants (
    id TEXT PRIMARY KEY CONSTRAINT tenant_id_canonical CHECK (
        id <> 'default' AND length(id) <= 63 AND id ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$'
    ),
    name TEXT NOT NULL CHECK (name <> '' AND name = btrim(name)),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

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

CREATE TABLE wallet_transaction_authorization_intents (
    intent_hash BYTEA PRIMARY KEY CHECK (octet_length(intent_hash) = 32),
    browser_start_hash BYTEA UNIQUE CHECK (
        browser_start_hash IS NULL OR octet_length(browser_start_hash) = 32
    ),
    tenant_id TEXT NOT NULL CHECK (tenant_id <> '' AND length(tenant_id) <= 63),
    issuer TEXT NOT NULL CHECK (issuer <> '' AND length(issuer) <= 2048),
    subject TEXT NOT NULL CHECK (subject <> '' AND length(subject) <= 512),
    operation TEXT NOT NULL CHECK (operation IN ('wallet.p2p', 'wallet.withdrawal')),
    request_digest BYTEA NOT NULL CHECK (octet_length(request_digest) = 32),
    idempotency_key TEXT NOT NULL CHECK (idempotency_key <> '' AND length(idempotency_key) <= 256),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > created_at),
    browser_started_at TIMESTAMPTZ,
    authorized_at TIMESTAMPTZ,
    authentication_time TIMESTAMPTZ,
    FOREIGN KEY (tenant_id) REFERENCES tenants(id),
    CHECK (
        (browser_start_hash IS NOT NULL AND browser_started_at IS NULL) OR
        (browser_start_hash IS NULL AND browser_started_at IS NOT NULL)
    ),
    CHECK (
		(authorized_at IS NULL AND authentication_time IS NULL) OR
		(authorized_at IS NOT NULL AND authentication_time IS NOT NULL AND
		 authorized_at >= created_at AND expires_at > authorized_at)
	)
);

CREATE INDEX wallet_transaction_authorization_intents_expiry_idx
    ON wallet_transaction_authorization_intents(expires_at);

CREATE TABLE wallet_transaction_authorization_flows (
    state_hash BYTEA PRIMARY KEY CHECK (octet_length(state_hash) = 32),
    intent_hash BYTEA NOT NULL UNIQUE REFERENCES wallet_transaction_authorization_intents(intent_hash) ON DELETE CASCADE,
    browser_hash BYTEA NOT NULL CHECK (octet_length(browser_hash) = 32),
    pkce_key_id TEXT NOT NULL CHECK (pkce_key_id <> '' AND length(pkce_key_id) <= 128),
    pkce_nonce BYTEA NOT NULL CHECK (octet_length(pkce_nonce) = 12),
    pkce_ciphertext BYTEA NOT NULL CHECK (octet_length(pkce_ciphertext) >= 16),
    nonce_hash BYTEA NOT NULL CHECK (octet_length(nonce_hash) = 32),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > created_at)
);

-- +goose Down
DROP TABLE wallet_transaction_authorization_flows;
DROP TABLE wallet_transaction_authorization_intents;
DROP TABLE backoffice_sessions;
DROP TABLE backoffice_auth_flows;
DROP TABLE tenants;
