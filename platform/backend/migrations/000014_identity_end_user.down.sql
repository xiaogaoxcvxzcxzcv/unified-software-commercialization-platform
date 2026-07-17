BEGIN;

DROP TABLE IF EXISTS identity.end_user_idempotency_records;
DROP TABLE IF EXISTS identity.external_identities;
DROP TABLE IF EXISTS identity.recovery_challenges;
DROP FUNCTION IF EXISTS identity.reject_recovery_reuse();
DROP TABLE IF EXISTS identity.end_user_session_tokens;
DROP TABLE IF EXISTS identity.end_user_sessions;
DROP TABLE IF EXISTS identity.user_profiles;
DROP TABLE IF EXISTS identity.user_identifiers;

ALTER TABLE identity.user_credentials
    DROP COLUMN IF EXISTS password_changed_at,
    DROP COLUMN IF EXISTS credential_version,
    DROP COLUMN IF EXISTS password_algorithm,
    DROP COLUMN IF EXISTS credential_status;

ALTER TABLE identity.users
    DROP COLUMN IF EXISTS security_changed_at,
    DROP COLUMN IF EXISTS user_version;

COMMIT;
