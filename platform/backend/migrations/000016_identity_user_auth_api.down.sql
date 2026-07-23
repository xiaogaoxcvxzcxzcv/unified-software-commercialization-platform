BEGIN;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM identity.end_user_idempotency_records WHERE response_document IS NOT NULL
    ) OR EXISTS (
        SELECT 1 FROM identity.end_user_session_tokens
        WHERE rotation_request_digest IS NOT NULL OR rotation_recovery_expires_at IS NOT NULL
    ) OR EXISTS (
        SELECT 1 FROM identity.end_user_login_failures
    ) THEN
        RAISE EXCEPTION 'migration 000016 rollback refused because durable identity API state exists';
    END IF;

    IF EXISTS (
        SELECT 1 FROM product_user_access.idempotency_records WHERE audit_id IS NOT NULL
    ) OR EXISTS (
        SELECT 1 FROM product_user_access.outbox_events WHERE payload ? 'audit_id'
    ) THEN
        RAISE EXCEPTION 'migration 000016 rollback refused because durable product user access audit identities exist';
    END IF;
END;
$$;

ALTER TABLE product_user_access.outbox_events
    DROP CONSTRAINT outbox_events_event_type_check,
    ADD CONSTRAINT outbox_events_event_type_check CHECK (event_type IN (
        'product-user-access.status-changed.v1',
        'product-user-access.session-revocation-requested.v1'
    ));

ALTER TABLE product_user_access.idempotency_records
    DROP CONSTRAINT IF EXISTS product_user_access_idempotency_audit_id_format,
    DROP COLUMN IF EXISTS audit_id;

ALTER TABLE identity.end_user_idempotency_records
    DROP CONSTRAINT IF EXISTS end_user_idempotency_response_document_object,
    DROP COLUMN IF EXISTS response_document;

DROP INDEX IF EXISTS identity.end_user_session_tokens_rotation_recovery_idx;

ALTER TABLE identity.end_user_session_tokens
    DROP CONSTRAINT IF EXISTS end_user_session_tokens_rotation_window_after_consumption,
    DROP CONSTRAINT IF EXISTS end_user_session_tokens_rotation_refresh_only,
    DROP CONSTRAINT IF EXISTS end_user_session_tokens_rotation_recovery_pair,
    DROP CONSTRAINT IF EXISTS end_user_session_tokens_rotation_request_digest_length,
    DROP COLUMN IF EXISTS rotation_recovery_expires_at,
    DROP COLUMN IF EXISTS rotation_request_digest;

DROP TABLE IF EXISTS identity.end_user_login_failures;

COMMIT;
