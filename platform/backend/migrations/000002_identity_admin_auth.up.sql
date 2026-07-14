BEGIN;

CREATE SCHEMA IF NOT EXISTS identity;

CREATE TABLE identity.users (
    user_id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 128),
    account_status TEXT NOT NULL CHECK (account_status IN ('active', 'locked', 'disabled')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE identity.user_credentials (
    credential_id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES identity.users(user_id),
    credential_type TEXT NOT NULL CHECK (credential_type = 'password'),
    identifier_digest BYTEA NOT NULL,
    identifier_masked TEXT NOT NULL,
    password_hash BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (credential_type, identifier_digest)
);

CREATE TABLE identity.admin_sessions (
    session_id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES identity.users(user_id),
    token_family_id TEXT NOT NULL UNIQUE,
    transport TEXT NOT NULL CHECK (transport IN ('cookie', 'bearer')),
    authentication_method TEXT NOT NULL CHECK (authentication_method IN ('password', 'oidc', 'recovery')),
    session_version BIGINT NOT NULL DEFAULT 1 CHECK (session_version > 0),
    auth_time TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    access_expires_at TIMESTAMPTZ NOT NULL,
    refresh_expires_at TIMESTAMPTZ NOT NULL,
    absolute_expires_at TIMESTAMPTZ NOT NULL,
    csrf_digest BYTEA,
    risk_summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    revoked_at TIMESTAMPTZ,
    revoke_reason TEXT,
    CHECK ((transport = 'cookie' AND csrf_digest IS NOT NULL) OR (transport = 'bearer' AND csrf_digest IS NULL))
);

CREATE INDEX admin_sessions_user_active_idx
    ON identity.admin_sessions (user_id, refresh_expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE identity.admin_session_tokens (
    token_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES identity.admin_sessions(session_id),
    token_family_id TEXT NOT NULL,
    token_type TEXT NOT NULL CHECK (token_type IN ('access', 'refresh')),
    generation INTEGER NOT NULL CHECK (generation > 0),
    token_digest BYTEA NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    replaced_by_token_id TEXT,
    CHECK (token_type = 'refresh' OR consumed_at IS NULL)
);

CREATE INDEX admin_session_tokens_family_idx
    ON identity.admin_session_tokens (token_family_id, token_type, generation);

CREATE TABLE identity.admin_login_failures (
    identifier_digest BYTEA NOT NULL,
    source_digest BYTEA NOT NULL,
    failure_count INTEGER NOT NULL CHECK (failure_count >= 0),
    window_started_at TIMESTAMPTZ NOT NULL,
    last_failed_at TIMESTAMPTZ NOT NULL,
    blocked_until TIMESTAMPTZ,
    PRIMARY KEY (identifier_digest, source_digest)
);

CREATE TABLE identity.outbox_events (
    event_id TEXT PRIMARY KEY,
    topic TEXT NOT NULL,
    payload JSONB NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'published', 'dead')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ,
    last_error TEXT
);

CREATE INDEX identity_outbox_pending_idx
    ON identity.outbox_events (next_attempt_at, created_at)
    WHERE status IN ('pending', 'processing');

COMMIT;
