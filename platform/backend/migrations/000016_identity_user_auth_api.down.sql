BEGIN;

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
