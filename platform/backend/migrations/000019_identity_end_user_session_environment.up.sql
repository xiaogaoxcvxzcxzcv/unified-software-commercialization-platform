BEGIN;

ALTER TABLE identity.end_user_sessions
    ADD COLUMN environment TEXT,
    ADD CONSTRAINT end_user_sessions_environment_valid
        CHECK (environment IS NULL OR environment IN ('local', 'test', 'production'));

ALTER TABLE identity.hosted_auth_proofs
    ADD COLUMN environment TEXT,
    ADD CONSTRAINT hosted_auth_proofs_environment_valid
        CHECK (environment IS NULL OR environment IN ('local', 'test', 'production'));

CREATE INDEX end_user_sessions_environment_scope_active_idx
    ON identity.end_user_sessions (environment, product_id, application_id, tenant_id, user_id)
    WHERE revoked_at IS NULL AND environment IS NOT NULL;

CREATE INDEX hosted_auth_proofs_environment_scope_active_idx
    ON identity.hosted_auth_proofs (environment, product_id, application_id, tenant_id, expires_at)
    WHERE consumed_at IS NULL AND environment IS NOT NULL;

COMMIT;
