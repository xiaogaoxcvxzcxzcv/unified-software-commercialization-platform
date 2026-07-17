BEGIN;

ALTER TABLE identity.users
    ADD COLUMN user_version BIGINT NOT NULL DEFAULT 1 CHECK (user_version > 0),
    ADD COLUMN security_changed_at TIMESTAMPTZ;

ALTER TABLE identity.user_credentials
    ADD COLUMN credential_status TEXT NOT NULL DEFAULT 'active' CHECK (credential_status IN ('active', 'revoked')),
    ADD COLUMN password_algorithm TEXT NOT NULL DEFAULT 'bcrypt' CHECK (password_algorithm IN ('bcrypt', 'argon2id')),
    ADD COLUMN credential_version BIGINT NOT NULL DEFAULT 1 CHECK (credential_version > 0),
    ADD COLUMN password_changed_at TIMESTAMPTZ;

CREATE TABLE identity.user_identifiers (
    identifier_id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES identity.users(user_id),
    identifier_type TEXT NOT NULL CHECK (identifier_type IN ('email', 'phone')),
    normalization_version INTEGER NOT NULL CHECK (normalization_version > 0),
    normalized_digest BYTEA NOT NULL,
    masked_value TEXT NOT NULL CHECK (char_length(masked_value) BETWEEN 1 AND 160),
    verification_status TEXT NOT NULL CHECK (verification_status IN ('unverified', 'verified')),
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (identifier_type, normalized_digest),
    CHECK (octet_length(normalized_digest) = 32),
    CHECK ((verification_status = 'verified' AND verified_at IS NOT NULL) OR (verification_status = 'unverified' AND verified_at IS NULL))
);

CREATE INDEX user_identifiers_user_idx ON identity.user_identifiers (user_id, identifier_type);

CREATE TABLE identity.user_profiles (
    user_id TEXT PRIMARY KEY REFERENCES identity.users(user_id),
    profile_version BIGINT NOT NULL DEFAULT 1 CHECK (profile_version > 0),
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 128),
    avatar_ref TEXT,
    locale TEXT,
    timezone TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (avatar_ref IS NULL OR char_length(avatar_ref) BETWEEN 1 AND 512),
    CHECK (locale IS NULL OR char_length(locale) BETWEEN 2 AND 35),
    CHECK (timezone IS NULL OR char_length(timezone) BETWEEN 1 AND 64)
);

CREATE TABLE identity.end_user_sessions (
    session_id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES identity.users(user_id),
    product_id TEXT NOT NULL,
    application_id TEXT NOT NULL,
    tenant_id TEXT,
    token_family_id TEXT NOT NULL UNIQUE,
    authentication_method TEXT NOT NULL CHECK (authentication_method IN ('password', 'oidc', 'wechat', 'recovery')),
    session_version BIGINT NOT NULL DEFAULT 1 CHECK (session_version > 0),
    auth_time TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    access_expires_at TIMESTAMPTZ NOT NULL,
    refresh_expires_at TIMESTAMPTZ NOT NULL,
    absolute_expires_at TIMESTAMPTZ NOT NULL,
    risk_summary_digest BYTEA,
    revoked_at TIMESTAMPTZ,
    revoke_reason TEXT,
    UNIQUE (session_id, token_family_id),
    CHECK (char_length(product_id) > 0),
    CHECK (char_length(application_id) > 0),
    CHECK (risk_summary_digest IS NULL OR octet_length(risk_summary_digest) = 32),
    CHECK (access_expires_at <= refresh_expires_at),
    CHECK (refresh_expires_at <= absolute_expires_at),
    CHECK (tenant_id IS NULL OR char_length(tenant_id) > 0)
);

CREATE INDEX end_user_sessions_scope_active_idx
    ON identity.end_user_sessions (product_id, tenant_id, user_id, refresh_expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE identity.end_user_session_tokens (
    token_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    token_family_id TEXT NOT NULL,
    token_type TEXT NOT NULL CHECK (token_type IN ('access', 'refresh')),
    generation INTEGER NOT NULL CHECK (generation > 0),
    token_digest BYTEA NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    replaced_by_token_id TEXT,
    FOREIGN KEY (session_id, token_family_id) REFERENCES identity.end_user_sessions(session_id, token_family_id) ON DELETE CASCADE,
    CHECK (octet_length(token_digest) = 32),
    CHECK (token_type = 'refresh' OR consumed_at IS NULL)
);

CREATE UNIQUE INDEX end_user_session_tokens_family_generation_idx
    ON identity.end_user_session_tokens (token_family_id, token_type, generation);

CREATE TABLE identity.recovery_challenges (
    challenge_id TEXT PRIMARY KEY,
    continuation_digest BYTEA NOT NULL UNIQUE,
    identifier_type TEXT NOT NULL CHECK (identifier_type IN ('email', 'phone')),
    identifier_digest BYTEA NOT NULL,
    matched_user_id TEXT REFERENCES identity.users(user_id),
    delivery_target_masked TEXT NOT NULL CHECK (char_length(delivery_target_masked) BETWEEN 1 AND 160),
    proof_digest BYTEA NOT NULL UNIQUE,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    max_attempts INTEGER NOT NULL CHECK (max_attempts BETWEEN 1 AND 20),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    CHECK (octet_length(continuation_digest) = 32),
    CHECK (octet_length(identifier_digest) = 32),
    CHECK (octet_length(proof_digest) = 32),
    CHECK (expires_at > created_at),
    CHECK (attempt_count <= max_attempts)
);

CREATE INDEX recovery_challenges_identifier_idx
    ON identity.recovery_challenges (identifier_type, identifier_digest, expires_at);

CREATE FUNCTION identity.reject_recovery_reuse() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.attempt_count < OLD.attempt_count THEN
        RAISE EXCEPTION 'recovery challenge attempt count cannot decrease';
    END IF;
    IF OLD.consumed_at IS NOT NULL AND NEW.consumed_at IS DISTINCT FROM OLD.consumed_at THEN
        RAISE EXCEPTION 'consumed recovery challenge is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER recovery_challenge_one_way
BEFORE UPDATE ON identity.recovery_challenges
FOR EACH ROW EXECUTE FUNCTION identity.reject_recovery_reuse();

CREATE TABLE identity.external_identities (
    external_identity_id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES identity.users(user_id),
    provider TEXT NOT NULL CHECK (char_length(provider) BETWEEN 1 AND 64),
    provider_application_id TEXT NOT NULL,
    subject_digest BYTEA NOT NULL,
    subject_masked TEXT NOT NULL CHECK (char_length(subject_masked) BETWEEN 1 AND 160),
    union_subject_digest BYTEA,
    status TEXT NOT NULL CHECK (status IN ('active', 'revoked')),
    identity_version BIGINT NOT NULL DEFAULT 1 CHECK (identity_version > 0),
    linked_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (provider, provider_application_id, subject_digest),
    CHECK (octet_length(subject_digest) = 32),
    CHECK (union_subject_digest IS NULL OR octet_length(union_subject_digest) = 32),
    CHECK ((status = 'active' AND revoked_at IS NULL) OR (status = 'revoked' AND revoked_at IS NOT NULL))
);

CREATE INDEX external_identities_user_idx ON identity.external_identities (user_id, status);

CREATE TABLE identity.end_user_idempotency_records (
    operation TEXT NOT NULL,
    scope_id TEXT NOT NULL,
    actor_digest BYTEA NOT NULL,
    key_digest BYTEA NOT NULL,
    request_digest BYTEA NOT NULL,
    resource_id TEXT,
    state TEXT NOT NULL CHECK (state IN ('pending', 'completed', 'failed')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (operation, scope_id, actor_digest, key_digest),
    CHECK (octet_length(actor_digest) = 32),
    CHECK (octet_length(key_digest) = 32),
    CHECK (octet_length(request_digest) = 32)
);

COMMIT;
