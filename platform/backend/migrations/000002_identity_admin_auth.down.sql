BEGIN;

DROP TABLE IF EXISTS identity.outbox_events;
DROP TABLE IF EXISTS identity.admin_login_failures;
DROP TABLE IF EXISTS identity.admin_session_tokens;
DROP TABLE IF EXISTS identity.admin_sessions;
DROP TABLE IF EXISTS identity.user_credentials;
DROP TABLE IF EXISTS identity.users;
DROP SCHEMA IF EXISTS identity;

COMMIT;
