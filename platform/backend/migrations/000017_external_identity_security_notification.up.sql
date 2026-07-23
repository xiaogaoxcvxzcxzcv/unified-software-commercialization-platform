BEGIN;

ALTER TABLE product_application.redirect_policy_entries
    DROP CONSTRAINT redirect_policy_entries_entry_type_check,
    ADD COLUMN target_code TEXT,
    ADD CONSTRAINT redirect_policy_entries_entry_type_check
        CHECK (entry_type IN ('web_redirect', 'origin', 'deep_link', 'auth_return_target')),
    ADD CONSTRAINT redirect_policy_entries_auth_target_shape
        CHECK (
            (entry_type = 'auth_return_target' AND target_code IS NOT NULL AND target_code ~ '^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$')
            OR (entry_type <> 'auth_return_target' AND target_code IS NULL)
        );

CREATE UNIQUE INDEX redirect_policy_entries_auth_target_code_idx
    ON product_application.redirect_policy_entries (policy_id, target_code)
    WHERE entry_type = 'auth_return_target';

ALTER TABLE product_application.outbox_events
    ADD COLUMN lease_token TEXT,
    ADD COLUMN lease_expires_at TIMESTAMPTZ,
    ADD CONSTRAINT product_application_outbox_lease_shape CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL)),
    ADD CONSTRAINT product_application_outbox_terminal_shape CHECK (
        (published_at IS NULL OR (dead = FALSE AND lease_token IS NULL))
        AND (dead = FALSE OR (published_at IS NULL AND lease_token IS NULL))
    );

CREATE FUNCTION product_application.enforce_outbox_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'product application outbox rows are immutable';
    END IF;
    IF NEW.event_id <> OLD.event_id
        OR NEW.aggregate_id <> OLD.aggregate_id
        OR NEW.event_type <> OLD.event_type
        OR NEW.payload <> OLD.payload
        OR NEW.occurred_at <> OLD.occurred_at THEN
        RAISE EXCEPTION 'product application outbox facts are immutable';
    END IF;
    IF NEW.attempt_count < OLD.attempt_count OR NEW.attempt_count > OLD.attempt_count + 1 THEN
        RAISE EXCEPTION 'product application outbox attempts must advance one claim at a time';
    END IF;
    IF OLD.published_at IS NOT NULL OR OLD.dead THEN
        RAISE EXCEPTION 'product application outbox terminal state is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER product_application_outbox_one_way
BEFORE UPDATE OR DELETE ON product_application.outbox_events
FOR EACH ROW EXECUTE FUNCTION product_application.enforce_outbox_transition();

ALTER TABLE identity.recovery_challenges
    ADD COLUMN delivery_status TEXT NOT NULL DEFAULT 'active'
        CHECK (delivery_status IN ('pending', 'active'));

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM identity.end_user_sessions
        WHERE authentication_method IN ('oidc', 'wechat')
    ) THEN
        RAISE EXCEPTION 'migration 000017 requires external identity provenance backfill for existing external-auth sessions';
    END IF;
END;
$$;

ALTER TABLE identity.end_user_sessions
    ADD COLUMN external_identity_id TEXT REFERENCES identity.external_identities(external_identity_id),
    ADD CONSTRAINT end_user_sessions_external_identity_shape CHECK (
        (authentication_method IN ('oidc', 'wechat') AND external_identity_id IS NOT NULL)
        OR (authentication_method NOT IN ('oidc', 'wechat') AND external_identity_id IS NULL)
    );

CREATE TABLE identity.external_auth_flows (
    flow_id TEXT PRIMARY KEY,
    product_id TEXT NOT NULL,
    application_id TEXT NOT NULL,
    tenant_id TEXT,
    environment TEXT NOT NULL CHECK (environment IN ('local', 'test', 'production')),
    provider TEXT NOT NULL CHECK (char_length(provider) BETWEEN 1 AND 64),
    provider_application_ref TEXT NOT NULL CHECK (char_length(provider_application_ref) BETWEEN 1 AND 160),
    mode TEXT NOT NULL CHECK (mode IN ('redirect', 'qr', 'native')),
    return_target_code TEXT NOT NULL CHECK (return_target_code ~ '^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$'),
    return_target_uri TEXT NOT NULL CHECK (char_length(return_target_uri) BETWEEN 1 AND 2048),
    return_target_policy_version BIGINT NOT NULL CHECK (return_target_policy_version > 0),
    state_digest BYTEA NOT NULL UNIQUE,
    nonce_digest BYTEA NOT NULL UNIQUE,
    pkce_challenge_digest BYTEA,
    browser_session_digest BYTEA,
    authorization_code_digest BYTEA,
    processing_token_digest BYTEA,
    processing_expires_at TIMESTAMPTZ,
    status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'consumed', 'failed', 'expired')),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    failure_code TEXT,
    CHECK (char_length(product_id) BETWEEN 1 AND 160),
    CHECK (char_length(application_id) BETWEEN 1 AND 160),
    CHECK (tenant_id IS NULL OR char_length(tenant_id) BETWEEN 1 AND 160),
    CHECK (octet_length(state_digest) = 32),
    CHECK (octet_length(nonce_digest) = 32),
    CHECK (pkce_challenge_digest IS NULL OR octet_length(pkce_challenge_digest) = 32),
    CHECK (browser_session_digest IS NULL OR octet_length(browser_session_digest) = 32),
    CHECK (authorization_code_digest IS NULL OR octet_length(authorization_code_digest) = 32),
    CHECK (failure_code IS NULL OR failure_code ~ '^[A-Za-z][A-Za-z0-9_.-]{0,127}$'),
    CHECK (processing_token_digest IS NULL OR octet_length(processing_token_digest) = 32),
    CHECK ((processing_token_digest IS NULL) = (processing_expires_at IS NULL)),
    CHECK (expires_at > created_at),
    CHECK (
        (status = 'pending' AND consumed_at IS NULL AND authorization_code_digest IS NULL AND failure_code IS NULL AND processing_token_digest IS NULL)
        OR (status = 'processing' AND consumed_at IS NULL AND authorization_code_digest IS NULL AND failure_code IS NULL AND processing_token_digest IS NOT NULL AND processing_expires_at > created_at)
        OR (status = 'consumed' AND consumed_at IS NOT NULL AND authorization_code_digest IS NOT NULL AND failure_code IS NULL AND processing_token_digest IS NULL)
        OR (status IN ('failed', 'expired') AND consumed_at IS NOT NULL AND authorization_code_digest IS NULL AND failure_code IS NOT NULL AND processing_token_digest IS NULL)
    )
);

CREATE INDEX external_auth_flows_scope_pending_idx
    ON identity.external_auth_flows (product_id, application_id, provider, expires_at)
    WHERE status = 'pending';

CREATE UNIQUE INDEX external_auth_flows_authorization_code_idx
    ON identity.external_auth_flows (provider, provider_application_ref, authorization_code_digest)
    WHERE authorization_code_digest IS NOT NULL;

CREATE TABLE identity.external_identity_proofs (
    proof_id TEXT PRIMARY KEY,
    flow_id TEXT NOT NULL REFERENCES identity.external_auth_flows(flow_id),
    product_id TEXT NOT NULL,
    application_id TEXT NOT NULL,
    tenant_id TEXT,
    provider TEXT NOT NULL CHECK (char_length(provider) BETWEEN 1 AND 64),
    provider_application_ref TEXT NOT NULL CHECK (char_length(provider_application_ref) BETWEEN 1 AND 160),
    subject_digest BYTEA NOT NULL,
    subject_masked TEXT NOT NULL CHECK (char_length(subject_masked) BETWEEN 1 AND 160),
    union_subject_digest BYTEA,
    proof_digest BYTEA NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    CHECK (char_length(product_id) BETWEEN 1 AND 160),
    CHECK (char_length(application_id) BETWEEN 1 AND 160),
    CHECK (tenant_id IS NULL OR char_length(tenant_id) BETWEEN 1 AND 160),
    CHECK (octet_length(subject_digest) = 32),
    CHECK (union_subject_digest IS NULL OR octet_length(union_subject_digest) = 32),
    CHECK (octet_length(proof_digest) = 32),
    CHECK (expires_at > created_at)
);

CREATE INDEX external_identity_proofs_scope_active_idx
    ON identity.external_identity_proofs (product_id, application_id, provider, expires_at)
    WHERE consumed_at IS NULL;

CREATE FUNCTION identity.enforce_external_proof_flow_scope() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM identity.external_auth_flows f
        WHERE f.flow_id = NEW.flow_id
          AND f.product_id = NEW.product_id
          AND f.application_id = NEW.application_id
          AND f.tenant_id IS NOT DISTINCT FROM NEW.tenant_id
          AND f.provider = NEW.provider
          AND f.provider_application_ref = NEW.provider_application_ref
    ) THEN
        RAISE EXCEPTION 'external identity proof scope must match its flow';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER external_identity_proof_flow_scope
BEFORE INSERT OR UPDATE ON identity.external_identity_proofs
FOR EACH ROW EXECUTE FUNCTION identity.enforce_external_proof_flow_scope();

CREATE TABLE identity.registration_verification_challenges (
    challenge_id TEXT PRIMARY KEY,
    continuation_digest BYTEA NOT NULL UNIQUE,
    product_id TEXT NOT NULL,
    application_id TEXT NOT NULL,
    tenant_id TEXT,
    identifier_type TEXT NOT NULL CHECK (identifier_type IN ('email', 'phone')),
    identifier_digest BYTEA NOT NULL,
    proof_digest BYTEA NOT NULL UNIQUE,
    delivery_id TEXT NOT NULL UNIQUE,
    delivery_status TEXT NOT NULL CHECK (delivery_status IN ('pending', 'active')),
    consumer_key_digest BYTEA,
    consumer_request_digest BYTEA,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    max_attempts INTEGER NOT NULL CHECK (max_attempts BETWEEN 1 AND 20),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    CHECK (char_length(product_id) BETWEEN 1 AND 160),
    CHECK (char_length(application_id) BETWEEN 1 AND 160),
    CHECK (tenant_id IS NULL OR char_length(tenant_id) BETWEEN 1 AND 160),
    CHECK (octet_length(continuation_digest) = 32),
    CHECK (octet_length(identifier_digest) = 32),
    CHECK (octet_length(proof_digest) = 32),
    CHECK (consumer_key_digest IS NULL OR octet_length(consumer_key_digest) = 32),
    CHECK (consumer_request_digest IS NULL OR octet_length(consumer_request_digest) = 32),
    CHECK ((consumer_key_digest IS NULL) = (consumer_request_digest IS NULL)),
    CHECK (expires_at > created_at),
    CHECK (attempt_count <= max_attempts)
);

CREATE INDEX registration_verification_scope_idx
    ON identity.registration_verification_challenges (product_id, application_id, identifier_type, identifier_digest, expires_at);

CREATE FUNCTION identity.enforce_external_auth_flow_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF ROW(NEW.flow_id,NEW.product_id,NEW.application_id,NEW.tenant_id,NEW.environment,NEW.provider,
           NEW.provider_application_ref,NEW.mode,NEW.return_target_code,NEW.return_target_uri,
           NEW.return_target_policy_version,NEW.state_digest,NEW.nonce_digest,NEW.pkce_challenge_digest,
           NEW.browser_session_digest,NEW.created_at,NEW.expires_at)
       IS DISTINCT FROM
       ROW(OLD.flow_id,OLD.product_id,OLD.application_id,OLD.tenant_id,OLD.environment,OLD.provider,
           OLD.provider_application_ref,OLD.mode,OLD.return_target_code,OLD.return_target_uri,
           OLD.return_target_policy_version,OLD.state_digest,OLD.nonce_digest,OLD.pkce_challenge_digest,
           OLD.browser_session_digest,OLD.created_at,OLD.expires_at) THEN
        RAISE EXCEPTION 'external authentication flow facts are immutable';
    END IF;
    IF NOT (
        (OLD.status = 'pending' AND NEW.status IN ('processing', 'failed', 'expired'))
        OR (OLD.status = 'processing' AND NEW.status IN ('consumed', 'failed', 'expired'))
    ) THEN
        RAISE EXCEPTION 'invalid external authentication flow transition';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION identity.enforce_external_identity_proof_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF ROW(NEW.proof_id,NEW.flow_id,NEW.product_id,NEW.application_id,NEW.tenant_id,NEW.provider,
           NEW.provider_application_ref,NEW.subject_digest,NEW.subject_masked,NEW.union_subject_digest,
           NEW.proof_digest,NEW.created_at,NEW.expires_at)
       IS DISTINCT FROM
       ROW(OLD.proof_id,OLD.flow_id,OLD.product_id,OLD.application_id,OLD.tenant_id,OLD.provider,
           OLD.provider_application_ref,OLD.subject_digest,OLD.subject_masked,OLD.union_subject_digest,
           OLD.proof_digest,OLD.created_at,OLD.expires_at)
       OR OLD.consumed_at IS NOT NULL OR NEW.consumed_at IS NULL THEN
        RAISE EXCEPTION 'invalid external identity proof transition';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION identity.reject_registration_verification_reuse() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF ROW(NEW.challenge_id,NEW.continuation_digest,NEW.product_id,NEW.application_id,NEW.tenant_id,
           NEW.identifier_type,NEW.identifier_digest,NEW.proof_digest,NEW.delivery_id,
           NEW.max_attempts,NEW.created_at,NEW.expires_at)
       IS DISTINCT FROM
       ROW(OLD.challenge_id,OLD.continuation_digest,OLD.product_id,OLD.application_id,OLD.tenant_id,
           OLD.identifier_type,OLD.identifier_digest,OLD.proof_digest,OLD.delivery_id,
           OLD.max_attempts,OLD.created_at,OLD.expires_at) THEN
        RAISE EXCEPTION 'registration verification challenge facts are immutable';
    END IF;
    IF NEW.attempt_count < OLD.attempt_count THEN
        RAISE EXCEPTION 'verification challenge attempt count cannot decrease';
    END IF;
    IF NOT (
        NEW.delivery_status = OLD.delivery_status
        OR (OLD.delivery_status = 'pending' AND NEW.delivery_status = 'active')
    ) THEN
        RAISE EXCEPTION 'invalid verification delivery transition';
    END IF;
    IF NEW.consumed_at IS NOT NULL AND NEW.delivery_status <> 'active' THEN
        RAISE EXCEPTION 'pending verification challenge cannot be consumed';
    END IF;
    IF OLD.consumer_key_digest IS NOT NULL AND (
        NEW.consumer_key_digest IS DISTINCT FROM OLD.consumer_key_digest
        OR NEW.consumer_request_digest IS DISTINCT FROM OLD.consumer_request_digest
    ) THEN
        RAISE EXCEPTION 'verification consumer binding is immutable';
    END IF;
    IF NEW.consumer_key_digest IS NOT NULL AND NEW.consumed_at IS NULL THEN
        RAISE EXCEPTION 'verification consumer binding requires consumption';
    END IF;
    IF OLD.consumed_at IS NOT NULL AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'consumed verification challenge is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION identity.enforce_recovery_delivery_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF ROW(NEW.challenge_id,NEW.continuation_digest,NEW.identifier_type,NEW.identifier_digest,
           NEW.matched_user_id,NEW.delivery_target_masked,NEW.proof_digest,NEW.max_attempts,
           NEW.created_at,NEW.expires_at)
       IS DISTINCT FROM
       ROW(OLD.challenge_id,OLD.continuation_digest,OLD.identifier_type,OLD.identifier_digest,
           OLD.matched_user_id,OLD.delivery_target_masked,OLD.proof_digest,OLD.max_attempts,
           OLD.created_at,OLD.expires_at) THEN
        RAISE EXCEPTION 'recovery challenge facts are immutable';
    END IF;
    IF NOT (
        NEW.delivery_status = OLD.delivery_status
        OR (OLD.delivery_status = 'pending' AND NEW.delivery_status = 'active')
    ) THEN
        RAISE EXCEPTION 'invalid recovery delivery transition';
    END IF;
    IF NEW.consumed_at IS NOT NULL AND NEW.delivery_status <> 'active' THEN
        RAISE EXCEPTION 'pending recovery challenge cannot be consumed';
    END IF;
    IF OLD.consumed_at IS NOT NULL AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'consumed recovery challenge is immutable';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER external_auth_flow_one_way
BEFORE UPDATE ON identity.external_auth_flows
FOR EACH ROW EXECUTE FUNCTION identity.enforce_external_auth_flow_transition();

CREATE TRIGGER external_identity_proof_one_way
BEFORE UPDATE ON identity.external_identity_proofs
FOR EACH ROW EXECUTE FUNCTION identity.enforce_external_identity_proof_transition();

CREATE TRIGGER registration_verification_one_way
BEFORE UPDATE ON identity.registration_verification_challenges
FOR EACH ROW EXECUTE FUNCTION identity.reject_registration_verification_reuse();

CREATE TRIGGER recovery_delivery_one_way
BEFORE UPDATE ON identity.recovery_challenges
FOR EACH ROW EXECUTE FUNCTION identity.enforce_recovery_delivery_transition();

CREATE SCHEMA notification;

CREATE TABLE notification.security_deliveries (
    delivery_id TEXT PRIMARY KEY,
    request_digest BYTEA NOT NULL,
    purpose TEXT NOT NULL CHECK (purpose IN ('registration_verify', 'password_recovery', 'account_security')),
    product_id TEXT NOT NULL,
    application_id TEXT NOT NULL,
    tenant_id TEXT,
    provider_ref TEXT NOT NULL CHECK (char_length(provider_ref) BETWEEN 1 AND 160),
    destination_type TEXT NOT NULL CHECK (destination_type IN ('email', 'phone', 'provider_subject')),
    protector_key_ref TEXT NOT NULL CHECK (char_length(protector_key_ref) BETWEEN 1 AND 256),
    payload_nonce BYTEA NOT NULL,
    payload_ciphertext BYTEA NOT NULL,
    payload_digest BYTEA NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'delivered', 'dead')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    max_attempts INTEGER NOT NULL CHECK (max_attempts BETWEEN 1 AND 20),
    next_attempt_at TIMESTAMPTZ NOT NULL,
    lease_owner TEXT,
    lease_started_at TIMESTAMPTZ,
    lease_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    delivered_at TIMESTAMPTZ,
    dead_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    trace_id TEXT NOT NULL,
    CHECK (char_length(product_id) BETWEEN 1 AND 160),
    CHECK (char_length(application_id) BETWEEN 1 AND 160),
    CHECK (tenant_id IS NULL OR char_length(tenant_id) BETWEEN 1 AND 160),
    CHECK (octet_length(request_digest) = 32),
    CHECK (octet_length(payload_digest) = 32),
    CHECK (octet_length(payload_nonce) BETWEEN 12 AND 32),
    CHECK (octet_length(payload_ciphertext) > 16),
    CHECK (attempt_count <= max_attempts),
    CHECK ((lease_owner IS NULL) = (lease_started_at IS NULL)),
    CHECK ((lease_owner IS NULL) = (lease_expires_at IS NULL)),
    CHECK (lease_expires_at IS NULL OR lease_expires_at > lease_started_at),
    CHECK ((status = 'processing') = (lease_owner IS NOT NULL)),
    CHECK (expires_at > created_at),
    CHECK ((status = 'delivered') = (delivered_at IS NOT NULL)),
    CHECK ((status = 'dead') = (dead_at IS NOT NULL))
);

CREATE INDEX security_deliveries_pending_idx
    ON notification.security_deliveries (next_attempt_at, created_at)
    WHERE status IN ('pending', 'processing');

CREATE TABLE notification.security_delivery_attempts (
    attempt_id TEXT PRIMARY KEY,
    delivery_id TEXT NOT NULL REFERENCES notification.security_deliveries(delivery_id),
    attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
    outcome TEXT NOT NULL CHECK (outcome IN ('delivered', 'retryable_failure', 'terminal_failure')),
    provider_message_digest BYTEA,
    error_code TEXT,
    error_digest BYTEA,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    UNIQUE (delivery_id, attempt_number),
    CHECK (error_digest IS NULL OR octet_length(error_digest) = 32),
    CHECK (provider_message_digest IS NULL OR octet_length(provider_message_digest) = 32),
    CHECK (finished_at >= started_at)
);

CREATE TABLE notification.outbox_events (
    event_id TEXT PRIMARY KEY,
    delivery_id TEXT NOT NULL REFERENCES notification.security_deliveries(delivery_id),
    event_type TEXT NOT NULL CHECK (event_type IN ('notification.security-delivery-requested.v1')),
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    occurred_at TIMESTAMPTZ NOT NULL,
    next_attempt_at TIMESTAMPTZ NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    lease_owner TEXT,
    lease_expires_at TIMESTAMPTZ,
    published_at TIMESTAMPTZ,
    dead BOOLEAN NOT NULL DEFAULT FALSE,
    last_error_code TEXT,
    CHECK ((lease_owner IS NULL) = (lease_expires_at IS NULL)),
    CHECK (published_at IS NULL OR (dead = FALSE AND lease_owner IS NULL)),
    CHECK (dead = FALSE OR (published_at IS NULL AND lease_owner IS NULL))
);

CREATE INDEX notification_outbox_pending_idx
    ON notification.outbox_events (next_attempt_at, occurred_at)
    WHERE published_at IS NULL AND dead = FALSE;

CREATE FUNCTION notification.reject_attempt_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'security delivery attempts are immutable';
END;
$$;

CREATE TRIGGER security_delivery_attempt_immutable
BEFORE UPDATE OR DELETE ON notification.security_delivery_attempts
FOR EACH ROW EXECUTE FUNCTION notification.reject_attempt_update();

CREATE FUNCTION notification.enforce_security_delivery_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'security delivery rows are immutable';
    END IF;
    IF NEW.delivery_id <> OLD.delivery_id
        OR NEW.request_digest <> OLD.request_digest
        OR NEW.purpose <> OLD.purpose
        OR NEW.product_id <> OLD.product_id
        OR NEW.application_id <> OLD.application_id
        OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.provider_ref <> OLD.provider_ref
        OR NEW.destination_type <> OLD.destination_type
        OR NEW.protector_key_ref <> OLD.protector_key_ref
        OR NEW.payload_nonce <> OLD.payload_nonce
        OR NEW.payload_ciphertext <> OLD.payload_ciphertext
        OR NEW.payload_digest <> OLD.payload_digest
        OR NEW.max_attempts <> OLD.max_attempts
        OR NEW.created_at <> OLD.created_at
        OR NEW.expires_at <> OLD.expires_at
        OR NEW.trace_id <> OLD.trace_id THEN
        RAISE EXCEPTION 'security delivery facts are immutable';
    END IF;
    IF OLD.status IN ('delivered', 'dead') THEN
        RAISE EXCEPTION 'security delivery terminal state is immutable';
    END IF;
    IF OLD.status = 'pending' THEN
        IF NEW.status = 'processing' THEN
            IF NEW.attempt_count <> OLD.attempt_count + 1 OR NEW.lease_owner IS NULL THEN
                RAISE EXCEPTION 'invalid security delivery claim';
            END IF;
        ELSIF NEW.status = 'dead' THEN
            IF NEW.attempt_count <> OLD.attempt_count OR NEW.lease_owner IS NOT NULL THEN
                RAISE EXCEPTION 'invalid pending security delivery terminal transition';
            END IF;
        ELSE
            RAISE EXCEPTION 'invalid pending security delivery transition';
        END IF;
    ELSIF OLD.status = 'processing' THEN
        IF NEW.status NOT IN ('pending', 'delivered', 'dead')
            OR NEW.attempt_count <> OLD.attempt_count
            OR NEW.lease_owner IS NOT NULL
            OR NOT EXISTS (
                SELECT 1 FROM notification.security_delivery_attempts
                WHERE delivery_id = OLD.delivery_id AND attempt_number = OLD.attempt_count
            ) THEN
            RAISE EXCEPTION 'invalid processing security delivery completion';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION notification.enforce_outbox_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'notification outbox rows are immutable';
    END IF;
    IF NEW.event_id <> OLD.event_id
        OR NEW.delivery_id <> OLD.delivery_id
        OR NEW.event_type <> OLD.event_type
        OR NEW.payload <> OLD.payload
        OR NEW.occurred_at <> OLD.occurred_at THEN
        RAISE EXCEPTION 'notification outbox facts are immutable';
    END IF;
    IF OLD.published_at IS NOT NULL OR OLD.dead THEN
        RAISE EXCEPTION 'notification outbox terminal state is immutable';
    END IF;
    IF OLD.lease_owner IS NULL THEN
        IF NEW.lease_owner IS NULL OR NEW.attempt_count <> OLD.attempt_count + 1 OR NEW.published_at IS NOT NULL OR NEW.dead THEN
            RAISE EXCEPTION 'invalid notification outbox claim';
        END IF;
    ELSE
        IF NEW.lease_owner IS NOT NULL THEN
            IF OLD.lease_expires_at > clock_timestamp()
                OR NEW.lease_owner = OLD.lease_owner
                OR NEW.attempt_count <> OLD.attempt_count + 1
                OR NEW.published_at IS NOT NULL
                OR NEW.dead THEN
                RAISE EXCEPTION 'invalid notification outbox reclaim';
            END IF;
        ELSIF NEW.attempt_count <> OLD.attempt_count THEN
            RAISE EXCEPTION 'invalid notification outbox completion';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER security_delivery_one_way
BEFORE UPDATE OR DELETE ON notification.security_deliveries
FOR EACH ROW EXECUTE FUNCTION notification.enforce_security_delivery_transition();

CREATE TRIGGER notification_outbox_one_way
BEFORE UPDATE OR DELETE ON notification.outbox_events
FOR EACH ROW EXECUTE FUNCTION notification.enforce_outbox_transition();

COMMIT;
