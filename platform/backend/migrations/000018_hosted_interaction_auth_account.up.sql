BEGIN;

CREATE SCHEMA hosted_interaction;

CREATE TABLE hosted_interaction.interactions (
    interaction_id TEXT PRIMARY KEY CHECK (interaction_id ~ '^hint_[A-Za-z0-9_-]{24,160}$'),
    route_id TEXT NOT NULL CHECK (route_id IN ('hosted.auth', 'hosted.account')),
    product_id TEXT NOT NULL CHECK (char_length(product_id) BETWEEN 1 AND 160),
    application_id TEXT NOT NULL CHECK (char_length(application_id) BETWEEN 1 AND 160),
    tenant_id TEXT CHECK (tenant_id IS NULL OR char_length(tenant_id) BETWEEN 1 AND 160),
    environment TEXT NOT NULL CHECK (environment IN ('local', 'test', 'production')),
    channel TEXT NOT NULL CHECK (channel IN ('web', 'h5', 'desktop', 'app')),
    initiator_kind TEXT NOT NULL CHECK (initiator_kind IN ('client', 'user')),
    initiator_client_session_id TEXT NOT NULL CHECK (char_length(initiator_client_session_id) BETWEEN 1 AND 160),
    initiator_user_id TEXT,
    initiator_user_session_id TEXT,
    return_target_code TEXT NOT NULL CHECK (return_target_code ~ '^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$'),
    return_target_uri TEXT NOT NULL CHECK (char_length(return_target_uri) BETWEEN 1 AND 2048),
    return_target_policy_version BIGINT NOT NULL CHECK (return_target_policy_version > 0),
    state_ciphertext BYTEA NOT NULL,
    state_digest BYTEA NOT NULL UNIQUE,
    nonce_digest BYTEA UNIQUE,
    pkce_challenge_digest BYTEA,
    pkce_method TEXT,
    locale TEXT CHECK (locale IS NULL OR char_length(locale) BETWEEN 2 AND 32),
    theme_variant TEXT CHECK (theme_variant IS NULL OR char_length(theme_variant) BETWEEN 1 AND 64),
    status TEXT NOT NULL CHECK (status IN ('created', 'opened', 'authenticating', 'completed', 'exchanged', 'cancelled', 'failed', 'expired')),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    result_kind TEXT CHECK (result_kind IS NULL OR result_kind IN ('authorization_code', 'account_completed', 'cancelled', 'failed')),
    failure_code TEXT CHECK (failure_code IS NULL OR failure_code ~ '^[a-z][a-z0-9_.-]{0,127}$'),
    trace_id TEXT NOT NULL CHECK (char_length(trace_id) BETWEEN 1 AND 128),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    opened_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    terminal_at TIMESTAMPTZ,
    CHECK (expires_at > created_at),
    CHECK (
        (route_id = 'hosted.auth' AND initiator_kind = 'client' AND initiator_user_id IS NULL AND initiator_user_session_id IS NULL AND nonce_digest IS NOT NULL AND pkce_challenge_digest IS NOT NULL AND pkce_method = 'S256')
        OR (route_id = 'hosted.account' AND initiator_kind = 'user' AND initiator_user_id IS NOT NULL AND initiator_user_session_id IS NOT NULL AND nonce_digest IS NULL AND pkce_challenge_digest IS NULL AND pkce_method IS NULL)
    ),
    CHECK (
        (status IN ('created', 'opened', 'authenticating') AND completed_at IS NULL AND terminal_at IS NULL AND result_kind IS NULL AND failure_code IS NULL)
        OR (status = 'completed' AND completed_at IS NOT NULL AND terminal_at IS NULL AND result_kind IN ('authorization_code', 'account_completed') AND failure_code IS NULL)
        OR (status = 'exchanged' AND completed_at IS NOT NULL AND terminal_at IS NOT NULL AND result_kind IN ('authorization_code', 'account_completed') AND failure_code IS NULL)
        OR (status = 'cancelled' AND completed_at IS NULL AND terminal_at IS NOT NULL AND result_kind = 'cancelled' AND failure_code IS NULL)
        OR (status IN ('failed', 'expired') AND terminal_at IS NOT NULL AND result_kind = 'failed' AND failure_code IS NOT NULL)
    )
);

CREATE INDEX hosted_interactions_scope_idx
    ON hosted_interaction.interactions (product_id, application_id, tenant_id, status, expires_at);

CREATE TABLE hosted_interaction.browser_sessions (
    browser_session_id TEXT PRIMARY KEY CHECK (browser_session_id ~ '^hbs_[A-Za-z0-9_-]{24,160}$'),
    interaction_id TEXT NOT NULL REFERENCES hosted_interaction.interactions(interaction_id),
    token_digest BYTEA NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('active', 'revoked', 'expired')),
    created_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    revoke_reason TEXT,
    CHECK (expires_at > created_at),
    CHECK (last_seen_at >= created_at),
    CHECK ((status = 'active' AND revoked_at IS NULL AND revoke_reason IS NULL) OR (status <> 'active' AND revoked_at IS NOT NULL AND revoke_reason IS NOT NULL))
);

CREATE UNIQUE INDEX hosted_browser_sessions_one_active_idx
    ON hosted_interaction.browser_sessions (interaction_id)
    WHERE status = 'active';

CREATE TABLE hosted_interaction.completion_grants (
    grant_id TEXT PRIMARY KEY CHECK (grant_id ~ '^hgrant_[A-Za-z0-9_-]{24,160}$'),
    interaction_id TEXT NOT NULL UNIQUE REFERENCES hosted_interaction.interactions(interaction_id),
    grant_type TEXT NOT NULL CHECK (grant_type IN ('authorization_code', 'account_completed')),
    code_digest BYTEA NOT NULL UNIQUE,
    identity_proof_id TEXT,
    result_document JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(result_document) = 'object'),
    status TEXT NOT NULL CHECK (status IN ('available', 'processing', 'consumed', 'expired')),
    processing_token_digest BYTEA,
    processing_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    CHECK (expires_at > created_at),
    CHECK ((grant_type = 'authorization_code' AND identity_proof_id IS NOT NULL) OR (grant_type = 'account_completed' AND identity_proof_id IS NULL)),
    CHECK (
        (status = 'available' AND processing_token_digest IS NULL AND processing_expires_at IS NULL AND consumed_at IS NULL)
        OR (status = 'processing' AND processing_token_digest IS NOT NULL AND processing_expires_at IS NOT NULL AND consumed_at IS NULL)
        OR (status = 'consumed' AND processing_token_digest IS NULL AND processing_expires_at IS NULL AND consumed_at IS NOT NULL)
        OR (status = 'expired' AND processing_token_digest IS NULL AND processing_expires_at IS NULL AND consumed_at IS NULL)
    )
);

CREATE TABLE hosted_interaction.idempotency_records (
    operation TEXT NOT NULL CHECK (operation IN ('create', 'account_complete')),
    actor_digest BYTEA NOT NULL,
    key_digest BYTEA NOT NULL,
    request_digest BYTEA NOT NULL,
    interaction_id TEXT NOT NULL REFERENCES hosted_interaction.interactions(interaction_id),
    response_document JSONB NOT NULL CHECK (jsonb_typeof(response_document) = 'object'),
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (operation, actor_digest, key_digest)
);

CREATE TABLE hosted_interaction.outbox_events (
    event_id TEXT PRIMARY KEY CHECK (char_length(event_id) BETWEEN 1 AND 160),
    interaction_id TEXT NOT NULL REFERENCES hosted_interaction.interactions(interaction_id),
    event_type TEXT NOT NULL CHECK (event_type IN ('hosted.interaction_created.v1', 'hosted.interaction_opened.v1', 'hosted.interaction_completed.v1', 'hosted.interaction_cancelled.v1', 'hosted.interaction_exchanged.v1', 'hosted.interaction_failed.v1', 'hosted.interaction_expired.v1')),
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    occurred_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    lease_token TEXT,
    lease_expires_at TIMESTAMPTZ,
    next_attempt_at TIMESTAMPTZ NOT NULL,
    last_error_code TEXT,
    dead BOOLEAN NOT NULL DEFAULT FALSE,
    CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL)),
    CHECK ((published_at IS NULL OR (dead = FALSE AND lease_token IS NULL)) AND (dead = FALSE OR (published_at IS NULL AND lease_token IS NULL)))
);

CREATE INDEX hosted_outbox_claim_idx
    ON hosted_interaction.outbox_events (next_attempt_at, occurred_at, event_id)
    WHERE published_at IS NULL AND dead = FALSE;

CREATE FUNCTION hosted_interaction.enforce_interaction_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'hosted interactions are immutable facts';
    END IF;
    IF NEW.interaction_id <> OLD.interaction_id
        OR NEW.route_id <> OLD.route_id
        OR NEW.product_id <> OLD.product_id
        OR NEW.application_id <> OLD.application_id
        OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.environment <> OLD.environment
        OR NEW.channel <> OLD.channel
        OR NEW.initiator_kind <> OLD.initiator_kind
        OR NEW.initiator_client_session_id <> OLD.initiator_client_session_id
        OR NEW.initiator_user_id IS DISTINCT FROM OLD.initiator_user_id
        OR NEW.initiator_user_session_id IS DISTINCT FROM OLD.initiator_user_session_id
        OR NEW.return_target_code <> OLD.return_target_code
        OR NEW.return_target_uri <> OLD.return_target_uri
        OR NEW.return_target_policy_version <> OLD.return_target_policy_version
        OR NEW.state_ciphertext <> OLD.state_ciphertext
        OR NEW.state_digest <> OLD.state_digest
        OR NEW.nonce_digest IS DISTINCT FROM OLD.nonce_digest
        OR NEW.pkce_challenge_digest IS DISTINCT FROM OLD.pkce_challenge_digest
        OR NEW.pkce_method IS DISTINCT FROM OLD.pkce_method
        OR NEW.locale IS DISTINCT FROM OLD.locale
        OR NEW.theme_variant IS DISTINCT FROM OLD.theme_variant
        OR NEW.trace_id <> OLD.trace_id
        OR NEW.created_at <> OLD.created_at
        OR NEW.expires_at <> OLD.expires_at THEN
        RAISE EXCEPTION 'hosted interaction scope and security facts are immutable';
    END IF;
    IF NEW.version <> OLD.version + 1 THEN
        RAISE EXCEPTION 'hosted interaction version must advance once';
    END IF;
    IF OLD.status IN ('exchanged', 'cancelled', 'failed', 'expired') THEN
        RAISE EXCEPTION 'hosted interaction terminal state is immutable';
    END IF;
    IF NOT (
        (OLD.status = 'created' AND NEW.status IN ('opened', 'cancelled', 'failed', 'expired'))
        OR (OLD.status = 'opened' AND NEW.status IN ('opened', 'authenticating', 'completed', 'cancelled', 'failed', 'expired'))
        OR (OLD.status = 'authenticating' AND NEW.status IN ('opened', 'completed', 'cancelled', 'failed', 'expired'))
        OR (OLD.status = 'completed' AND NEW.status IN ('exchanged', 'expired'))
    ) THEN
        RAISE EXCEPTION 'invalid hosted interaction transition % -> %', OLD.status, NEW.status;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER hosted_interaction_one_way
BEFORE UPDATE OR DELETE ON hosted_interaction.interactions
FOR EACH ROW EXECUTE FUNCTION hosted_interaction.enforce_interaction_transition();

CREATE FUNCTION hosted_interaction.enforce_browser_session_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'hosted browser sessions are immutable';
    END IF;
    IF NEW.browser_session_id <> OLD.browser_session_id
        OR NEW.interaction_id <> OLD.interaction_id
        OR NEW.token_digest <> OLD.token_digest
        OR NEW.created_at <> OLD.created_at
        OR NEW.expires_at <> OLD.expires_at THEN
        RAISE EXCEPTION 'hosted browser session security facts are immutable';
    END IF;
    IF OLD.status <> 'active' THEN
        RAISE EXCEPTION 'hosted browser session terminal state is immutable';
    END IF;
    IF NEW.status NOT IN ('active', 'revoked', 'expired') OR NEW.last_seen_at < OLD.last_seen_at THEN
        RAISE EXCEPTION 'invalid hosted browser session transition';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER hosted_browser_session_one_way
BEFORE UPDATE OR DELETE ON hosted_interaction.browser_sessions
FOR EACH ROW EXECUTE FUNCTION hosted_interaction.enforce_browser_session_transition();

CREATE FUNCTION hosted_interaction.reject_idempotency_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'hosted idempotency records are immutable';
END;
$$;

CREATE TRIGGER hosted_idempotency_immutable
BEFORE UPDATE OR DELETE ON hosted_interaction.idempotency_records
FOR EACH ROW EXECUTE FUNCTION hosted_interaction.reject_idempotency_mutation();

CREATE FUNCTION hosted_interaction.enforce_grant_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'hosted completion grants are immutable';
    END IF;
    IF NEW.grant_id <> OLD.grant_id
        OR NEW.interaction_id <> OLD.interaction_id
        OR NEW.grant_type <> OLD.grant_type
        OR NEW.code_digest <> OLD.code_digest
        OR NEW.identity_proof_id IS DISTINCT FROM OLD.identity_proof_id
        OR NEW.result_document <> OLD.result_document
        OR NEW.created_at <> OLD.created_at
        OR NEW.expires_at <> OLD.expires_at THEN
        RAISE EXCEPTION 'hosted completion grant facts are immutable';
    END IF;
    IF OLD.status IN ('consumed', 'expired') THEN
        RAISE EXCEPTION 'hosted completion grant terminal state is immutable';
    END IF;
    IF NOT (
        (OLD.status = 'available' AND NEW.status IN ('processing', 'expired'))
        OR (OLD.status = 'processing' AND NEW.status IN ('processing', 'available', 'consumed', 'expired'))
    ) THEN
        RAISE EXCEPTION 'invalid hosted completion grant transition % -> %', OLD.status, NEW.status;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER hosted_grant_one_way
BEFORE UPDATE OR DELETE ON hosted_interaction.completion_grants
FOR EACH ROW EXECUTE FUNCTION hosted_interaction.enforce_grant_transition();

CREATE FUNCTION hosted_interaction.enforce_outbox_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'hosted outbox rows are immutable';
    END IF;
    IF NEW.event_id <> OLD.event_id
        OR NEW.interaction_id <> OLD.interaction_id
        OR NEW.event_type <> OLD.event_type
        OR NEW.payload <> OLD.payload
        OR NEW.occurred_at <> OLD.occurred_at THEN
        RAISE EXCEPTION 'hosted outbox facts are immutable';
    END IF;
    IF NEW.attempt_count < OLD.attempt_count OR NEW.attempt_count > OLD.attempt_count + 1 THEN
        RAISE EXCEPTION 'hosted outbox attempts must advance one claim at a time';
    END IF;
    IF OLD.published_at IS NOT NULL OR OLD.dead THEN
        RAISE EXCEPTION 'hosted outbox terminal state is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER hosted_outbox_one_way
BEFORE UPDATE OR DELETE ON hosted_interaction.outbox_events
FOR EACH ROW EXECUTE FUNCTION hosted_interaction.enforce_outbox_transition();

CREATE TABLE identity.hosted_auth_proofs (
    proof_id TEXT PRIMARY KEY CHECK (proof_id ~ '^hproof_[A-Za-z0-9_-]{24,160}$'),
    user_id TEXT NOT NULL REFERENCES identity.users(user_id),
    product_id TEXT NOT NULL CHECK (char_length(product_id) BETWEEN 1 AND 160),
    application_id TEXT NOT NULL CHECK (char_length(application_id) BETWEEN 1 AND 160),
    tenant_id TEXT CHECK (tenant_id IS NULL OR char_length(tenant_id) BETWEEN 1 AND 160),
    authentication_method TEXT NOT NULL CHECK (authentication_method IN ('password', 'oidc', 'wechat')),
    risk_summary_digest BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_by_grant_id TEXT UNIQUE,
    consumed_at TIMESTAMPTZ,
    CHECK (expires_at > created_at),
    CHECK ((consumed_by_grant_id IS NULL) = (consumed_at IS NULL))
);

CREATE TABLE identity.hosted_grant_redemptions (
    grant_id TEXT PRIMARY KEY CHECK (grant_id ~ '^hgrant_[A-Za-z0-9_-]{24,160}$'),
    proof_id TEXT NOT NULL UNIQUE REFERENCES identity.hosted_auth_proofs(proof_id),
    request_digest BYTEA NOT NULL,
    session_id TEXT NOT NULL UNIQUE REFERENCES identity.end_user_sessions(session_id),
    created_at TIMESTAMPTZ NOT NULL
);

CREATE FUNCTION identity.enforce_hosted_auth_proof_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'hosted auth proofs are immutable';
    END IF;
    IF NEW.proof_id <> OLD.proof_id
        OR NEW.user_id <> OLD.user_id
        OR NEW.product_id <> OLD.product_id
        OR NEW.application_id <> OLD.application_id
        OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.authentication_method <> OLD.authentication_method
        OR NEW.risk_summary_digest <> OLD.risk_summary_digest
        OR NEW.created_at <> OLD.created_at
        OR NEW.expires_at <> OLD.expires_at THEN
        RAISE EXCEPTION 'hosted auth proof facts are immutable';
    END IF;
    IF OLD.consumed_at IS NOT NULL OR NEW.consumed_at IS NULL OR NEW.consumed_by_grant_id IS NULL THEN
        RAISE EXCEPTION 'hosted auth proof may be consumed exactly once';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER identity_hosted_auth_proof_one_way
BEFORE UPDATE OR DELETE ON identity.hosted_auth_proofs
FOR EACH ROW EXECUTE FUNCTION identity.enforce_hosted_auth_proof_transition();

CREATE FUNCTION identity.reject_hosted_grant_redemption_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'hosted grant redemptions are immutable';
END;
$$;

CREATE TRIGGER identity_hosted_grant_redemption_immutable
BEFORE UPDATE OR DELETE ON identity.hosted_grant_redemptions
FOR EACH ROW EXECUTE FUNCTION identity.reject_hosted_grant_redemption_mutation();

COMMIT;
