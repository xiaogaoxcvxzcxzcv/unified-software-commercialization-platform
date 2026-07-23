BEGIN;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM identity.end_user_sessions WHERE environment IS NOT NULL)
        OR EXISTS (SELECT 1 FROM identity.hosted_auth_proofs WHERE environment IS NOT NULL) THEN
        RAISE EXCEPTION 'migration 000019 rollback refused: Identity environment facts exist';
    END IF;
END;
$$;

DROP INDEX IF EXISTS identity.end_user_sessions_environment_scope_active_idx;
DROP INDEX IF EXISTS identity.hosted_auth_proofs_environment_scope_active_idx;
ALTER TABLE identity.hosted_auth_proofs DROP CONSTRAINT IF EXISTS hosted_auth_proofs_environment_valid;
ALTER TABLE identity.hosted_auth_proofs DROP COLUMN IF EXISTS environment;
ALTER TABLE identity.end_user_sessions DROP CONSTRAINT IF EXISTS end_user_sessions_environment_valid;
ALTER TABLE identity.end_user_sessions DROP COLUMN IF EXISTS environment;

COMMIT;
