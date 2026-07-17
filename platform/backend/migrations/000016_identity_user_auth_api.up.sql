BEGIN;

CREATE TABLE identity.end_user_login_failures (
    scope_id TEXT NOT NULL,
    identifier_digest BYTEA NOT NULL,
    source_digest BYTEA NOT NULL,
    failure_count INTEGER NOT NULL CHECK (failure_count >= 0),
    window_started_at TIMESTAMPTZ NOT NULL,
    last_failed_at TIMESTAMPTZ NOT NULL,
    blocked_until TIMESTAMPTZ,
    PRIMARY KEY (scope_id, identifier_digest, source_digest),
    CHECK (char_length(scope_id) BETWEEN 1 AND 512),
    CHECK (octet_length(identifier_digest) = 32),
    CHECK (octet_length(source_digest) = 32)
);

ALTER TABLE identity.end_user_session_tokens
    ADD COLUMN rotation_request_digest BYTEA,
    ADD COLUMN rotation_recovery_expires_at TIMESTAMPTZ,
    ADD CONSTRAINT end_user_session_tokens_rotation_request_digest_length
        CHECK (rotation_request_digest IS NULL OR octet_length(rotation_request_digest) = 32),
    ADD CONSTRAINT end_user_session_tokens_rotation_recovery_pair
        CHECK ((rotation_request_digest IS NULL) = (rotation_recovery_expires_at IS NULL)),
    ADD CONSTRAINT end_user_session_tokens_rotation_refresh_only
        CHECK (rotation_request_digest IS NULL OR (token_type = 'refresh' AND consumed_at IS NOT NULL)),
    ADD CONSTRAINT end_user_session_tokens_rotation_window_after_consumption
        CHECK (rotation_recovery_expires_at IS NULL OR rotation_recovery_expires_at > consumed_at);

CREATE INDEX end_user_session_tokens_rotation_recovery_idx
    ON identity.end_user_session_tokens (rotation_request_digest, rotation_recovery_expires_at)
    WHERE rotation_request_digest IS NOT NULL;

ALTER TABLE product_user_access.idempotency_records
    ADD COLUMN audit_id TEXT,
    ADD CONSTRAINT product_user_access_idempotency_audit_id_format
        CHECK (audit_id IS NULL OR char_length(audit_id) BETWEEN 16 AND 160);

ALTER TABLE product_user_access.outbox_events
    DROP CONSTRAINT outbox_events_event_type_check,
    ADD CONSTRAINT outbox_events_event_type_check CHECK (event_type IN (
        'product-user-access.status-changed.v1',
        'product-user-access.session-revocation-requested.v1',
        'product-user-access.command-audited.v1'
    ));

COMMIT;
