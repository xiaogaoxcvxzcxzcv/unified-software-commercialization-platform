BEGIN;

DROP INDEX IF EXISTS identity.admin_sessions_controlled_client_active_idx;

ALTER TABLE identity.admin_sessions
    DROP CONSTRAINT IF EXISTS admin_sessions_controlled_client_binding_check,
    DROP CONSTRAINT IF EXISTS admin_sessions_controlled_client_fk,
    DROP COLUMN IF EXISTS controlled_client_credential_id,
    DROP COLUMN IF EXISTS controlled_client_id;

DROP INDEX IF EXISTS identity.admin_auth_client_credentials_active_idx;
DROP TABLE IF EXISTS identity.admin_auth_client_credentials;
DROP TABLE IF EXISTS identity.admin_auth_clients;

COMMIT;
